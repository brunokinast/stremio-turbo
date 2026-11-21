"""Switch the test qBittorrent (Web UI 127.0.0.1:8089, scratchpad profile) between
lab mode (loopback uTP seeder for the transport lab) and its normal settings.
Usage: python qbt-mode.py lab|normal"""
import json
import os
import sys
import urllib.parse
import urllib.request

API = 'http://127.0.0.1:8089/api/v2'
HERE = os.path.dirname(os.path.abspath(__file__))
KEYS = ['bittorrent_protocol', 'current_interface_address', 'dht', 'enable_multi_connections_from_same_ip',
        'limit_lan_peers', 'listen_port', 'lsd', 'pex', 'upnp']
LAB = {'bittorrent_protocol': 2, 'current_interface_address': '127.0.0.1', 'dht': False,
       'enable_multi_connections_from_same_ip': True, 'limit_lan_peers': False, 'listen_port': 52001,
       'lsd': False, 'pex': False, 'upnp': False}

mode = sys.argv[1] if len(sys.argv) > 1 else ''
if mode == 'lab':
    prefs = LAB
elif mode == 'normal':
    with open(os.path.join(HERE, 'qbt-prefs-normal.json'), encoding='utf-8') as f:
        saved = json.load(f)
    prefs = {k: saved[k] for k in KEYS}
else:
    sys.exit(__doc__)
body = urllib.parse.urlencode({'json': json.dumps(prefs)}).encode()
urllib.request.urlopen(urllib.request.Request(API + '/app/setPreferences', data=body), timeout=10).read()
now = json.loads(urllib.request.urlopen(API + '/app/preferences', timeout=10).read())
print(mode, {k: now[k] for k in KEYS})
