#!/bin/sh
# seed-config.sh — seed baked-in default config into ~/.config/meridian on boot
# so a freshly-installed machine comes up with the same configuration as the
# committed code (the per-machine egress proxy is the ONLY thing set by hand).
#
# Semantics: baked defaults are the FLOOR. Anything already set (UI/CLI changes,
# and the per-machine egressProxy) WINS — we never clobber a machine's own
# config. A fresh machine (empty volume) gets the full default set; an existing
# machine gets any new default keys filled in. Run as root from the entrypoint.
set -u

DEFAULTS_DIR=/app/config/defaults
TARGET_DIR=/home/claude/.config/meridian
mkdir -p "$TARGET_DIR"

for f in settings.json sdk-features.json; do
  d="$DEFAULTS_DIR/$f"
  t="$TARGET_DIR/$f"
  [ -f "$d" ] || continue
  if [ -f "$t" ]; then
    # Merge: defaults as base, existing overrides (recursive `*`). Only replace
    # the target if jq succeeds, so a parse error can never lose existing config.
    if command -v jq >/dev/null 2>&1; then
      if jq -s '.[0] * .[1]' "$d" "$t" > "$t.seedtmp" 2>/dev/null; then
        mv "$t.seedtmp" "$t"
      else
        rm -f "$t.seedtmp"
      fi
    fi
  else
    cp "$d" "$t"
    echo "[seed-config] seeded default $f"
  fi
done

chown -R claude:claude "$TARGET_DIR" 2>/dev/null
