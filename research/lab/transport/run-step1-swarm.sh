#!/bin/bash
# Step 1 Part B: real 4K HDR swarm. One run at a time, 2 minutes apart. Stremio/Rivulet must be closed.
cd "$(dirname "$0")"
IH=9247c52e17a888088fefe6bcce418dba52a353b6
OUT=../../data/swarmprobe
DL=/c/Users/bruno/AppData/Local/Temp/claude/b988acceca048fd5/ceabc5bc-61e4-475e-b4e4-772595085a96/scratchpad/real
QBT_PROFILE='C:\Users\bruno\AppData\Local\Temp\claude\b988acceca048fd5\ceabc5bc-61e4-475e-b4e4-772595085a96\scratchpad\qbt'
probe() { echo "### $(date +%T) $1"; ./swarmprobe.exe -ih $IH -trackers trackers.txt -file 0 -dir "$DL" -secs 240 $2 > $OUT/$1.txt 2>&1; rm -rf "$DL"; sleep 120; }
probe plain ""
UTPLAB_BIGSACK=1 UTPLAB_OSBUF=4194304 probe fixed ""
UTPLAB_BIGSACK=1 UTPLAB_OSBUF=4194304 probe tcp "-no-utp"
echo "### $(date +%T) qbt"
"/c/Program Files/qBittorrent/qbittorrent.exe" --profile="$QBT_PROFILE" --configuration=diag &
until curl -s -o /dev/null http://127.0.0.1:8089/api/v2/app/version; do sleep 2; done
python qbtref.py $IH 0 240 > $OUT/qbt.txt 2>&1
curl -s -X POST http://127.0.0.1:8089/api/v2/app/shutdown
echo "### $(date +%T) all done"
