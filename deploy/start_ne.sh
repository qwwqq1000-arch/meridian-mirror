#!/bin/bash
pkill -f native-egress 2>/dev/null
sleep 1
cd /root/new-meridian
NATIVE_EGRESS_ADDR=127.0.0.1:9999 MERIDIAN_NATIVE_DEBUG=1 nohup ./native-egress/native-egress >> /tmp/ne_live.log 2>&1 &
echo "NE started pid=$!"
