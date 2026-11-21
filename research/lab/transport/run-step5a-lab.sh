#!/bin/bash
# Step 5a: an mpv-style head read (32 KiB, then sequential) plus, 1 s after the first chunk, a read of
# the last 100 KiB of piece 2 (16 MiB pieces). Old reads (verified, piece order) vs responsive +
# ReaderOffsetFirst. Off, on, off: the seeder's first run is cold.
cd "$(dirname "$0")"
export UTPLAB=utplab-fork.exe
READS="0s@0+32768,1s@50229248+102400"
./run-lab.sh R-off1 "" "-secs 30 -reads $READS"; sleep 60
./run-lab.sh R-on "" "-secs 30 -reads $READS -responsive -offsetfirst"; sleep 60
./run-lab.sh R-off2 "" "-secs 30 -reads $READS"
echo "### $(date +%T) all done"
