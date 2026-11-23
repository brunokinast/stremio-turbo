"""Single-connection ceiling (plan 20 Part B): how fast one peer can feed Turbo's Rivulet, against
libtorrent, with the swarm taken out. Everything runs on 127.0.0.1.

usage (Python 3.13; libtorrent 2.1.1 from research/lab/bench/.pylib):
  py -3.13 ceiling.py [key=value ...]
    size=4 (GiB of random data to seed; made once in research/data/ceiling)  secs=30 (per run)
    rows=turbo,lt  rounds=2  delay=0 (ms one way, through a relay between the downloader and the seeder)
    utp=0 (1: the seeder takes uTP only, else TCP only)
    variants=label:ENV=v;ENV2=w|label2: (Turbo rows with their own environment, as compare.py)

The seeder is a libtorrent session seeding one file (16 MiB pieces, like a 4K release). Rows:
- turbo: the installed rivulet-engine.exe (APP_PATH in research/data/ceiling, the owner's data is
  untouched): POST /create, the seeder dials Rivulet's BT port (Rivulet ignores loopback trackers,
  an SSRF guard), then the file is read over HTTP as fast as it comes.
  Rivulet's /debug/trace is kept per run (per-peer outstanding, queue time, round trip).
- lt: a second libtorrent session (qBittorrent's defaults), sequential, connected to the seeder.
Each run reports MB/s over the run and over its second half (the steady state), and its CPU seconds.
"""
import hashlib, json, os, queue, shutil, socket, socketserver, statistics, subprocess, sys, threading, time
import urllib.error, urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, '.pylib'))
import libtorrent as lt  # noqa: E402

sys.stdout.reconfigure(encoding='utf-8')
cfg = dict(size='4', secs='30', rows='turbo,lt', rounds='2', delay='0', utp='0', variants='')
for a in sys.argv[1:]:
    k, v = a.split('=', 1)
    cfg[k] = v
SECS, ROUNDS, DELAY = float(cfg['secs']), int(cfg['rounds']), float(cfg['delay']) / 1000
ROWS = cfg['rows'].split(',')
VARIANTS = [(v.split(':', 1)[0], dict(kv.split('=', 1) for kv in v.split(':', 1)[1].split(';') if kv))
            for v in cfg['variants'].split('|') if v]
LOCAL = os.environ['LOCALAPPDATA']
TURBO_DIR = os.path.join(LOCAL, 'Programs', 'Stremio Turbo')
# Absolute: libtorrent's long-path prefix on Windows doesn't resolve '..'.
WORK = os.path.abspath(os.path.join(HERE, '..', '..', 'data', 'ceiling'))
OUT = os.path.join(WORK, time.strftime('run-%Y%m%d-%H%M%S'))
SEED_PORT, RELAY_PORT, LT_PORT, RIV_BT_PORT = 49990, 49994, 49992, 49993
SERVER = 'http://127.0.0.1:11470'
os.makedirs(OUT, exist_ok=True)


def log(*a):
    print(time.strftime('%H:%M:%S'), *a, flush=True)


# ---- the file and its torrent --------------------------------------------------------------------

def make_torrent():
    data_dir = os.path.join(WORK, 'seed')
    name = f'ceiling-{cfg["size"]}g.bin'
    path = os.path.join(data_dir, name)
    tpath = path + '.torrent'
    size = int(float(cfg['size']) * (1 << 30))
    if not (os.path.exists(path) and os.path.getsize(path) == size):
        os.makedirs(data_dir, exist_ok=True)
        log('writing', size >> 20, 'MiB of random data')
        with open(path, 'wb') as f:
            left = size
            while left:
                n = min(left, 64 << 20)
                f.write(os.urandom(n))
                left -= n
        if os.path.exists(tpath):
            os.remove(tpath)
    if not os.path.exists(tpath):
        fs = lt.file_storage()
        fs.add_file(name, size)
        t = lt.create_torrent(fs, 16 << 20, flags=lt.create_torrent.v1_only)
        # Hashed here: libtorrent's set_piece_hashes fails from Python on Windows (I/O aborted).
        with open(path, 'rb') as f:
            for i in range(t.num_pieces()):
                t.set_hash(i, hashlib.sha1(f.read(16 << 20)).digest())
        with open(tpath, 'wb') as f:
            f.write(lt.bencode(t.generate()))
    with open(tpath, "rb") as f:
        return data_dir, lt.torrent_info(lt.bdecode(f.read()))


# ---- seeder, tracker, relay ----------------------------------------------------------------------

def session(port, utp_only=None):
    pack = {'listen_interfaces': f'127.0.0.1:{port}', 'enable_dht': False, 'enable_lsd': False,
            'enable_upnp': False, 'enable_natpmp': False, 'connections_limit': 500,
            'allow_multiple_connections_per_ip': True, 'alert_mask': lt.alert_category.error}
    if utp_only is not None:
        pack.update(enable_incoming_utp=utp_only, enable_outgoing_utp=utp_only,
                    enable_incoming_tcp=not utp_only, enable_outgoing_tcp=not utp_only)
    return lt.session(pack)


class Relay(socketserver.ThreadingTCPServer):
    """TCP relay that holds each direction's bytes for DELAY: the request/response round trip of
    a far peer. Loopback sockets on either side, so the OS window doesn't bind; target is the
    side the dialler wants (the seeder for lt, Rivulet's BT port for turbo)."""
    daemon_threads = True
    allow_reuse_address = True


class RelayHandler(socketserver.BaseRequestHandler):
    target = SEED_PORT

    def handle(self):
        up = socket.create_connection(('127.0.0.1', RelayHandler.target))
        for s in (self.request, up):
            s.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
        done = threading.Event()
        for src, dst in ((self.request, up), (up, self.request)):
            q = queue.Queue()
            threading.Thread(target=self.read, args=(src, q, done), daemon=True).start()
            threading.Thread(target=self.write, args=(dst, q, done), daemon=True).start()
        done.wait()
        for s in (self.request, up):
            try:
                s.close()
            except OSError:
                pass

    @staticmethod
    def read(src, q, done):
        try:
            while not done.is_set():
                b = src.recv(1 << 18)
                if not b:
                    break
                q.put((time.perf_counter() + DELAY, b))
        except OSError:
            pass
        q.put(None)

    @staticmethod
    def write(dst, q, done):
        try:
            while True:
                item = q.get()
                if item is None:
                    break
                due, b = item
                wait = due - time.perf_counter()
                if wait > 0:
                    time.sleep(wait)
                dst.sendall(b)
        except OSError:
            pass
        done.set()


# ---- rows ----------------------------------------------------------------------------------------

def http(url, body=None, timeout=10, method=None):
    req = urllib.request.Request(url, data=body, method=method, headers={'Content-Type': 'application/json'} if body else {})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read()


def proc_cpu(pid):
    out = subprocess.run(['powershell', '-NoProfile', '-Command', f'(Get-Process -Id {pid}).TotalProcessorTime.TotalSeconds'],
                         capture_output=True, text=True).stdout.strip()
    try:
        return float(out.replace(',', '.'))
    except ValueError:
        return None


def run_turbo(ti, run_dir, env_extra, seed):
    ih = str(ti.info_hash())
    app = os.path.join(WORK, 'turbo-app')
    shutil.rmtree(app, ignore_errors=True)
    owner = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(86400)'], creationflags=subprocess.CREATE_NO_WINDOW)
    env = dict(os.environ, BIND_ADDRESS='127.0.0.1', HTTP_PORT='11470', HTTPS_PORT='0', BT_LISTEN_PORT=str(RIV_BT_PORT),
               APP_PATH=app, RIVULET_OWNER_PID=str(owner.pid), RIVULET_LOG_FILE=os.path.join(run_dir, 'rivulet.log'),
               RIVULET_SPEED_LIMITS='0', PATH=TURBO_DIR + os.pathsep + os.environ.get('PATH', ''), **env_extra)
    p = subprocess.Popen([os.path.join(TURBO_DIR, 'rivulet-engine.exe')], cwd=TURBO_DIR, env=env,
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, creationflags=subprocess.CREATE_NO_WINDOW)
    try:
        for _ in range(100):
            try:
                http(SERVER + '/heartbeat', timeout=1)
                break
            except Exception:
                time.sleep(0.3)
        cpu0 = proc_cpu(p.pid)
        body = json.dumps({'torrent': {'infoHash': ih}, 'peerSearch': {'sources': [f'dht:{ih}'], 'min': 40, 'max': 200}}).encode()
        t0 = time.perf_counter()
        created = threading.Event()
        threading.Thread(target=lambda: (http(f'{SERVER}/{ih}/create', body, timeout=60), created.set()), daemon=True).start()
        RelayHandler.target = RIV_BT_PORT
        dial = RELAY_PORT if DELAY > 0 else RIV_BT_PORT
        stop = threading.Event()

        def dialer():
            # Rivulet only hears of the seeder by its dials: dial whenever they aren't connected. Not
            # blindly: through the relay, libtorrent files the connection under Rivulet's own
            # listen port (its extension handshake), so a second dial opens a duplicate connection.
            while not stop.wait(1.0):
                if seed.status().num_peers == 0:
                    seed.connect_peer(('127.0.0.1', dial))
        threading.Thread(target=dialer, daemon=True).start()
        if not created.wait(60):
            raise RuntimeError('create did not finish')
        log('created', round(time.perf_counter() - t0, 2), 's; seeder peers', seed.status().num_peers)
        samples, got = [], 0
        with urllib.request.urlopen(f'{SERVER}/{ih}/0', timeout=20) as r:
            first = None
            nxt = 1.0
            while True:
                b = r.read(1 << 20)
                if not b:
                    break
                now = time.perf_counter() - t0
                if first is None:
                    first = now
                got += len(b)
                if now >= nxt:
                    samples.append(dict(t=round(now, 2), downloaded=got))
                    nxt += 1.0
                if now >= SECS:
                    break
        stop.set()
        cpu = proc_cpu(p.pid)
        with open(os.path.join(run_dir, 'trace.json'), 'wb') as f:
            f.write(http(f'{SERVER}/debug/trace/{ih}', timeout=30))
        try:
            with open(os.path.join(run_dir, 'locks.json'), 'wb') as f:
                f.write(http(f'{SERVER}/debug/locks', timeout=30))
        except Exception:
            pass
        try:
            http(f'{SERVER}/downloads/{ih}', method='DELETE', timeout=30)
        except urllib.error.HTTPError:
            pass
        return dict(firstByteS=round(first, 2) if first else None, cpuS=round(cpu - cpu0, 1) if cpu and cpu0 else None), samples
    except Exception:
        for path, name in ((f'/debug/trace/{ih}', 'trace.json'), (f'/{ih}/stats.json', 'stats.json'), ('/debug/status', 'status.json')):
            try:
                with open(os.path.join(run_dir, name), 'wb') as f:
                    f.write(http(SERVER + path, timeout=10))
            except Exception:
                pass
        raise
    finally:
        owner.kill()
        try:
            p.wait(30)
        except subprocess.TimeoutExpired:
            p.kill()
        for _ in range(60):
            try:
                http(SERVER + '/heartbeat', timeout=1)
                time.sleep(0.5)
            except Exception:
                break


def run_lt(ti, run_dir, peer_port):
    RelayHandler.target = SEED_PORT
    s = session(LT_PORT)
    save = os.path.join(run_dir, 'files')
    p = lt.add_torrent_params()
    p.ti = ti
    p.save_path = save
    p.flags |= lt.torrent_flags.sequential_download
    t0 = time.perf_counter()
    h = s.add_torrent(p)
    h.connect_peer(('127.0.0.1', peer_port))
    samples = []
    cpu0 = time.process_time()
    while time.perf_counter() - t0 < SECS:
        time.sleep(1.0 - ((time.perf_counter() - t0) % 1.0))
        st = h.status()
        samples.append(dict(t=round(time.perf_counter() - t0, 2), downloaded=st.total_wanted_done, peers=st.num_peers))
        if st.is_seeding:
            break
    peers = [dict(ip=f'{pi.ip[0]}:{pi.ip[1]}', MBps=round(pi.total_download / 1e6 / SECS, 1)) for pi in h.get_peer_info()]
    log('lt peers', peers)
    s.remove_torrent(h, lt.session.delete_files)
    time.sleep(1)
    del s
    shutil.rmtree(save, ignore_errors=True)
    return dict(cpuS=None), samples


def summarize(samples):
    if not samples:
        return {}
    end = samples[-1]
    half = min(samples, key=lambda x: abs(x['t'] - end['t'] / 2))
    return dict(mbps=round(end['downloaded'] / end['t'] / 1e6, 1),
                steadyMBps=round((end['downloaded'] - half['downloaded']) / max(0.1, end['t'] - half['t']) / 1e6, 1))


def main():
    data_dir, ti = make_torrent()
    log('torrent', ti.info_hash(), ti.total_size() >> 20, 'MiB')
    seeder = session(SEED_PORT, utp_only=cfg['utp'] == '1')
    sp = lt.add_torrent_params()
    sp.ti = ti
    sp.save_path = data_dir
    seed = seeder.add_torrent(sp)
    # A full check up front (not seed_mode, which hashes each piece on its first request) so the
    # seeder only reads while serving.
    while not seed.status().is_seeding:
        time.sleep(0.5)
    log('seeding')
    peer_port = SEED_PORT
    if DELAY > 0:
        relay = Relay(('127.0.0.1', RELAY_PORT), RelayHandler)
        threading.Thread(target=relay.serve_forever, daemon=True).start()
        peer_port = RELAY_PORT
    time.sleep(1)

    clients = []
    for r in ROWS:
        if r == 'turbo':
            clients += [(n, e) for n, e in VARIANTS] or [('turbo', {})]
        else:
            clients.append((r, None))
    results = []
    for rnd in range(1, ROUNDS + 1):
        order = clients[rnd - 1:] + clients[:rnd - 1]
        for name, env in order:
            run_dir = os.path.join(OUT, f'r{rnd}-{name}')
            os.makedirs(run_dir, exist_ok=True)
            log(f'round {rnd} {name}')
            head, samples = run_turbo(ti, run_dir, env, seed) if env is not None else run_lt(ti, run_dir, peer_port)
            row = dict(round=rnd, client=name, delayMs=DELAY * 1000, **head, **summarize(samples))
            with open(os.path.join(run_dir, 'samples.json'), 'w', encoding='utf-8') as f:
                json.dump(dict(row=row, samples=samples), f)
            log(json.dumps(row))
            results.append(row)
    agg = {}
    for name, _ in clients:
        rs = [r for r in results if r['client'] == name]
        agg[name] = {k: statistics.median([r[k] for r in rs if r.get(k) is not None] or [0]) for k in ('mbps', 'steadyMBps', 'cpuS')}
    with open(os.path.join(OUT, 'results.json'), 'w', encoding='utf-8') as f:
        json.dump(dict(cfg=cfg, runs=results, summary=agg), f, indent=1)
    print(json.dumps(agg, indent=1))
    print('->', os.path.abspath(OUT))


if __name__ == '__main__':
    main()
