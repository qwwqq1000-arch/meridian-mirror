#!/bin/sh
# Kill-switch: remove the redsocks redirect and restore DIRECT egress instantly.
# Run inside the container: docker exec <container> /app/bin/redsocks-off.sh
iptables -t nat -D OUTPUT -p tcp -j MERIDIAN_REDSOCKS 2>/dev/null
iptables -t nat -F MERIDIAN_REDSOCKS 2>/dev/null
iptables -t nat -X MERIDIAN_REDSOCKS 2>/dev/null
pkill -x redsocks 2>/dev/null
echo "[redsocks-off] direct egress restored (redsocks + iptables redirect removed)"
