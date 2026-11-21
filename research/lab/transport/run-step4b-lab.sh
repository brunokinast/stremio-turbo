#!/bin/bash
# Step 4b non-regression: reorder bytes count against the advertised uTP window. S10 and L40 first,
# plain last (warm seeder). Compare with Q-plain2/3, Q-S10 and F-L40.
cd "$(dirname "$0")"
export UTPLAB=utplab-fork.exe
./run-lab.sh W-S10 "-stride 50 -stride-n 10 -stride-at 20s" "-secs 45"; sleep 60
./run-lab.sh W-L40 "-burst 40" "-secs 60"; sleep 60
./run-lab.sh W-plain "" "-secs 60"
echo "### $(date +%T) all done"
