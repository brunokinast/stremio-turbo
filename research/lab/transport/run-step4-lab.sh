#!/bin/bash
# Step 4 acceptance: the request queue (InitialRequestQueue 32, RequestLowWater 0.5) through the
# relay, three plain runs and one S10, against step 3's F-plain/F-S10. One at a time, 60 s apart.
cd "$(dirname "$0")"
export UTPLAB=utplab-fork.exe
./run-lab.sh Q-plain1 "" "-secs 60"; sleep 60
./run-lab.sh Q-plain2 "" "-secs 60"; sleep 60
./run-lab.sh Q-plain3 "" "-secs 60"; sleep 60
./run-lab.sh Q-S10 "-stride 50 -stride-n 10 -stride-at 20s" "-secs 45"
echo "### $(date +%T) all done"
