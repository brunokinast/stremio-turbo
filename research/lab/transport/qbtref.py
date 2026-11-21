"""qBittorrent reference run on the real swarm (adapted from data/ab-qbt2.py).
The test qBittorrent (normal mode) downloads one file of a torrent sequentially, first/last
piece first, with the lab tracker list; logs progress every 10 s and every peer every 30 s;
deletes the torrent and its files at the end.
Usage: python qbtref.py <infohash> <file index> <seconds>"""
import json
import os
import sys
import time
import urllib.parse
import urllib.request

sys.stdout.reconfigure(encoding='utf-8', errors='replace')  # peer client names are not cp1252-safe
API = 'http://127.0.0.1:8089/api/v2'
HERE = os.path.dirname(os.path.abspath(__file__))
IH, IDX, DURATION = sys.argv[1].lower(), int(sys.argv[2]), int(sys.argv[3])
with open(os.path.join(HERE, 'trackers.txt'), encoding='utf-8') as f:
    trackers = f.read().split()


def call(path, data=None):
    body = urllib.parse.urlencode(data).encode() if data is not None else None
    return urllib.request.urlopen(urllib.request.Request(f'{API}/{path}', data=body), timeout=10).read()


call('torrents/stop', {'hashes': 'all'})  # nothing else may compete for bandwidth or slots
magnet = f'magnet:?xt=urn:btih:{IH}' + ''.join('&tr=' + urllib.parse.quote(t, safe='') for t in trackers)
call('torrents/add', {'urls': magnet, 'sequentialDownload': 'true', 'firstLastPiecePrio': 'true'})
t0 = time.time()
files_set = False
next_log, next_peers = 10, 30
try:
    while time.time() - t0 < DURATION:
        time.sleep(1)
        t = int(time.time() - t0)
        if not files_set:
            files = json.loads(call(f'torrents/files?hash={IH}') or b'[]')
            if files:
                if IDX < 0:  # -1 = the largest file
                    IDX = max(range(len(files)), key=lambda i: files[i]['size'])
                props = json.loads(call(f'torrents/properties?hash={IH}'))
                print(f"      file {IDX}: {files[IDX]['name']} ({files[IDX]['size'] / 1e9:.2f} GB), piece {props['piece_size'] >> 20} MiB", flush=True)
                others = '|'.join(str(i) for i in range(len(files)) if i != IDX)
                if others:
                    call('torrents/filePrio', {'hash': IH, 'id': others, 'priority': '0'})
                files_set = True
                print(f'{t:4d}s metadata ({len(files)} files)', flush=True)
            continue
        if t >= next_log:
            next_log += 10
            info = json.loads(call(f'torrents/info?hashes={IH}'))[0]
            ps = json.loads(call(f'torrents/pieceStates?hash={IH}'))
            contig = next((i for i, v in enumerate(ps) if v != 2), len(ps))
            print(f"{t:4d}s network {info['downloaded'] / 1e6:7.1f} MB  speed {info['dlspeed'] / 1e6:5.2f} MB/s  peers {info['num_leechs']}+{info['num_seeds']} seeds  (swarm {info['num_incomplete']}+{info['num_complete']})  contiguous {contig * 16} MiB", flush=True)
        if t >= next_peers:
            next_peers += 30
            peers = json.loads(call(f'sync/torrentPeers?hash={IH}'))['peers']
            for p in sorted(peers.values(), key=lambda p: -p['downloaded']):
                if p['downloaded'] > 0 or p['dl_speed'] > 0:
                    print(f"      {p['ip']:>40}:{p['port']:<5} {p['connection']:5} {p['client'][:26]:26} flags={p['flags']:10} prog={p['progress']:.2f} down={p['downloaded'] / 1e6:6.1f}MB {p['dl_speed'] / 1e3:7.1f}KB/s", flush=True)
finally:
    call('torrents/delete', {'hashes': IH, 'deleteFiles': 'true'})
    print('deleted torrent and files', flush=True)
