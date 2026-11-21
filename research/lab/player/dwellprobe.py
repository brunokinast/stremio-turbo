"""Emulated dwell without playing (step 6e, M1): preload a quality's sources as the web client does,
poll each torrent's stats every 2 s for `secs`, then save each torrent's trace and remove it (cold
cache for the next run). Shows what a source's early download predicts.

usage: python dwellprobe.py <label> race=<addon .json> [tier="4k DV | HDR"] [limit=3] [secs=30]
       [server=http://127.0.0.1:11470]
Writes data/dwell-<time>-<label>/{samples.json,trace-<ih8>.json}.
"""
import json, os, sys, time, urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import smartplay_emul

label = sys.argv[1]
cfg = dict(race='', tier='4k DV | HDR', limit='3', secs='30', server='http://127.0.0.1:11470')
for a in sys.argv[2:]:
    k, v = a.split('=', 1)
    cfg[k] = v
here = os.path.dirname(os.path.abspath(__file__))
out = os.path.join(here, '..', '..', 'data', time.strftime('dwell-%Y%m%d-%H%M%S-') + label)
os.makedirs(out)
T0 = time.perf_counter()
events = []
sp = smartplay_emul.SmartPlay(cfg['server'], T0, lambda n, **kw: events.append(dict(t=round(time.perf_counter() - T0, 3), name=n, **kw)))
srcs = smartplay_emul.load_sources(cfg['race'], cfg['tier'], int(cfg['limit']))
sp.preload(srcs)
samples = []
end = T0 + float(cfg['secs'])
while time.perf_counter() < end:
    t = round(time.perf_counter() - T0, 3)
    for s in srcs:
        st = sp.stats(s['infoHash']) or {}
        samples.append(dict(t=t, infoHash=s['infoHash'], metadata=bool(st.get('files')), peers=st.get('peers', 0),
                            unchoked=st.get('unchoked', 0), downloaded=st.get('downloaded', 0),
                            downloadSpeed=st.get('downloadSpeed', 0)))
    time.sleep(2)
for s in srcs:
    ih = s['infoHash']
    try:
        data = urllib.request.urlopen(cfg['server'] + f'/debug/trace/{ih}', timeout=30).read()
        open(os.path.join(out, f'trace-{ih[:8]}.json'), 'wb').write(data)
    except Exception as e:
        print('trace failed', ih[:8], e)
    try:
        urllib.request.urlopen(cfg['server'] + f'/{ih}/remove', timeout=30).read()
    except Exception:
        pass
json.dump(dict(sources=srcs, events=events, samples=samples), open(os.path.join(out, 'samples.json'), 'w'), indent=1)
for s in srcs:
    last = [x for x in samples if x['infoHash'] == s['infoHash']][-1]
    print(f"{s['infoHash'][:8]} size {(s['size'] or 0) / 1e9:.1f} GB: downloaded {last['downloaded'] / 1e6:.0f} MB, "
          f"peers {last['peers']}, unchoked {last['unchoked']}")
print('->', os.path.abspath(out))
