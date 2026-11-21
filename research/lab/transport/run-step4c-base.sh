#!/bin/bash
# Step 4c baseline (4b build): a 576-byte path keeps qBittorrent at its 528-byte uTP payload floor.
cd "$(dirname "$0")"
export UTPLAB=utplab-fork.exe
./run-lab.sh M0-mtu "-mtu 548" "-secs 45"; sleep 60
./run-lab.sh M0-S10 "-mtu 548 -stride 50 -stride-n 10 -stride-at 20s" "-secs 45"
echo "### $(date +%T) all done"
