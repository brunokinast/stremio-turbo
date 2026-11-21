#!/bin/bash
# Step 4c after G1-G4 (2016-slot reorder buffer, slot-bounded window): the 576-byte path runs
# against M0-*, then the full-size S10/L40 non-regression runs against W-S10/W-L40.
cd "$(dirname "$0")"
export UTPLAB=utplab-fork.exe
./run-lab.sh M1-S10 "-mtu 548 -stride 50 -stride-n 10 -stride-at 20s" "-secs 45"; sleep 60
./run-lab.sh M1-mtu "-mtu 548" "-secs 45"; sleep 60
./run-lab.sh X-S10 "-stride 50 -stride-n 10 -stride-at 20s" "-secs 45"; sleep 60
./run-lab.sh X-L40 "-burst 40" "-secs 60"
echo "### $(date +%T) all done"
