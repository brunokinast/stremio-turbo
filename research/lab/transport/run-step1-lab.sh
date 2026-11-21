#!/bin/bash
# Step 1 Part A: the remaining lab runs, one at a time, 60 s apart.
cd "$(dirname "$0")"
run() { echo "### $(date +%T) $*"; "$@"; sleep 60; }
for i in 1 2 3 4 5; do
  run ./run-lab.sh B$i "" "-secs 60"
  UTPLAB_OSBUF=4194304 run ./run-lab.sh O$i "" "-secs 60"
done
run ./run-lab.sh L40 "-burst 40" "-secs 60"
UTPLAB_BIGSACK=1 run ./run-lab.sh L40-bigsack "-burst 40" "-secs 60"
run ./run-lab.sh ST "" "-secs 60 -stall 200ms -stall-every 8s"
UTPLAB_OSBUF=4194304 run ./run-lab.sh ST-osbuf "" "-secs 60 -stall 200ms -stall-every 8s"
./run-lab.sh RQT0 "" "-secs 60 -rqt 0"
echo "### $(date +%T) all done"
