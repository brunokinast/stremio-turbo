"""Offline check of smartplay_emul against a fake server on 127.0.0.1 (no swarm): the fastest
source wins without a runtime, the losers are captured then removed, the settle rule picks the leader,
a lone racer wins at 1 MiB, race reads are probes, preload sends the app's body; with a runtime
(step 6e) the highest-bitrate source that keeps up wins, in the race and after a dwell, larger ones
too slow are listed as passed, and with none keeping up the settle picks the best rate / bitrate.
Run: python -I test_smartplay_emul.py"""
import json, os, sys, threading, time
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import smartplay_emul as se

RATES = {}       # infoHash -> bytes per second (0: never delivers)
METADATA = {}    # infoHash -> seconds until stats show files
DL = {}          # infoHash -> download bytes per second the stats report (`downloaded` grows by it)
created, removed, streamed = [], [], []
T_START = time.time()


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        n = int(self.headers.get('Content-Length', 0))
        created.append((self.path, json.loads(self.rfile.read(n))))
        self.send_response(200); self.end_headers(); self.wfile.write(b'{}')

    def do_GET(self):
        parts = self.path.split('?')[0].strip('/').split('/')
        ih = parts[0]
        if parts[-1] == 'stats.json':
            files = [1] if time.time() - T_START >= METADATA.get(ih, 0) else []
            body = json.dumps({'files': files, 'peers': 3, 'downloaded': int(DL.get(ih, 0) * (time.time() - T_START))}).encode()
            self.send_response(200); self.end_headers(); self.wfile.write(body); return
        if parts[-1] == 'remove':
            removed.append(ih); self.send_response(200); self.end_headers(); return
        streamed.append((self.path, self.headers['Range']))
        rate = RATES.get(ih, 0)
        start, end = map(int, self.headers['Range'].split('=')[1].split('-'))
        self.send_response(206); self.end_headers()
        sent = 0
        try:
            while start + sent <= end:
                if rate == 0:
                    time.sleep(0.1); continue
                n = min(65536, end - start - sent + 1)
                self.wfile.write(b'\0' * n); sent += n
                time.sleep(n / rate)
        except (ConnectionError, OSError):
            pass


def run(rates, metadata=None, start_fraction=0.0, runtime=None, dl=None, sizes=None, dwell=0.3):
    global T_START
    RATES.clear(); RATES.update(rates); METADATA.clear(); METADATA.update(metadata or {})
    DL.clear(); DL.update(dl or {})
    created.clear(); removed.clear(); streamed.clear(); T_START = time.time()
    srv = ThreadingHTTPServer(('127.0.0.1', 0), H)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    base = f'http://127.0.0.1:{srv.server_address[1]}'
    events, captured = [], []
    sp = se.SmartPlay(base, time.perf_counter(), lambda n, **kw: events.append(n), start_fraction=start_fraction, runtime=runtime)
    sp.meter.memory = getattr(run, 'memory', None)
    size = 1000 << 20 if start_fraction else None
    srcs = [dict(infoHash=ih, fileIdx=1, trackers=['tracker:udp://t.example:1/announce', 'dht:' + ih],
                 size=(sizes or {}).get(ih, size)) for ih in rates]
    sp.preload(srcs)
    sp.dwell(srcs, dwell)
    win, rec = sp.race(srcs, captured.append)
    srv.shutdown()
    run.passed = sp.passed
    return win, rec, captured


def main():
    # Fastest wins (others have data too, so it reads 8 MiB); losers captured and removed.
    win, rec, captured = run({'a' * 40: 1 << 20, 'b' * 40: 16 << 20, 'c' * 40: 0})
    assert win['infoHash'] == 'b' * 40, win
    assert sorted(captured) == sorted(removed) == ['a' * 40, 'c' * 40], (captured, removed)
    w = next(r for r in rec if r['winner'])
    assert [r['winner'] for r in rec].count(True) == 1 and w['done8MiB'] is not None and w['decided'] == 'race', rec
    # Race reads are probes.
    assert streamed and all('probe=1' in p for p, _ in streamed), streamed
    assert all(r.startswith('bytes=0-') for _, r in streamed), streamed
    # Preload sends the addon's sources as given.
    path, body = created[0]
    assert path.endswith('/create') and body['fileIdx'] == 1, created[0]
    assert body['peerSearch']['sources'][0] == 'tracker:udp://t.example:1/announce', body
    assert 'startFraction' not in body, body
    # Nobody reaches 8 MiB: the leader wins at the settle time.
    se.SETTLE_AFTER = 3.0
    t = time.time()
    win, rec, _ = run({'a' * 40: 256 << 10, 'b' * 40: 1 << 20, 'c' * 40: 0})
    assert win['infoHash'] == 'b' * 40 and 2.5 < time.time() - t < 6, (win, time.time() - t)
    # Sources with metadata race first; a racer alone with data wins at 1 MiB.
    se.SETTLE_AFTER = 30.0
    se.START_RACERS = 1
    win, rec, _ = run({'a' * 40: 16 << 20, 'b' * 40: 16 << 20}, metadata={'a' * 40: 999, 'b' * 40: 0})
    assert rec[0]['infoHash'] == 'b' * 40 and rec[0]['decided'] == 'alone', rec
    assert se.ALONE_WIN_BYTES <= rec[0]['read'] < se.RACE_BYTES, rec
    se.START_RACERS = 3
    # Step 6e, runtime 100 s: a needs 10 MB/s and gets 12 (keeps up), b needs 1 and gets 2 (keeps up,
    # smaller), c needs 30 and gets 3 (too slow). Best picture that keeps up: a; c passed.
    A, B, C = 'a' * 40, 'b' * 40, 'c' * 40
    sizes = {A: 10 ** 9, B: 10 ** 8, C: 3 * 10 ** 9}
    dl = {A: 12e6, B: 2e6, C: 3e6}
    reads = {A: 16 << 20, B: 16 << 20, C: 16 << 20}
    win, rec, _ = run(reads, runtime=100, dl=dl, sizes=sizes)
    assert win['infoHash'] == A and next(r for r in rec if r['winner'])['decided'] == 'keepsup', rec
    assert [x['infoHash'] for x in run.passed] == [C], run.passed
    # After a dwell the same choice is made without reading, the others removed.
    win, rec, _ = run(reads, runtime=100, dl=dl, sizes=sizes, dwell=2.5)
    assert win['infoHash'] == A and all(r['decided'] == 'dwell' for r in rec), rec
    assert not streamed and sorted(removed) == [B, C], (streamed, removed)
    # A larger source with no rate yet (on disk, downloading nothing) stops the dwell decision: the
    # race measures it, and its read rate (16 MiB/s >= 10) makes it the choice.
    win, rec, _ = run(reads, runtime=100, dl={B: 2e6, C: 3e6}, sizes=sizes, dwell=2.5)
    assert win['infoHash'] == A and next(r for r in rec if r['winner'])['decided'] != 'dwell', rec
    # A larger racer that can't keep up (0.1x) doesn't hold back the smaller one keeping up.
    win, rec, _ = run({C: 16 << 20, B: 2 << 20}, runtime=100, dl={B: 2e6, C: 3e6}, sizes=sizes)
    assert win['infoHash'] == B, rec
    # A small file with data doesn't win while a larger one (no metadata for 2 s, so no data yet)
    # may still keep up; the larger one then does (12 of 10 MB/s) and wins.
    win, rec, _ = run({B: 16 << 20, A: 16 << 20}, runtime=100, dl={A: 12e6, B: 2e6}, sizes=sizes, metadata={A: 2})
    assert win['infoHash'] == A, rec
    # A larger file on disk (trickle downloads, reads at once) beats a smaller one on disk.
    win, rec, _ = run({B: 1 << 30, A: 1 << 30}, runtime=100, dl={A: 2e3, B: 2e3}, sizes=sizes, dwell=2.5)
    assert win['infoHash'] == A, rec
    # None keeps up: at the settle time the best rate / bitrate wins (c: 0.1, a: 0.05).
    se.SETTLE_AFTER = 3.0
    win, rec, _ = run({A: 256 << 10, C: 1 << 20}, runtime=100, dl={A: 0.5e6, C: 3e6}, sizes=sizes)
    assert win['infoHash'] == C and next(r for r in rec if r['winner'])['decided'] == 'settle', rec
    se.SETTLE_AFTER = 30.0
    # A resume: the preload asks to prime there, and the race reads there.
    win, rec, _ = run({'a' * 40: 16 << 20, 'b' * 40: 0}, start_fraction=0.25)
    assert all(b.get('startFraction') == 0.25 for _, b in created), created
    reads = [r for p, r in streamed if p.startswith('/' + 'a' * 40)]
    assert reads and all(r.startswith(f'bytes={250 << 20}-') for r in reads), streamed
    # Step 6h: a larger racer remembered as too slow (0.1x) doesn't hold the smaller one keeping up
    # (c never delivers, so it has no live rate), and is reported as passed, remembered.
    mem = se.RateMemory()
    mem.remember(C, 0.1)
    run.memory = mem
    t = time.time()
    win, rec, _ = run({A: 16 << 20, C: 0}, runtime=100, dl={A: 12e6}, sizes=sizes, metadata={C: 999})
    assert win['infoHash'] == A and time.time() - t < 5, (rec, time.time() - t)
    assert [(x['infoHash'], x['remembered']) for x in run.passed] == [(C, True)], run.passed
    # A live rate overrides memory: remembered 0.1x, live 1.2x keeps up and wins as the larger.
    mem.remember(C, 0.1)
    win, rec, _ = run({A: 16 << 20, C: 16 << 20}, runtime=100, dl={A: 12e6, C: 36e6}, sizes=sizes)
    assert win['infoHash'] == C, rec
    # Entries older than a day are ignored.
    mem.entries[C] = dict(ratio=0.1, at=time.time() - 25 * 3600)
    assert mem.recall(C) is None
    run.memory = None
    # Step 6f: the start buffer holds below 2x the bitrate (A3: 3.09 of 2.85 MB/s), not above
    # (A2: 14.75), and never without a rate or bitrate.
    assert se.should_hold(3.09e6, 2.85e6) and not se.should_hold(14.75e6, 2.85e6)
    assert not se.should_hold(None, 2.85e6) and not se.should_hold(3e6, None)
    ahead_checks()
    print('ok')


def ahead_checks():
    """Step 8c rule. HotD: 30 min left, 66 min episode (window 96 min = 5760 s)."""
    W = 30 * 60 + 66 * 60
    # The user's example: 11.3 GB needs 1.96 MB/s (2.45 with the margin); the 35.5 GB remux 6.2.
    assert se.fits_window(11.3e9, 3.8 * 11.3e9 / (66 * 60), W)
    assert not se.fits_window(35.5e9, 0.3 * 35.5e9 / (66 * 60), W)
    # Exactly at the edge: 80% of the rate fills the window.
    assert se.fits_window(8e9, 1e9 / W * 10, W) and not se.fits_window(8e9, 1e9 / W * 9.99, W)
    tiers = [
        ('4k dv | hdr', [{'infoHash': 'r', 'size': 35.5e9, 'fileIdx': 0}, {'infoHash': 'f', 'size': 11.3e9, 'fileIdx': 3}]),
        ('4k', [{'infoHash': 'g', 'size': 15e9, 'fileIdx': 0}]),
        ('1080p', [{'infoHash': 'h', 'size': 4e9, 'fileIdx': 0}]),
        ('720p', [{'infoHash': 'i', 'size': 1.5e9, 'fileIdx': 0}]),
    ]
    rates = {'r': 0.5e6, 'f': 4e6, 'g': 4e6, 'h': 0.5e6, 'i': 0.5e6}
    pick, stepped = se.choose_ahead(tiers, '4k dv | hdr', lambda s: rates[s['infoHash']], W)
    assert pick['infoHash'] == 'f' and not stepped, pick  # best that fits in the remembered tier
    # Nothing in 4K fits: drop to 1080p (which doesn't fit at 0.5 MB/s), then 720p.
    rates.update({'f': 0.5e6, 'g': 0.5e6})
    pick, stepped = se.choose_ahead(tiers, '4k dv | hdr', lambda s: rates[s['infoHash']], W)
    assert pick['infoHash'] == 'i' and stepped, pick
    # Unknown rates never fit; nothing fits at all: None.
    pick, _ = se.choose_ahead(tiers, '4k dv | hdr', lambda s: None, W)
    assert pick is None
    # More time (episode 2 ahead: window + another runtime) lets a bigger file fit.
    rates.update({'f': 1.8e6})
    assert se.choose_ahead(tiers, '4k dv | hdr', lambda s: rates[s['infoHash']], W)[0]['infoHash'] == 'i'
    assert se.choose_ahead(tiers, '4k dv | hdr', lambda s: rates[s['infoHash']], W + 66 * 60)[0]['infoHash'] == 'f'
    # Step 9c (run F): playing S02E04 from pack f. Its S02E05 is on trial (no probe); the remux is
    # measured too slow; the small copy 'z' is complete on disk. The best that fits is the pack.
    tiers9 = [('4k dv | hdr', [{'infoHash': 'r', 'size': 35.5e9, 'fileIdx': 0}, {'infoHash': 'f', 'size': 11.3e9, 'fileIdx': 4},
                               {'infoHash': 'z', 'size': 1.9e9, 'fileIdx': 5}])]
    rate9 = lambda slow: (lambda s: se.ahead_rate(s, {'r': 0.5e6}, 'f', {('z', 5)}, slow))
    pick, stepped = se.choose_ahead(tiers9, '4k dv | hdr', rate9(set()), W)
    assert pick['infoHash'] == 'f' and not stepped, pick
    # The trial's 60 s check found it too slow: it joins the slow list, and the copy on disk is chosen.
    pick, stepped = se.choose_ahead(tiers9, '4k dv | hdr', rate9({'f'}), W)
    assert pick['infoHash'] == 'z' and not stepped, pick
    # Not playing that pack: it's measured like any other (unknown rate here, so not chosen).
    pick, _ = se.choose_ahead(tiers9, '4k dv | hdr', lambda s: se.ahead_rate(s, {'r': 0.5e6}, 'other', {('z', 5)}, set()), W)
    assert pick['infoHash'] == 'z', pick


if __name__ == "__main__":
    main()
