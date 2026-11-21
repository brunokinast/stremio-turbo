"""Per quality: whole-season packs vs the best source per episode (Torrentio counts)."""
import sys, json, re, urllib.request
from collections import defaultdict
sys.stdout.reconfigure(encoding='utf-8')
show, season, n = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
def gb(t):
    m = re.search(r'💾 ([\d.]+) ([GM]B)', t)
    return float(m.group(1)) / (1 if m.group(2) == 'GB' else 1024) if m else 0
eps = {}
for e in range(1, n + 1):
    url = f'https://torrentio.strem.fun/limit=10/stream/series/{show}:{season}:{e}.json'
    with urllib.request.urlopen(urllib.request.Request(url, headers={'User-Agent': 'Mozilla/5.0'}), timeout=30) as r:
        eps[e] = [s for s in json.load(r)['streams'] if 'infoHash' in s]
cover = defaultdict(set)
for e, ss in eps.items():
    for s in ss: cover[s['infoHash']].add(e)
packs = {h for h, c in cover.items() if len(c) == n}
quals = defaultdict(lambda: defaultdict(list))
for e, ss in eps.items():
    for s in ss:
        q = s['name'].split('\n', 1)[-1].strip(); t = s.get('title', '')
        seeds = int((re.search(r'👤 (\d+)', t) or [0, 0])[1])
        quals[q][e].append((seeds, gb(t), s['infoHash'], t.split('\n')[0][:60]))
print(f'== {show} S{season} ({n} eps)')
for q, by in sorted(quals.items(), key=lambda kv: -max((x[0] for v in kv[1].values() for x in v), default=0)):
    if len(by) < n: 
        print(f'  {q:16} only on {len(by)}/{n} episodes'); continue
    best = [max(by[e]) for e in range(1, n + 1)]
    best_pack_share = sum(1 for b in best if b[2] in packs)
    singles = [max([x for x in by[e] if x[2] not in packs] or [(0, 0, '', '')]) for e in range(1, n + 1)]
    pk = sorted({(x[0], x[2], x[3]) for e in by for x in by[e] if x[2] in packs}, reverse=True)
    pksz = {}
    for e in by:
        for x in by[e]:
            if x[2] in packs: pksz[x[2]] = pksz.get(x[2], 0) + x[1]
    print(f'  {q:16} packs {len({p[1] for p in pk}):2}  best pack 👤{pk[0][0] if pk else 0:4} {pksz.get(pk[0][1],0) if pk else 0:6.1f} GB {pk[0][2] if pk else "":60}')
    print(f'  {"":16} per-ep best is a pack on {best_pack_share}/{n}; single-ep torrents: {sum(1 for s in singles if s[0]>0)}/{n} eps, 👤 min {min(s[0] for s in singles)} max {max(s[0] for s in singles)}, total {sum(s[1] for s in singles):.1f} GB')
