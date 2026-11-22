"""Supply funnel: how each client in a benchmark found peers and kept them (plan 16 Part A).

usage: python funnel.py <bench dir>   (a compare.py output: r<round>-<client>/ folders)

Per run, from what each client records:
- turbo: Rivulet's /debug/trace supply section and /debug/dials (every attempt, per transport);
- lt: ltclient.py's libtorrent alerts and counters;
- qbt: qBittorrent's peer list each second (flags and transport);
- official: stats.json peers only.
Prints, per client (median over rounds where it makes sense):
- addresses offered by source, attempts started, connected, at 5/10/20/30/60 s;
- attempt outcomes (connected, timeout, refused, handshake, other) and time to connect;
- connections held each second, and why connections closed;
- bytes delivered by the peers' source and transport.
Writes funnel.json next to results.json.
"""
import collections, glob, json, os, statistics, sys

if len(sys.argv) < 2:
    sys.exit(__doc__)
BENCH = sys.argv[1]
MARKS = [5, 10, 20, 30, 60]


def load(path, default=None):
    try:
        with open(path, encoding='utf-8') as f:
            return json.load(f)
    except (OSError, ValueError):
        return default


def at(series, t):
    """The last value of [(t, v)] at or before t."""
    v = None
    for ts, val in series:
        if ts <= t:
            v = val
    return v


def pct(xs, p):
    if not xs:
        return None
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(p * len(xs)))]


def peers_from_samples(samples):
    return [(s['t'], s.get('peers') or 0) for s in samples if 'error' not in s]


def turbo(run):
    tr = load(os.path.join(run, 'trace.json'), {}) or {}
    dials = load(os.path.join(run, 'dials.json'), []) or []
    samples = (load(os.path.join(run, 'samples.json'), {}) or {}).get('samples', [])
    sup = tr.get('supply') or {}
    out = dict(client='turbo')
    ss = sup.get('samples') or []
    out['offered'] = {m: at([(s['atMs'] / 1000, sum((s.get('offeredBySource') or {}).values())) for s in ss], m) for m in MARKS}
    out['offeredBySource60'] = at([(s['atMs'] / 1000, s.get('offeredBySource')) for s in ss], 60)
    out['pending'] = {m: at([(s['atMs'] / 1000, sum((s.get('pendingBySource') or {}).values())) for s in ss], m) for m in MARKS}
    out['halfOpen'] = {m: at([(s['atMs'] / 1000, s.get('halfOpen')) for s in ss], m) for m in MARKS}
    out['dialTimeoutMs'] = {m: at([(s['atMs'] / 1000, s.get('dialTimeoutMs')) for s in ss], m) for m in MARKS}
    outgoing = [d for d in dials if d.get('outgoing')]
    out['attempts'] = {m: sum(1 for d in outgoing if d['startedMs'] / 1000 <= m) for m in MARKS}
    out['connectedAttempts'] = {m: sum(1 for d in outgoing if d['result'] == 'connected' and (d['startedMs'] + d['durationMs']) / 1000 <= m) for m in MARKS}
    out['incoming'] = sum(1 for d in dials if not d.get('outgoing'))
    out['outcomes'] = dict(collections.Counter(d['result'] for d in outgoing))
    ok = [d['durationMs'] for d in outgoing if d['result'] == 'connected']
    out['connectMs'] = dict(p50=pct(ok, .5), p90=pct(ok, .9), max=max(ok) if ok else None)
    legs = collections.Counter()
    for d in outgoing:
        for l in d.get('legs') or []:
            legs[(l['network'], l['result'])] += 1
    out['legs'] = {f'{n}:{r}': c for (n, r), c in sorted(legs.items())}
    out['bySource'] = dict(collections.Counter(d['source'] for d in outgoing))
    out['connectedBySource'] = dict(collections.Counter(d['source'] for d in outgoing if d['result'] == 'connected'))
    out['held'] = {m: at(peers_from_samples(samples), m) for m in MARKS}
    closes = sup.get('closes') or []
    out['closeReasons'] = dict(collections.Counter(c['reason'] for c in closes if c.get('lifetimeMs')).most_common(8))
    out['announces'] = [dict(atS=round(a['atMs'] / 1000, 1), url=a['url'].split('/')[2] if '//' in a['url'] else a['url'],
                             peers=a['peers'], added=a['added'], err=bool(a.get('error'))) for a in (sup.get('announces') or [])][:40]
    dht = sup.get('dht') or []
    out['dht'] = dict(firstBatchS=round(dht[0]['atMs'] / 1000, 1) if dht else None, peers=sum(b['peers'] for b in dht),
                      added=sum(b['added'] for b in dht), batches=len(dht))
    # Bytes by source and transport, from the last peer sample of each address.
    last = {}
    for p in tr.get('peerSamples') or []:
        last[p['address']] = p
    by = collections.Counter()
    for p in last.values():
        by[(p.get('source') or '?', 'utp' if 'udp' in (p.get('network') or '') else 'tcp')] += p.get('downloaded') or 0
    for c in closes:
        if c.get('address') not in last:
            by[(c.get('source') or '?', 'utp' if 'udp' in (c.get('network') or '') else 'tcp')] += c.get('downloaded') or 0
    out['bytesBy'] = {f'{s}/{n}': round(b / 1e6, 1) for (s, n), b in by.most_common()}
    return out


def lt(run):
    d = load(os.path.join(run, 'samples.json'), {}) or {}
    samples = d.get('samples', [])
    events = load(os.path.join(run, 'events.json'), []) or []
    out = dict(client='lt')
    cnt = [(s['t'], s.get('counters') or {}) for s in samples]
    out['attempts'] = {m: (at(cnt, m) or {}).get('peer.connection_attempts') for m in MARKS}
    out['halfOpen'] = {m: (at(cnt, m) or {}).get('peer.num_peers_half_open') for m in MARKS}
    out['connectTimeouts'] = (cnt[-1][1] if cnt else {}).get('peer.connect_timeouts')
    out['known'] = {m: at([(s['t'], s.get('listPeers')) for s in samples], m) for m in MARKS}
    out['candidates'] = {m: at([(s['t'], s.get('connectCandidates')) for s in samples], m) for m in MARKS}
    out['held'] = {m: at(peers_from_samples(samples), m) for m in MARKS}
    conns = [e for e in events if e['kind'] == 'connect']
    out['connects'] = {m: sum(1 for e in conns if e['t'] <= m) for m in MARKS}
    disc = [e for e in events if e['kind'] == 'disconnect']
    out['disconnectOps'] = dict(collections.Counter(f"{e.get('op')}: {e.get('error')}" for e in disc).most_common(10))
    out['tracker'] = [dict(atS=e['t'], url=e['url'].split('/')[2] if '//' in e['url'] else e['url'], peers=e['peers'])
                      for e in events if e['kind'] == 'tracker-reply'][:40]
    dht = [e for e in events if e['kind'] == 'dht-reply']
    out['dht'] = dict(firstReplyS=dht[0]['t'] if dht else None, peers=sum(e['peers'] for e in dht), replies=len(dht))
    last = {}
    for s in samples:
        for w in s.get('wires') or []:
            last[w['address']] = w
    by = collections.Counter()
    for w in last.values():
        src = '+'.join(w.get('source') or []) or '?'
        by[(src, 'utp' if w.get('utp') else 'tcp')] += w.get('downloaded') or 0
    out['bytesBy'] = {f'{s}/{n}': round(b / 1e6, 1) for (s, n), b in by.most_common(10)}
    out['peersSeen'] = len(last)
    out['incomingSeen'] = sum(1 for w in last.values() if w.get('incoming'))
    return out


def qbt(run):
    samples = (load(os.path.join(run, 'samples.json'), {}) or {}).get('samples', [])
    out = dict(client='qbt')
    out['held'] = {m: at(peers_from_samples(samples), m) for m in MARKS}
    last = {}
    for s in samples:
        for w in s.get('wires') or []:
            last[w['address']] = w
    by = collections.Counter()
    for w in last.values():
        flags = (w.get('flags') or '').split()
        if not (w.get('client') or w.get('downloaded')):
            continue  # an attempt still in progress, not a peer
        src = 'dht' if 'H' in flags else 'pex' if 'X' in flags else 'incoming' if 'I' in flags else 'lsd' if 'L' in flags else 'tracker'
        by[(src, 'utp' if 'P' in flags else 'tcp')] += w.get('downloaded') or 0
    out['bytesBy'] = {f'{s}/{n}': round(b / 1e6, 1) for (s, n), b in by.most_common()}
    out['peersSeen'] = len(last)
    return out


def official(run):
    samples = (load(os.path.join(run, 'samples.json'), {}) or {}).get('samples', [])
    return dict(client='official', held={m: at(peers_from_samples(samples), m) for m in MARKS})


def main():
    runs = sorted(glob.glob(os.path.join(BENCH, 'r*-*')))
    per = collections.defaultdict(list)
    for run in runs:
        name = os.path.basename(run).split('-', 1)[1]
        fn = dict(turbo=turbo, lt=lt, qbt=qbt, official=official).get(name.split('-')[0])
        if fn:
            r = fn(run)
            r['run'] = os.path.basename(run)
            per[name].append(r)
    with open(os.path.join(BENCH, 'funnel.json'), 'w', encoding='utf-8') as f:
        json.dump(per, f, indent=1)

    def med(rs, key):
        row = []
        for m in MARKS:
            vals = [r[key][m] for r in rs if r.get(key) and r[key].get(m) is not None]
            row.append(statistics.median(vals) if vals else None)
        return row

    print('median over rounds at', MARKS, 's')
    for name in sorted(per):
        rs = per.get(name)
        if not rs:
            continue
        print(f'\n== {name} ({len(rs)} runs)')
        for key in ('offered', 'known', 'pending', 'candidates', 'attempts', 'connectedAttempts', 'connects', 'halfOpen', 'dialTimeoutMs', 'held'):
            if any(key in r for r in rs):
                print(f'  {key:18} {med(rs, key)}')
        for r in rs:
            extra = {k: r[k] for k in ('outcomes', 'connectMs', 'legs', 'bySource', 'connectedBySource', 'incoming', 'dht', 'closeReasons',
                                       'disconnectOps', 'connectTimeouts', 'peersSeen', 'incomingSeen', 'bytesBy', 'offeredBySource60') if k in r}
            print(f"  {r['run']}: {json.dumps(extra)}")


if __name__ == '__main__':
    main()
