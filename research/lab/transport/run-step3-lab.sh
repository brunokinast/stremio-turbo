#!/bin/bash
# Step 3 acceptance: the go-utp fork (full selective ack, ack timer, 4 MiB socket buffer) through the
# relay, against step 1's S10 and L40 runs and a plain run. One at a time, 60 s apart.
cd "$(dirname "$0")"
export UTPLAB=utplab-fork.exe
./run-lab.sh F-plain "" "-secs 60"; sleep 60
./run-lab.sh F-S10 "-stride 50 -stride-n 10 -stride-at 20s" "-secs 45"; sleep 60
./run-lab.sh F-L40 "-burst 40" "-secs 60"
echo "### $(date +%T) all done"
