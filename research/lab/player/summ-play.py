"""Summarise a play folder written by mpvplay.py.
usage: summ-play.py <play dir>
Puts the player's timeline (wall clock from loadfile) next to Rivulet's trace (ms since the torrent
was added), so each step of the start and every stall can be explained.
"""
import json, os, sys
from collections import defaultdict
sys.stdout.reconfigure(encoding='utf-8')
d = sys.argv[1]
pj = os.path.join(d, 'play.json')
# A real-app play has no player log (appplay.mjs drives the UI only).
play = json.load(open(pj, encoding='utf-8')) if os.path.exists(pj) else dict(
    label=os.path.basename(d), start=0, firstFrameS=None, stallCount=None, stallSeconds=None, events=[], pauses=[], polls=[])
tr = json.load(open(os.path.join(d, 'trace.json'), encoding='utf-8'))
locks = json.load(open(os.path.join(d, 'locks.json'), encoding='utf-8'))
print(f"== {play['label']}  start={play['start']}  first frame {play['firstFrameS']} s  stalls {play['stallCount']} / {play['stallSeconds']} s")
print('-- player')
for e in play['events']:
    print(f"  {e['t']:8.2f}s  {e['name']}  {e.get('ms', '')}")
for p in play['pauses']:
    print(f"  pause at {p['at']} s for {p['seconds']} s (pos {p['pos']})")
print('-- server milestones (s since torrent added)')
for e in tr['events']:
    print(f"  {e['atMs']/1000:8.2f}s  {e['name']}  {e.get('detail', '')}")
print('-- requests')
for r in tr['requests']:
    print(f"  #{r['id']:<3} {r['atMs']/1000:7.2f}s {r['class']:9} {r.get('range', ''):24} ua={r.get('userAgent', '')[:18]:18} "
          f"status={r['status']} first={r['firstByteMs']}ms bytes={r['bytes']/1048576:.1f}MiB dur={r['durationMs']/1000:.1f}s {r['end']}")
print('-- reads: first reads and stalls >= 250 ms')
for s in tr['stalls']:
    print(f"  {s['atMs']/1000:7.2f}s +{s['durationMs']/1000:.2f}s req#{s.get('request', 0)} off={s['offset']/1048576:.1f}MiB piece={s['piece']}{' FIRST' if s.get('first') else ''}")
# Peers: last sample per address, ranked by bytes.
last = {}
first_seen = {}
for s in tr['peerSamples']:
    last[s['address']] = s
    first_seen.setdefault(s['address'], s['atMs'])
tot = sum(s['downloaded'] for s in last.values())
print(f"-- peers ({len(last)} sampled, {tot/1048576:.0f} MiB useful from sampled peers)")
for s in sorted(last.values(), key=lambda s: -s['downloaded'])[:15]:
    u = s.get('utp')
    utp = (f" rtt={u['rttMs']}ms dup={u['duplicates']} reorderIns={u['reorderInserts']} peak={u['reorderPeak']} tooFar={u['reorderTooFar']}" if u else '')
    print(f"  {s['downloaded']/1048576:7.1f}MiB {s['network']:5} {s['source']:3} {s['client'][:22]:22} q={s['outstanding']}/{s['maxRequests']} "
          f"choked={s['peerChoking']} snub={s['snubbed']} from {first_seen[s['address']]/1000:.0f}s{utp}")
# Throughput per 10 s from the samples (sum of per-peer deltas).
by_t = defaultdict(int)
prev = {}
for s in tr['peerSamples']:
    a = s['address']
    if a in prev:
        by_t[s['atMs'] // 10000] += s['downloaded'] - prev[a]
    prev[a] = s['downloaded']
print('-- rate from sampled peers, per 10 s window (MiB/s): ' +
      ' '.join(f"{k*10}s:{v/10/1048576:.1f}" for k, v in sorted(by_t.items())))
st = tr['storage']
mc = sorted(x['durationMs'] for x in st['markCompletions'])
print(f"-- storage: {st['chunkWrites']} chunk writes, {st['chunkWriteMs']:.0f} ms total, {len(st['slowWrites'])} >= 10 ms; "
      f"MarkComplete n={len(mc)} median={mc[len(mc)//2] if mc else 0:.1f} max={mc[-1] if mc else 0:.1f} ms")
for w in sorted(st['slowWrites'], key=lambda w: -w['durationMs'])[:5]:
    print(f"   slow write {w['durationMs']:.0f} ms at {w['atMs']/1000:.1f}s piece {w['piece']} {w.get('error', '')}")
print(f"-- lock holds >= 50 ms: {len(locks)}")
agg = defaultdict(list)
for l in locks:
    agg[l['lockedAt']].append(l['heldMs'])
for site, v in sorted(agg.items(), key=lambda kv: -max(kv[1]))[:8]:
    print(f"   {len(v):4}x max {max(v):.0f} ms  {site}")
print('-- player cache every 10 s (t pos cacheDur)')
print('  ' + ' '.join(f"{p['t']:.0f}:{p['pos'] or 0:.0f}/{p['cacheDur'] or 0:.0f}" for p in play['polls'] if int(p['t'] * 2) % 20 == 0))
