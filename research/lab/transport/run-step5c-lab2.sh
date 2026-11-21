#!/bin/bash
# Step 5c, new settings again after replacing the sampled RTT estimate with the transport's
# (PeerConnRTT). Same runs as run-step5c-lab.sh; the old-settings runs there stand.
cd "$(dirname "$0")"
D=/c/Users/bruno/Documents/git/streaming-research/data/transport-lab
export UTPLAB=utplab-fork.exe
BASE="-secs 30 -responsive -offsetfirst"
NEW="-qmin 500ms -qrtts 4 -critical 1048576"
SEEK="-reads 0s@0+32768,12s@125829120+262144 -closehead"
run() { (sleep 10; python qbt-peers.py > $D/$1.qbt.txt 2>&1) & ./run-lab.sh "$@"; wait; sleep 60; }
run Q-L1-new "" "$BASE $NEW"
run Q-L2-new "" "$BASE $NEW $SEEK"
run Q-L2-new2 "" "$BASE $NEW $SEEK"
echo "### $(date +%T) all done"
