#!/bin/sh
# Docker entrypoint:
# 1. Fix volume permissions (created as root, need claude ownership)
# 2. Symlink .claude.json into persistent volume

CLAUDE_DIR="/home/claude/.claude"
CLAUDE_JSON="/home/claude/.claude.json"
CLAUDE_JSON_VOL="$CLAUDE_DIR/.claude.json"

# Fix ownership if volume was created as root
if [ -d "$CLAUDE_DIR" ] && [ ! -w "$CLAUDE_DIR" ]; then
  echo "[entrypoint] Fixing volume permissions..."
fi

# Symlink .claude.json into volume so it persists across restarts
if [ -f "$CLAUDE_JSON_VOL" ] && [ ! -f "$CLAUDE_JSON" ]; then
  ln -sf "$CLAUDE_JSON_VOL" "$CLAUDE_JSON"
elif [ -f "$CLAUDE_JSON" ] && [ ! -L "$CLAUDE_JSON" ] && [ -w "$CLAUDE_DIR" ]; then
  cp "$CLAUDE_JSON" "$CLAUDE_JSON_VOL" 2>/dev/null
  rm -f "$CLAUDE_JSON"
  ln -sf "$CLAUDE_JSON_VOL" "$CLAUDE_JSON"
fi

# --- Egress proxy enforcement + privilege drop (root only) ---
# The container now starts as root so we can install the redsocks + iptables
# transparent proxy (forces ALL egress through the configured SOCKS5, self-tests
# and rolls back on failure). Then we drop to the unprivileged `claude` user to
# run meridian — exactly as before.
if [ "$(id -u)" = "0" ]; then
  # (volume ownership is handled by the compose `init` service that chowns to 1000)
  # Seed baked-in default config so fresh machines match the committed code.
  [ -x /app/bin/seed-config.sh ] && /app/bin/seed-config.sh
  if [ -x /app/bin/redsocks-setup.sh ]; then
    /app/bin/redsocks-setup.sh
    if [ $? -ne 0 ]; then
      echo "[entrypoint] FATAL: egress proxy required but failed — refusing to start without proxy"
      exit 1
    fi
  fi
  # Match container timezone to the exit IP (must run after redsocks so the
  # lookup exits through the proxy, and while still root to write /etc/localtime).
  if [ -x /app/bin/auto-timezone.sh ]; then
    . /app/bin/auto-timezone.sh
  fi
  if command -v su-exec >/dev/null 2>&1; then
    # su-exec setuids but does NOT reset HOME — meridian (os.homedir) must see
    # /home/claude so it reads/writes ~/.config/meridian + ~/.claude correctly.
    export HOME=/home/claude
    exec su-exec claude:claude "$@"
  fi
fi

exec "$@"
