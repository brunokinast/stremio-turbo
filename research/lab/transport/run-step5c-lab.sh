#!/bin/bash
# Step 5c: RTT-scaled request queue and critical chunks, one peer through the relay (150 ms each way).
# L1: plain download, 30 s (throughput guard). L2: mpv-style head read, then at +12 s a seek: the
# head reader closes and 256 KiB at 120 MiB are read (time to bytes behind a full queue).
# Old = RequestQueueTime 3 s only; new = 4 round trips, at least 0.5 s, plus 1 MiB critical bytes.
# Both have step 5a's responsive readers and offset-first order. qBittorrent's view of the peer is
# saved at +10 s in every run (runs where the seeder never sends data).
cd "$(dirname "$0")"
D=/c/Users/bruno/Documents/git/streaming-research/data/transport-lab
export UTPLAB=utplab-fork.exe
BASE="-secs 30 -responsive -offsetfirst"
NEW="-qmin 500ms -qrtts 4 -critical 1048576"
SEEK="-reads 0s@0+32768,12s@125829120+262144 -closehead"
run() { (sleep 10; python qbt-peers.py > $D/$1.qbt.txt 2>&1) & ./run-lab.sh "$@"; wait; sleep 60; }
run Q-L1-old "" "$BASE"
run Q-L1-new "" "$BASE $NEW"
run Q-L2-old "" "$BASE $SEEK"
run Q-L2-new "" "$BASE $NEW $SEEK"
run Q-L2-old2 "" "$BASE $SEEK"
run Q-L2-new2 "" "$BASE $NEW $SEEK"
echo "### $(date +%T) all done"
