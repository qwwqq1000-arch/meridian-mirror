#!/bin/sh
# Auto-set timezone to match exit IP geolocation.
# Runs after the egress proxy is configured (redsocks) so the lookups go out
# through the same exit IP the account's requests use. A container clock in a
# different timezone than the exit IP is a mismatch that risk detection can flag.

log() { echo "[auto-timezone] $*"; }

EXIT_IP=$(wget -qO- -T5 http://api.ipify.org 2>/dev/null)
if [ -z "$EXIT_IP" ]; then
  log "SKIP — cannot detect exit IP"
  return 0 2>/dev/null || exit 0
fi

TZ_DETECTED=$(wget -qO- -T5 "http://ip-api.com/line/${EXIT_IP}?fields=timezone" 2>/dev/null)
if [ -z "$TZ_DETECTED" ]; then
  log "SKIP — cannot lookup timezone for $EXIT_IP"
  return 0 2>/dev/null || exit 0
fi

export TZ="$TZ_DETECTED"
echo "$TZ_DETECTED" > /etc/timezone 2>/dev/null || true
ln -sf "/usr/share/zoneinfo/$TZ_DETECTED" /etc/localtime 2>/dev/null || true

log "OK — exit IP $EXIT_IP → TZ=$TZ_DETECTED ($(date +%Z))"
