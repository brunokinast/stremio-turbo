"""Print the test qBittorrent's view of its peers on every torrent (flags, choke state, rates).
Usage: python qbt-peers.py"""
import json
import urllib.request

API = 'http://127.0.0.1:8089/api/v2'
for t in json.loads(urllib.request.urlopen(API + '/torrents/info', timeout=10).read()):
    peers = json.loads(urllib.request.urlopen(API + '/sync/torrentPeers?rid=0&hash=' + t['hash'], timeout=10).read())
    for addr, p in (peers.get('peers') or {}).items():
        print(json.dumps({'torrent': t['name'], 'state': t['state'], 'peer': addr,
                          **{k: p.get(k) for k in ('client', 'connection', 'flags', 'flags_desc', 'progress', 'up_speed', 'dl_speed', 'uploaded')}}))
