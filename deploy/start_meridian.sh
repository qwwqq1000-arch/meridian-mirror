#!/bin/bash
pkill -f "bun run bin/cli.ts" 2>/dev/null
sleep 2
cd /root/new-meridian
export MERIDIAN_TELEMETRY_PERSIST=1
nohup /root/.bun/bin/bun run bin/cli.ts >> /tmp/meridian.log 2>&1 &
echo "Meridian started pid=$!"
