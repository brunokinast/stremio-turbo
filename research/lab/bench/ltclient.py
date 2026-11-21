"""libtorrent reference client for the benchmark (plan 16 Part A): the engine under qBittorrent, run
with qBittorrent's defaults, logging what it does to find and keep peers.

usage (Python 3.13; libtorrent 2.1.1 from research/lab/bench/.pylib):
  py -3.13 ltclient.py <infoHash> <fileIdx> <out dir> [key=value ...]
    secs=60 (from the add)  settle=10 (seconds the session runs before the add, as an app just opened)
    port=49881  state=<file> (session state, kept between runs so the DHT stays warm, as qBittorrent's)

One run: start the session, wait `settle`, add the magnet (the bench's tracker list), then, once the
metadata is in, download only that file, sequentially, with deadlines on its first two pieces and its
last piece (what a player needs first: the headers and the Cues index at the end). After `secs` the
torrent is removed with its files and the state saved.

Writes to <out dir>:
- samples.json: {row, samples}, one sample a second in the bench's schema (downloaded, rate, peers,
  metadata, wires: every peer with its source and transport) plus libtorrent's own counters;
- events.json: every connection attempt, connect, disconnect (with the reason), DHT reply, tracker
  reply and piece milestone, from libtorrent's alerts, with t in seconds from the add.
The first-frame stand-in (firstFrameS) is when the first and the last piece are both on disk.
"""
import json, os, sys, time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, '.pylib'))
import libtorrent as lt  # noqa: E402

if len(sys.argv) < 4:
    sys.exit(__doc__)
IH, IDX, OUT = sys.argv[1].lower(), int(sys.argv[2]), sys.argv[3]
cfg = dict(secs='60', settle='10', port='49881', state=os.path.join(HERE, '..', '..', 'data', 'bench-lt-state.bin'))
for a in sys.argv[4:]:
    k, v = a.split('=', 1)
    cfg[k] = v
SECS, SETTLE, PORT = float(cfg['secs']), float(cfg['settle']), int(cfg['port'])
DRY = cfg.get('dry') == '1'  # loopback only, no LSD/UPnP: checks the script without a firewall rule
os.makedirs(OUT, exist_ok=True)
SAVE = os.path.join(OUT, 'files')
with open(os.path.join(HERE, '..', 'transport', 'trackers.txt'), encoding='utf-8') as f:
    TRACKERS = [l.strip() for l in f if l.strip()]

# qBittorrent's defaults where they differ from libtorrent's (qBittorrent 5: 500 connections in all,
# 100 per torrent, uTP and TCP mixed, DHT/PEX/LSD on, UPnP/NAT-PMP on). Everything else is
# libtorrent's default: connection_speed 30/s, peer_connect_timeout 15 s, no half-open cap.
pack = {
    'listen_interfaces': f'0.0.0.0:{PORT},[::]:{PORT}',
    'connections_limit': 500,
    'enable_dht': True, 'enable_lsd': True, 'enable_upnp': True, 'enable_natpmp': True,
    'alert_mask': (lt.alert_category.status | lt.alert_category.error | lt.alert_category.peer |
                   lt.alert_category.connect | lt.alert_category.dht | lt.alert_category.tracker |
                   lt.alert_category.storage),
    'alert_queue_size': 100000,
}
if DRY:
    pack.update(listen_interfaces=f'127.0.0.1:{PORT}', enable_lsd=False, enable_upnp=False, enable_natpmp=False)
    cfg['state'] = os.path.join(OUT, 'dry-state.bin')
params = lt.session_params()
if os.path.exists(cfg['state']):
    with open(cfg['state'], 'rb') as f:
        params = lt.read_session_params(f.read())
params.settings = pack
ses = lt.session(params)
metric_index = {m.name: m.value_index for m in lt.session_stats_metrics()}
METRICS = ['peer.num_peers_half_open', 'peer.num_peers_connected', 'peer.connection_attempts',
           'peer.connect_timeouts', 'peer.timeout_peers', 'peer.uninteresting_peers', 'peer.incoming_connections',
           'peer.num_tcp_peers', 'peer.num_utp_peers', 'peer.boost_connection_attempts', 'peer.missed_connection_attempts',
           'peer.no_peer_connection_attempts', 'dht.dht_nodes']
METRICS = [m for m in METRICS if m in metric_index]

events = []
T0 = None


def now():
    return round(time.perf_counter() - T0, 3) if T0 is not None else round(-(SETTLE - (time.perf_counter() - S0)), 3)


def ev(kind, **kw):
    events.append(dict(t=now(), kind=kind, **kw))


def endpoint(a):
    try:
        return f'{a.endpoint[0]}:{a.endpoint[1]}'
    except Exception:
        return str(getattr(a, 'ip', ''))


last_counters = {}


def drain(handle):
    global last_counters
    for a in ses.pop_alerts():
        if isinstance(a, lt.session_stats_alert):
            vals = a.values
            last_counters = {m: (vals[m] if isinstance(vals, dict) else vals[metric_index[m]]) for m in METRICS}
        elif isinstance(a, lt.peer_connect_alert):
            ev('connect', peer=endpoint(a), socket=str(a.socket_type), direction=str(getattr(a, 'direction', '')))
        elif isinstance(a, lt.peer_disconnected_alert):
            ev('disconnect', peer=endpoint(a), socket=str(a.socket_type), op=str(a.op), error=a.error.message(),
               reason=str(a.reason))
        elif isinstance(a, lt.peer_error_alert):
            ev('peer-error', peer=endpoint(a), op=str(a.op), error=a.error.message())
        elif isinstance(a, lt.incoming_connection_alert):
            ev('incoming', peer=endpoint(a), socket=str(a.socket_type))
        elif isinstance(a, lt.dht_get_peers_reply_alert):
            ev('dht-reply', peers=a.num_peers())
        elif isinstance(a, lt.dht_reply_alert):
            ev('dht-done', peers=a.num_peers)
        elif isinstance(a, lt.tracker_reply_alert):
            ev('tracker-reply', url=a.tracker_url(), peers=a.num_peers)
        elif isinstance(a, lt.tracker_error_alert):
            ev('tracker-error', url=a.tracker_url(), error=a.error.message())
        elif isinstance(a, lt.tracker_announce_alert):
            ev('tracker-announce', url=a.tracker_url())
        elif isinstance(a, lt.metadata_received_alert):
            ev('metadata')
        elif isinstance(a, lt.piece_finished_alert):
            if a.piece_index in WATCH:
                ev('piece', piece=a.piece_index)
        elif isinstance(a, lt.listen_failed_alert):
            ev('listen-failed', error=a.error.message(), address=str(a.address), port=a.port)
        elif isinstance(a, lt.listen_succeeded_alert):
            ev('listen', address=str(a.address), port=a.port, socket=str(a.socket_type))


WATCH = set()
S0 = time.perf_counter()
while time.perf_counter() - S0 < SETTLE:  # the app has just opened
    drain(None)
    time.sleep(0.2)

magnet = f'magnet:?xt=urn:btih:{IH}' + ''.join('&tr=' + t for t in TRACKERS)
atp = lt.parse_magnet_uri(magnet)
atp.save_path = SAVE
T0 = time.perf_counter()
h = ses.add_torrent(atp)
h.set_max_connections(100)  # qBittorrent's per-torrent default
ev('added')

samples, ready, configured, first, last = [], None, False, None, None
while time.perf_counter() - T0 < SECS:
    tick = time.perf_counter()
    ses.post_session_stats()
    st = h.status()
    if st.has_metadata and not configured:
        ti = h.torrent_file()
        fs = ti.files()
        prios = [0] * fs.num_files()
        prios[IDX] = 4
        h.prioritize_files(prios)
        h.set_flags(lt.torrent_flags.sequential_download)
        first = ti.map_file(IDX, 0, 1).piece
        last = ti.map_file(IDX, max(0, fs.file_size(IDX) - 1), 1).piece
        WATCH.update({first, first + 1, last})
        for i, p in enumerate((first, first + 1, last)):
            h.set_piece_deadline(p, i * 100)
        configured = True
        ev('configured', firstPiece=first, lastPiece=last, pieceLength=ti.piece_length())
    drain(h)
    wires = []
    # Bits the Python bindings don't name (libtorrent 2.x peer_info.hpp): peer_flags utp_socket is
    # bit 17, peer_source_flags incoming is bit 5.
    UTP, SRC_INCOMING = 1 << 17, 1 << 5
    for p in h.get_peer_info():
        src = p.source
        wires.append(dict(address=f'{p.ip[0]}:{p.ip[1]}', down=p.down_speed, downloaded=p.total_download,
                          utp=bool(int(p.flags) & UTP), incoming=not bool(p.flags & lt.peer_info.outgoing_connection),
                          seed=bool(p.flags & lt.peer_info.seed), client=p.client.decode('utf-8', 'replace') if isinstance(p.client, bytes) else p.client,
                          source=[n for n, bit in (('tracker', lt.peer_info.tracker), ('dht', lt.peer_info.dht), ('pex', lt.peer_info.pex),
                                                   ('lsd', lt.peer_info.lsd), ('incoming', SRC_INCOMING)) if int(src) & int(bit)],
                          connecting=bool(p.flags & lt.peer_info.connecting), handshake=bool(p.flags & lt.peer_info.handshake)))
    s = dict(t=round(tick - T0, 2), downloaded=st.total_payload_download, rate=st.download_payload_rate,
             peers=sum(1 for w in wires if not w['connecting'] and not w['handshake']), metadata=bool(st.has_metadata),
             listPeers=st.list_peers, listSeeds=st.list_seeds, numSeeds=st.num_seeds, connectCandidates=st.connect_candidates,
             counters=dict(last_counters), wires=wires)
    if configured:
        s['firstDone'], s['lastDone'] = h.have_piece(first), h.have_piece(last)
        if ready is None and s['firstDone'] and s['lastDone']:
            ready = s['t']
    samples.append(s)
    time.sleep(max(0.0, 1.0 - (time.perf_counter() - tick)))

ses.remove_torrent(h, lt.session.delete_files)
for _ in range(50):
    drain(h)
    if not os.path.exists(SAVE) or not any(files for _, _, files in os.walk(SAVE)):
        break
    time.sleep(0.2)
with open(cfg['state'], 'wb') as f:
    f.write(lt.write_session_params_buf(ses.session_state()))
cleaned = not os.path.exists(SAVE) or not any(files for _, _, files in os.walk(SAVE))
row = dict(firstFrameS=ready, firstFrameIsProxy=True, cleaned=cleaned)
with open(os.path.join(OUT, 'samples.json'), 'w', encoding='utf-8') as f:
    json.dump(dict(row=row, samples=samples), f, indent=1)
with open(os.path.join(OUT, 'events.json'), 'w', encoding='utf-8') as f:
    json.dump(events, f, indent=1)
print(json.dumps(row))
