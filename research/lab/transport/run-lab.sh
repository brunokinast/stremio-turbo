#!/bin/bash
# usage: [UTPLAB=utplab-fork.exe] [UTPLAB_OSBUF=4194304] [UTPLAB_BIGSACK=1] ./run-lab.sh <name> "<udprelay flags>" "<utplab flags>"
# Needs the qBittorrent test profile seeding lab.torrent on 127.0.0.1:52001 (see plans/01). One run at a time, 60 s apart.
# Downloads go to $DL (default: ./dl next to this script, emptied by utplab).
L=/c/Users/bruno/Documents/git/streaming-research/lab/transport; D=/c/Users/bruno/Documents/git/streaming-research/data/transport-lab
name=$1
errs() { netstat -s -p udp | grep -a -m1 "Erros de recebimento\|Receive Errors" | tr -dc "0-9"; }
E0=$(errs)
$L/udprelay2.exe $2 > $D/$name.relay.txt 2>&1 &
sleep 0.5
$L/${UTPLAB:-utplab2.exe} -torrent $L/lab.torrent -dir "${DL:-$L/dl}" $3 > $D/$name.txt 2>&1
taskkill //F //IM udprelay2.exe > /dev/null 2>&1
E1=$(errs)
echo "OS UDP receive errors during run: $((E1-E0))" >> $D/$name.txt
echo "== $name: $(grep -a TOTAL $D/$name.txt | cut -c1-200)"; tail -1 $D/$name.txt
grep -av reqq $D/$name.txt | sed -n 2,200p | awk '{printf "%s:%s ", $1, $3} NR%12==0 {print ""}'; echo
