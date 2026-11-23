"""Benchmark: the official Stremio streaming server vs Stremio Turbo's (Rivulet) vs qBittorrent,
on one torrent file, cold, in alternating rounds. Also records why a client is slow.

usage: python compare.py <infoHash> <fileIdx> [key=value ...]
  rounds=3  secs=60 (per run, from the click)  settle=10 (seconds a client runs before the click,
  as when the app has just opened)  rows=official,turbo,qbt  out=<dir> (default
  research/data/bench-<time>; it holds peer addresses and is never published)
  turboenv=NAME=value;NAME2=value (extra environment for Turbo's Rivulet, to bench a setting)
  variants=turbo-a:NAME=v|turbo-b:NAME=w (Turbo rows with their own environment, interleaved in each round;
  @EXE=<path> in a variant copies that rivulet-engine build into Turbo's install first, to compare builds
  from the one firewall-allowed path)

Each run starts the client fresh, waits `settle`, then:
- official / turbo: mpvplay.py plays the file with the shell's libmpv (vo=null, audio off) for
  `secs`, as stremio-core does (POST /create with trackers + DHT, then stream). "First frame" is
  mpv's first playback-restart after the click. Meanwhile stats.json is sampled every second.
- qbt: the file is added as a magnet (sequential, first and last pieces first). It has no player,
  so its "first frame" stands in as the moment the first and the last piece are on disk (what a
  player reading the file needs before its first frame: the headers and the Cues index at the end).
Then the client stops and the torrent's data is deleted, so the next run is cold.
The row order rotates each round (A B C, B C A, C A B) so no client always goes first.

Diagnostics, per run (samples.json): every second, bytes downloaded, rate, connected peers, and
each peer's address, rate and kind; plus the time to metadata and to the first byte. Turbo runs
also keep Rivulet's own trace (/debug/trace, dials, locks) through mpvplay.py.

Only executables that already have firewall rules run here: the official stremio-runtime.exe,
Turbo's installed rivulet-engine.exe and qBittorrent. The Turbo app must be closed first.
"""
import json, os, shutil, statistics, subprocess, sys, threading, time, urllib.parse, urllib.request

sys.stdout.reconfigure(encoding='utf-8')
if len(sys.argv) < 3:
    sys.exit(__doc__)
IH, IDX = sys.argv[1].lower(), int(sys.argv[2])
cfg = dict(rounds='3', secs='60', settle='10', rows='official,turbo,qbt,lt', out='', turboenv='', variants='')
for a in sys.argv[3:]:
    k, v = a.split('=', 1)
    cfg[k] = v
ROUNDS, SECS, SETTLE = int(cfg['rounds']), float(cfg['secs']), float(cfg['settle'])
ROWS = cfg['rows'].split(',')
# Extra environment for Turbo's Rivulet, to bench a setting: turboenv=NAME=value;NAME2=value
TURBO_ENV = dict(kv.split('=', 1) for kv in cfg['turboenv'].split(';') if kv)
# Turbo variants benched side by side in the same rounds, each a row of its own:
# variants=turbo-50:|turbo-200:RIVULET_PEERS_PER_TORRENT=200 (label:env as in turboenv, '|' between)
VARIANTS = [(v.split(':', 1)[0], dict(kv.split('=', 1) for kv in v.split(':', 1)[1].split(';') if kv))
            for v in cfg['variants'].split('|') if v]

HERE = os.path.dirname(os.path.abspath(__file__))
LAB = os.path.dirname(HERE)
OUT = cfg['out'] or os.path.join(LAB, '..', 'data', time.strftime('bench-%Y%m%d-%H%M%S'))
os.makedirs(OUT, exist_ok=True)
LOCAL = os.environ['LOCALAPPDATA']
OFFICIAL_DIR = os.path.join(LOCAL, 'Programs', 'Stremio')
OFFICIAL_CACHE = os.path.join(LOCAL, 'Stremio', 'stremio-cache')
TURBO_DIR = os.path.join(LOCAL, 'Programs', 'Stremio Turbo')
TURBO_DATA = os.path.join(LOCAL, 'rivulet-engine')
QBT = r'C:\Program Files\qBittorrent\qbittorrent.exe'
QBT_PROFILE = os.path.join(OUT, '..', 'bench-qbt-profile')  # kept between benches: its DHT stays warm
QBT_UI = 'http://127.0.0.1:8089'
BT_PORT = 49881  # Turbo's listening port; qBittorrent gets the same one (only one client runs at a time)
SERVER = 'http://127.0.0.1:11470'
with open(os.path.join(LAB, 'bench', 'trackers.txt'), encoding='utf-8') as f:
    TRACKERS = [l.strip() for l in f if l.strip()]


def http(url, body=None, timeout=10, form=False):
    data = None
    headers = {}
    if body is not None:
        data = urllib.parse.urlencode(body).encode() if form else json.dumps(body).encode()
        headers['Content-Type'] = 'application/x-www-form-urlencoded' if form else 'application/json'
    req = urllib.request.Request(url, data=data, headers=headers)
    with urllib.request.urlopen(req, timeout=timeout) as r:
        raw = r.read()
    try:
        return json.loads(raw)
    except ValueError:
        return raw


def wait_up(url, secs=30):
    end = time.time() + secs
    while time.time() < end:
        try:
            http(url, timeout=2)
            return True
        except Exception:
            time.sleep(0.3)
    return False


def port_free(secs=30):
    end = time.time() + secs
    while time.time() < end:
        try:
            http(SERVER + '/heartbeat', timeout=1)
            time.sleep(0.5)
        except Exception:
            return True
    return False


# ---- clients -------------------------------------------------------------------------------------

class Official:
    name = 'official'

    def start(self, run_dir):
        self.log = open(os.path.join(run_dir, 'server.log'), 'wb')
        self.p = subprocess.Popen([os.path.join(OFFICIAL_DIR, 'stremio-runtime.exe'), os.path.join(OFFICIAL_DIR, 'server.js')],
                                  cwd=OFFICIAL_DIR, stdout=self.log, stderr=subprocess.STDOUT, creationflags=subprocess.CREATE_NO_WINDOW)
        if not wait_up(SERVER + '/heartbeat'):
            raise RuntimeError('official server did not start')

    def stop(self):
        self.p.kill()
        self.p.wait()
        self.log.close()
        port_free()

    def clean(self):
        shutil.rmtree(os.path.join(OFFICIAL_CACHE, IH), ignore_errors=True)
        return not os.path.exists(os.path.join(OFFICIAL_CACHE, IH))


class Turbo:
    name = 'turbo'

    def __init__(self, name='turbo', env=None):
        self.name, self.env = name, env or {}

    def start(self, run_dir):
        env_extra = dict(self.env)
        exe = env_extra.pop('@EXE', None)
        if exe:
            shutil.copyfile(exe, os.path.join(TURBO_DIR, 'rivulet-engine.exe'))
        # As the shell starts it (shell/src/stremio_app/stremio_server/server.rs), with an owner
        # process of our own: when it ends, Rivulet exits on its own, cleanly.
        self.owner = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(86400)'], creationflags=subprocess.CREATE_NO_WINDOW)
        env = dict(os.environ, BIND_ADDRESS='127.0.0.1', HTTP_PORT='11470', HTTPS_PORT='0', BT_LISTEN_PORT=str(BT_PORT),
                   APP_PATH=TURBO_DATA, RIVULET_OWNER_PID=str(self.owner.pid),
                   RIVULET_LOG_FILE=os.path.join(run_dir, 'rivulet.log'), RIVULET_SPEED_LIMITS='0',
                   PATH=TURBO_DIR + os.pathsep + os.environ.get('PATH', ''), **{**TURBO_ENV, **env_extra})
        self.p = subprocess.Popen([os.path.join(TURBO_DIR, 'rivulet-engine.exe')], cwd=TURBO_DIR, env=env,
                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, creationflags=subprocess.CREATE_NO_WINDOW)
        if not wait_up(SERVER + '/heartbeat'):
            raise RuntimeError('Rivulet did not start')

    def clean(self):
        # Deletes this torrent's data and its download record (DELETE /downloads/{ih}), while it runs.
        try:
            req = urllib.request.Request(f'{SERVER}/downloads/{IH}', method='DELETE')
            urllib.request.urlopen(req, timeout=30).read()
        except urllib.error.HTTPError as e:
            if e.code != 404:
                raise
        recs = [d for d in http(SERVER + '/downloads') if d.get('infoHash') == IH]
        return not recs

    def stop(self):
        self.owner.kill()
        try:
            self.p.wait(30)
        except subprocess.TimeoutExpired:
            self.p.kill()
            self.p.wait()
        port_free()


class Qbt:
    name = 'qbt'

    def __init__(self):
        conf = os.path.join(QBT_PROFILE, 'qBittorrent_bench', 'config')
        os.makedirs(conf, exist_ok=True)
        ini = os.path.join(conf, 'qBittorrent.ini')
        if not os.path.exists(ini):
            save = os.path.join(QBT_PROFILE, 'downloads').replace('\\', '/')
            with open(ini, 'w', encoding='utf-8') as f:
                f.write('[LegalNotice]\nAccepted=true\n\n'
                        '[BitTorrent]\n'
                        f'Session\\Port={BT_PORT}\nSession\\DefaultSavePath={save}\nSession\\TempPathEnabled=false\n'
                        'Session\\QueueingSystemEnabled=false\nSession\\AddTorrentStopped=false\n\n'
                        '[Preferences]\nWebUI\\Enabled=true\nWebUI\\Port=8089\nWebUI\\Address=127.0.0.1\n'
                        'WebUI\\LocalHostAuth=false\nWebUI\\CSRFProtection=false\nGeneral\\ExitConfirm=false\n'
                        'General\\Locale=en\nDownloads\\SavePath=' + save + '\n')

    def start(self, run_dir):
        self.ensure_credentials()
        self.p = subprocess.Popen([QBT, f'--profile={QBT_PROFILE}', '--configuration=bench', '--no-splash'])
        if not wait_up(QBT_UI + '/api/v2/app/version', 60):
            raise RuntimeError('qBittorrent Web UI did not come up')

    def ensure_credentials(self):
        # qBittorrent 5 keeps its Web UI off until a login is set. A random one nobody needs:
        # localhost skips the login (LocalHostAuth=false).
        import base64, hashlib, secrets
        ini = os.path.join(QBT_PROFILE, 'qBittorrent_bench', 'config', 'qBittorrent.ini')
        text = open(ini, encoding='utf-8').read()
        if 'WebUI\\Password_PBKDF2' in text:
            return
        salt = secrets.token_bytes(16)
        key = hashlib.pbkdf2_hmac('sha512', secrets.token_urlsafe(24).encode(), salt, 100000, 64)
        line = ('WebUI\\Username=bench\n'
                f'WebUI\\Password_PBKDF2="@ByteArray({base64.b64encode(salt).decode()}:{base64.b64encode(key).decode()})"\n')
        text = text.replace('[Preferences]\n', '[Preferences]\n' + line, 1)
        open(ini, 'w', encoding='utf-8').write(text)

    def clean(self):
        try:
            http(QBT_UI + '/api/v2/torrents/delete', {'hashes': IH, 'deleteFiles': 'true'}, form=True)
        except Exception:
            pass
        time.sleep(2)
        info = http(QBT_UI + f'/api/v2/torrents/info?hashes={IH}')
        return not info

    def stop(self):
        try:
            http(QBT_UI + '/api/v2/app/shutdown', {}, form=True)
        except Exception:
            pass
        try:
            self.p.wait(30)
        except subprocess.TimeoutExpired:
            self.p.kill()
            self.p.wait()


class Lt:
    """libtorrent with qBittorrent's defaults, through ltclient.py (Python 3.13): its own process per
    run, which settles, adds, samples, removes the torrent with its files and saves its DHT state."""
    name = 'lt'
    PY = os.path.join(LOCAL, 'Programs', 'Python', 'Python313', 'python.exe')

    def start(self, run_dir):
        pass

    def stop(self):
        pass

    def clean(self):
        return True  # ltclient.py deletes its files; its row says whether that worked


# ---- runs ----------------------------------------------------------------------------------------

def sample_server(stop, samples):
    """stats.json every second: the official server and Rivulet both answer it (Rivulet keeps the format)."""
    while not stop.is_set():
        t = time.time()
        try:
            s = http(f'{SERVER}/{IH}/stats.json', timeout=2)  # torrent level: it carries the peer list
            if isinstance(s, dict):
                samples.append(dict(wall=t, downloaded=s.get('downloaded'), rate=s.get('downloadSpeed'),
                                    peers=s.get('peers'), unchoked=s.get('unchoked'), swarmConnections=s.get('swarmConnections'),
                                    swarmSize=s.get('swarmSize'), metadata=bool(s.get('files')),  # Rivulet names the torrent before it has the file list
                                    wires=[dict(address=w.get('address'), down=w.get('downSpeed'), seeder=w.get('isSeeder'))
                                           for w in (s.get('wires') or [])]))
        except Exception as e:
            samples.append(dict(wall=t, error=str(e)[:200]))
        stop.wait(max(0.0, 1.0 - (time.time() - t)))


def cpu_seconds(proc):
    """User plus kernel CPU time of a running process (Windows), or None."""
    import ctypes
    from ctypes import wintypes
    k32 = ctypes.windll.kernel32
    h = k32.OpenProcess(0x1000, False, proc.pid)  # PROCESS_QUERY_LIMITED_INFORMATION
    if not h:
        return None
    try:
        t = [wintypes.FILETIME() for _ in range(4)]
        if not k32.GetProcessTimes(h, *[ctypes.byref(x) for x in t]):
            return None
        ft = lambda f: (f.dwHighDateTime << 32 | f.dwLowDateTime) / 1e7
        return ft(t[2]) + ft(t[3])
    finally:
        k32.CloseHandle(h)


def run_player(client, run_dir):
    cpu0 = cpu_seconds(client.p) if isinstance(client, Turbo) else None
    samples, stop = [], threading.Event()
    th = threading.Thread(target=sample_server, args=(stop, samples), daemon=True)
    th.start()
    r = subprocess.run([sys.executable, os.path.join(LAB, 'player', 'mpvplay.py'), IH, str(IDX), client.name,
                        f'server={SERVER}', f'secs={SECS}', 'remove=0', f'out={run_dir}'],
                       capture_output=True, text=True, encoding='utf-8', errors='replace')
    stop.set()
    th.join()
    with open(os.path.join(run_dir, 'mpvplay.txt'), 'w', encoding='utf-8') as f:
        f.write(r.stdout + '\n' + r.stderr)
    play = json.load(open(os.path.join(run_dir, 'play.json'), encoding='utf-8'))
    t0 = play['wallStart']
    for s in samples:
        s['t'] = round(s.pop('wall') - t0, 2)
    extra = {}
    head = {}
    if cpu0 is not None:
        cpu1 = cpu_seconds(client.p)
        if cpu1 is not None:
            head['cpuS'] = round(cpu1 - cpu0, 1)  # Rivulet's CPU time during the run
    if isinstance(client, Turbo):
        try:
            extra['networkInfo'] = http(SERVER + '/network-info')
        except Exception:
            pass
    return dict(firstFrameS=play.get('firstFrameS'), stalls=play.get('stallCount'), stallSeconds=play.get('stallSeconds'), **head), samples, extra


def run_qbt(run_dir):
    magnet = f'magnet:?xt=urn:btih:{IH}' + ''.join('&tr=' + urllib.parse.quote(t, safe='') for t in TRACKERS)
    t0 = time.time()
    http(QBT_UI + '/api/v2/torrents/add', {'urls': magnet, 'sequentialDownload': 'true', 'firstLastPiecePrio': 'true'}, form=True)
    samples, ready, prio_set = [], None, False
    while time.time() - t0 < SECS:
        t = time.time()
        try:
            info = (http(QBT_UI + f'/api/v2/torrents/info?hashes={IH}') or [{}])[0] or {}
            peers = http(QBT_UI + f'/api/v2/sync/torrentPeers?hash={IH}&rid=0').get('peers', {})
            s = dict(t=round(t - t0, 2), downloaded=info.get('downloaded'), rate=info.get('dlspeed'),
                     # The peer list includes connections still being attempted (no client name, no
                     # bytes), which made qBittorrent look like it held 100 peers (findings/42).
                     peers=sum(1 for p in peers.values() if p.get('client') or p.get('downloaded')),
                     listed=len(peers), seeds=info.get('num_seeds'), leechs=info.get('num_leechs'),
                     metadata=info.get('size', 0) > 0 and info.get('state') != 'metaDL',
                     wires=[dict(address=a, down=p.get('dl_speed'), downloaded=p.get('downloaded'),
                                 kind=p.get('connection'), client=p.get('client'), flags=p.get('flags'))
                            for a, p in peers.items()])
            if s['metadata']:
                files = http(QBT_UI + f'/api/v2/torrents/files?hash={IH}')
                if not prio_set and len(files) > 1:  # only the benchmarked file
                    others = [str(i) for i in range(len(files)) if i != IDX]
                    http(QBT_UI + '/api/v2/torrents/filePrio', {'hash': IH, 'id': '|'.join(others), 'priority': '0'}, form=True)
                prio_set = True
                states = http(QBT_UI + f'/api/v2/torrents/pieceStates?hash={IH}')
                if len(files) > IDX and isinstance(states, list) and states:
                    first, last = files[IDX]['piece_range']
                    s['firstPiece'], s['lastPiece'] = states[first], states[last]
                    s['piecesDone'] = sum(1 for x in states if x == 2)
                    s['firstDone'] = [i for i, x in enumerate(states) if x == 2][:8]
                    if ready is None and states[first] == 2 and states[last] == 2:
                        ready = s['t']
            samples.append(s)
        except Exception as e:
            samples.append(dict(t=round(t - t0, 2), error=str(e)[:200]))
        time.sleep(max(0.0, 1.0 - (time.time() - t)))
    return dict(firstFrameS=ready, firstFrameIsProxy=True), samples, {}


def run_lt(run_dir):
    r = subprocess.run([Lt.PY, os.path.join(HERE, 'ltclient.py'), IH, str(IDX), run_dir, f'secs={SECS}', f'settle={SETTLE}',
                        f'port={BT_PORT}'], capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=SECS + SETTLE + 120)
    with open(os.path.join(run_dir, 'ltclient.txt'), 'w', encoding='utf-8') as f:
        f.write(r.stdout + '\n' + r.stderr)
    d = json.load(open(os.path.join(run_dir, 'samples.json'), encoding='utf-8'))
    head = {k: v for k, v in d['row'].items() if k != 'cleaned'}
    return head, d['samples'], {}


def summarize(samples, secs):
    ok = [s for s in samples if 'error' not in s and s.get('downloaded') is not None]
    meta = next((s['t'] for s in ok if s.get('metadata')), None)
    first_byte = next((s['t'] for s in ok if (s.get('downloaded') or 0) > 0), None)
    upto = [s for s in ok if s['t'] <= secs + 0.5]
    mbps = round(upto[-1]['downloaded'] / upto[-1]['t'] / 1e6, 2) if upto and upto[-1]['t'] > 0 else None
    peak = max((s.get('rate') or 0 for s in ok), default=0) / 1e6
    peers = [s.get('peers') or 0 for s in ok]
    # Which peers carried it: each peer's largest observed rate, top 5.
    best = {}
    for s in ok:
        for w in s.get('wires') or []:
            best[w['address']] = max(best.get(w['address'], 0), w.get('down') or 0)
    top = sorted(best.values(), reverse=True)[:5]
    return dict(metadataS=meta, firstByteS=first_byte, mbps=mbps, peakMBps=round(peak, 2),
                peersAt10s=next((s.get('peers') for s in ok if s['t'] >= 10), None), peersMax=max(peers, default=0),
                peersSeen=len(best), topPeerMBps=[round(x / 1e6, 2) for x in top])


def main():
    turbos = [Turbo(n, e) for n, e in VARIANTS] or [Turbo()]
    clients = {c.name: c for c in (Official(), *turbos, Qbt(), Lt()) if c.name in ROWS or (VARIANTS and isinstance(c, Turbo))}
    order = list(clients)
    results = []
    for rnd in range(ROUNDS):
        rot = order[rnd % len(order):] + order[:rnd % len(order)]
        for name in rot:
            c = clients[name]
            run_dir = os.path.join(OUT, f'r{rnd + 1}-{name}')
            os.makedirs(run_dir, exist_ok=True)
            print(f'round {rnd + 1} {name}: start', flush=True)
            c.start(run_dir)
            cold = c.clean()  # nothing left from an earlier run
            if name != 'lt':  # ltclient.py settles on its own
                time.sleep(SETTLE)
            try:
                if name == 'lt':
                    head, samples, extra = run_lt(run_dir)
                elif name == 'qbt':
                    head, samples, extra = run_qbt(run_dir)
                else:
                    head, samples, extra = run_player(c, run_dir)
            finally:
                cleaned = c.clean()
                c.stop()
                if name == 'official':
                    cleaned = c.clean()  # files are deleted once the server has let go of them
            row = dict(round=rnd + 1, client=name, coldStart=cold, cleaned=cleaned, **head, **summarize(samples, SECS))
            with open(os.path.join(run_dir, 'samples.json'), 'w', encoding='utf-8') as f:
                json.dump(dict(row=row, samples=samples, **extra), f, indent=1)
            results.append(row)
            print(json.dumps(row), flush=True)
    agg = {}
    for name in order:
        rs = [r for r in results if r['client'] == name]
        agg[name] = {}
        for k in ('firstFrameS', 'metadataS', 'firstByteS', 'mbps', 'peakMBps', 'peersMax', 'stalls', 'cpuS'):
            vals = [r[k] for r in rs if r.get(k) is not None]
            agg[name][k] = dict(median=round(statistics.median(vals), 2), min=min(vals), max=max(vals), n=len(vals)) if vals else None
        agg[name]['failedStarts'] = sum(1 for r in rs if r.get('firstFrameS') is None)
    out = dict(secs=SECS, settle=SETTLE, rounds=ROUNDS, runs=results, summary=agg)
    with open(os.path.join(OUT, 'results.json'), 'w', encoding='utf-8') as f:
        json.dump(out, f, indent=1)
    print(json.dumps(agg, indent=1))
    print('->', os.path.abspath(OUT))


if __name__ == '__main__':
    main()
