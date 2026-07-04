#!/bin/sh
# redsocks-setup.sh — force ALL container egress through the saved SOCKS5 proxy
# using redsocks + iptables (transparent proxy). Runs as ROOT from the docker
# entrypoint, before privileges are dropped to `claude`.
#
# Guarantees: once active, EVERY outbound TCP connection (claude.exe, the
# native-egress sidecar, meridian's own Node fetches — everything) is forced
# through the proxy at the kernel level. There is no application-level fallback
# to leak around it → fail-closed (if the proxy dies, egress stops).
#
# Safety: the setup SELF-TESTS (egress IP must change to the proxy's) and ROLLS
# BACK to direct egress if the proxy is unreachable at setup time, so a bad
# deploy/config can never brick the node. The container's iptables live only in
# the container netns — they never touch the host, so the host stays reachable
# and `docker exec <c> /app/bin/redsocks-off.sh` (or a restart) always recovers.
set -u

REDSOCKS_PORT=12345
SETTINGS="/home/claude/.config/meridian/settings.json"
CHAIN="MERIDIAN_REDSOCKS"

log() { echo "[redsocks-setup] $*"; }

flush_rules() {
  iptables -t nat -D OUTPUT -p tcp -j "$CHAIN" 2>/dev/null
  iptables -t nat -F "$CHAIN" 2>/dev/null
  iptables -t nat -X "$CHAIN" 2>/dev/null
}

# Clean slate (handles container restart with stale rules / redsocks).
flush_rules
pkill -x redsocks 2>/dev/null

# --- Resolve the proxy: UI source of truth (settings.json -> egressProxy),
#     else MERIDIAN_EGRESS_PROXY env. ---
PROXY="${MERIDIAN_EGRESS_PROXY:-}"
if [ -z "$PROXY" ] && [ -f "$SETTINGS" ]; then
  PROXY=$(grep -o '"egressProxy"[[:space:]]*:[[:space:]]*"[^"]*"' "$SETTINGS" 2>/dev/null \
    | sed 's/.*"egressProxy"[[:space:]]*:[[:space:]]*"//; s/"$//')
fi

if [ -z "$PROXY" ]; then
  log "no egress proxy configured — container will start but API requests blocked until proxy is set via web UI"
  # Write marker so meridian knows to block API requests
  touch /tmp/.no-egress-proxy
  exit 0
fi

# --- Parse scheme://[user:pass@]host:port ---
p="${PROXY#*://}"
case "$p" in
  *@*) auth="${p%@*}"; hostport="${p##*@}"; PUSER="${auth%%:*}"; PPASS="${auth#*:}" ;;
  *)   hostport="$p";  PUSER="";           PPASS="" ;;
esac
PHOST="${hostport%%:*}"
PPORT="${hostport##*:}"
# %-decode user/pass (parseProxy stores them %-encoded in the URL form).
PUSER=$(printf '%b' "$(printf '%s' "$PUSER" | sed 's/+/ /g; s/%/\\x/g')")
PPASS=$(printf '%b' "$(printf '%s' "$PPASS" | sed 's/+/ /g; s/%/\\x/g')")

if [ -z "$PHOST" ] || [ -z "$PPORT" ]; then
  log "could not parse proxy '$PROXY' — BLOCKING STARTUP (fail-closed)"
  exit 1
fi

# Resolve host -> IP for the loop-prevention RETURN rule (no-op if already an IP).
PIP=$(getent hosts "$PHOST" 2>/dev/null | awk '{print $1; exit}')
[ -z "$PIP" ] && PIP="$PHOST"

# --- Baseline: our DIRECT exit IP (before any redirect) ---
DIRECT_IP=$(wget -qO- -T 10 http://api.ipify.org 2>/dev/null)
log "direct exit IP=${DIRECT_IP:-unknown}; proxy=$PHOST:$PPORT (ip $PIP)"

# --- redsocks config ---
{
  echo "base {"
  echo "  log_debug = off; log_info = on; log = \"stderr\"; daemon = off;"
  echo "  redirector = iptables;"
  echo "}"
  echo "redsocks {"
  echo "  local_ip = 127.0.0.1; local_port = $REDSOCKS_PORT;"
  echo "  ip = $PIP; port = $PPORT; type = socks5;"
  [ -n "$PUSER" ] && echo "  login = \"$PUSER\";"
  [ -n "$PPASS" ] && echo "  password = \"$PPASS\";"
  echo "}"
} > /etc/redsocks.conf

redsocks -c /etc/redsocks.conf >/var/log/redsocks.log 2>&1 &
sleep 1
if ! pgrep -x redsocks >/dev/null 2>&1; then
  log "redsocks failed to start — BLOCKING STARTUP (fail-closed)"; sed 's/^/[redsocks] /' /var/log/redsocks.log 2>/dev/null; exit 1
fi

# --- iptables: redirect all new outbound TCP to redsocks, except the proxy
#     itself (loop) and local/private ranges (so localhost + the sidecar +
#     inbound:3456 are untouched). ---
iptables -t nat -N "$CHAIN" 2>/dev/null
iptables -t nat -A "$CHAIN" -d "$PIP" -j RETURN
for net in 0.0.0.0/8 10.0.0.0/8 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.168.0.0/16 100.64.0.0/10; do
  iptables -t nat -A "$CHAIN" -d "$net" -j RETURN
done
iptables -t nat -A "$CHAIN" -p tcp -j REDIRECT --to-ports "$REDSOCKS_PORT"
iptables -t nat -A OUTPUT -p tcp -j "$CHAIN"

# --- SELF-TEST: egress must now leave via the proxy (IP changed & non-empty) ---
sleep 1
PROXIED_IP=$(wget -qO- -T 15 http://api.ipify.org 2>/dev/null)
if [ -n "$PROXIED_IP" ] && { [ "$PROXIED_IP" = "$PIP" ] || [ "$PROXIED_IP" != "$DIRECT_IP" ]; }; then
  log "ACTIVE ✓ all egress forced through proxy (exit IP=$PROXIED_IP, was $DIRECT_IP). FAIL-CLOSED."
  rm -f /tmp/.no-egress-proxy
  exit 0
fi

log "SELF-TEST FAILED (proxied exit IP='$PROXIED_IP', direct was '$DIRECT_IP') — BLOCKING STARTUP (fail-closed)"
flush_rules
pkill -x redsocks 2>/dev/null
exit 1
