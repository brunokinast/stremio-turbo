#!/bin/bash
# usage: ./run-probe3.sh <infohash> <file index> <secs> <prefix>  — plain, fixed, tcp probe runs, 90 s apart.
cd "$(dirname "$0")"
IH=$1; IDX=$2; SECS=$3; P=$4
OUT=../../data/swarmprobe
DL=/c/Users/bruno/AppData/Local/Temp/claude/b988acceca048fd5/ceabc5bc-61e4-475e-b4e4-772595085a96/scratchpad/real
probe() { echo "### $(date +%T) $1"; ./swarmprobe.exe -ih $IH -trackers trackers.txt -file $IDX -dir "$DL" -secs $SECS $2 > $OUT/$P-$1.txt 2>&1; rm -rf "$DL"; }
probe plain ""; sleep 90
UTPLAB_BIGSACK=1 UTPLAB_OSBUF=4194304 probe fixed ""; sleep 90
UTPLAB_BIGSACK=1 UTPLAB_OSBUF=4194304 probe tcp "-no-utp"
echo "### $(date +%T) all done"
