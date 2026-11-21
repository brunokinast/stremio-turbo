"""The web client's smart-play start, emulated against Rivulet for measured plays (mpvplay.py
race=/preload=). It follows stremio-web/src/smartPlay exactly, bugs included:

- preload (useSmartPlay.ts, serverApi.preload): POST /{ih}/create for the first 3 sources of the
  chosen quality with {torrent:{infoHash}, fileIdx, startFraction (a resume, step 6c),
  peerSearch:{sources: announce}}; the announce list is the stream's own `sources` as the addon
  sent it ("tracker:<url>", "dht:<hash>"). When the remembered quality isn't offered the app preloads
  the most-seeded tier at its resolution (else the first at 4K or below) and says so in a toast;
  here the caller names the tier.
- choice (race.ts + supply.ts, step 6e): "best picture that keeps up". A source keeps up when its
  download rate (average over its last 3 active intervals of stats `downloaded`, or its race read
  rate if higher) is >= 1.0 x its bitrate (size / runtime). After a dwell (preloaded sources
  sampled every 2 s) the highest-bitrate source with metadata that keeps up wins without a race,
  unless a larger one has no rate yet (then the race measures it). Otherwise sources with metadata go first; 3 racers
  read 8 MiB from the start position, floor(size x startFraction) clamped to size - 8 MiB (Range
  requests on the probe URL, /{ih}/{fileIdx}?tr=...&probe=1); every 1 s poll, the highest-bitrate
  racer with data that keeps up wins, but for 10 s not while a larger racer may still (no rate, or
  >= 0.5 x its bitrate); a racer alone with 1 MiB wins at once on the same terms; without a runtime the first to read 8 MiB wins; with no data from anyone for
  15 s, 2 more join (up to 7); once anyone has data, at 30 s the best rate / bitrate (else most
  bytes) wins; a failed read retries after 2 s; the losers are removed. Larger sources passed over
  are listed in SmartPlay.passed (the app shows a toast).

Every time is relative to the caller's clock (T0) and recorded per racer.
"""
import json, re, threading, time, urllib.parse, urllib.request

PRELOAD_SOURCES = 3
START_RACERS, MAX_RACERS, WIDEN_BY = 3, 7, 2
WIDEN_AFTER, SETTLE_AFTER, POLL, RETRY = 15.0, 30.0, 1.0, 2.0
RACE_BYTES = 8 * 1024 * 1024
ALONE_WIN_BYTES = 1024 * 1024
VIABLE_RATIO, DWELL_POLL, RATE_INTERVALS, PROMISING_RATIO, LARGER_GRACE = 1.0, 2.0, 3, 0.5, 10.0
MIN_INTERVAL_BYTES, READ_RATE_FLOOR = 256 * 1024, 0.05
SIZE_UNITS = {'KB': 1e3, 'MB': 1e6, 'GB': 1e9, 'TB': 1e12}


def parse_size(stream):
    """The file size like groupByQuality.parseSize: behaviorHints.videoSize, else the description's
    "💾 9.9 GB"."""
    v = (stream.get('behaviorHints') or {}).get('videoSize')
    if isinstance(v, (int, float)):
        return int(v)
    m = re.search(r'💾\s*([\d.]+)\s*(KB|MB|GB|TB)', stream.get('description') or stream.get('title') or '')
    return round(float(m.group(1)) * SIZE_UNITS[m.group(2)]) if m else None


def load_sources(path, label, limit):
    """The quality tier `label` (the stream name's second line) from a saved addon response, in the
    addon's order (the app re-sorts by live seeders; the addon lists by seeders too), at most limit
    (the user's Torrentio limit)."""
    with open(path, encoding='utf-8') as f:
        streams = json.load(f)['streams']
    out = []
    for s in streams:
        lines = (s.get('name') or '').split('\n')
        if len(lines) > 1 and lines[1].strip().lower() == label.lower() and isinstance(s.get('infoHash'), str):
            out.append(dict(infoHash=s['infoHash'].lower(), fileIdx=s.get('fileIdx'), trackers=s.get('sources') or [],
                            size=parse_size(s)))
    return out[:limit]


class RateMeter:
    """supply.ts RateMeter: the average over the last RATE_INTERVALS sample intervals (>= 0.5 s)
    with download activity, or a race read's rate if higher (data on disk downloads nothing)."""

    def __init__(self):
        self.last, self.active, self.reads = {}, {}, {}
        self.memory = None

    def add(self, ih, at, downloaded):
        prev = self.last.get(ih)
        if prev is not None and at - prev[0] < 0.5:
            return
        if prev is not None and downloaded - prev[1] >= MIN_INTERVAL_BYTES:
            self.active[ih] = (self.active.get(ih, []) + [(at - prev[0], downloaded - prev[1])])[-RATE_INTERVALS:]
        self.last[ih] = (at, downloaded)

    def add_read(self, ih, rate):
        self.reads[ih] = rate

    def rate(self, ih):
        iv = self.active.get(ih)
        download = sum(b for _, b in iv) / sum(t for t, _ in iv) if iv else None
        read = self.reads.get(ih)
        if download is None:
            return read
        return download if read is None else max(download, read)


def needed_rate(src, runtime):
    return src['size'] / runtime if src.get('size') and runtime else None


def keeps_up(rate, needed):
    if rate is None or needed is None:
        return None
    return rate >= VIABLE_RATIO * needed


def may_keep_up(rate, needed):
    return needed is not None and keeps_up(rate, needed) is not True and (rate is None or rate >= PROMISING_RATIO * needed)


def should_hold(rate, needed):
    """startBuffer.ts shouldHold: hold the start on its first frame (until 4 s is buffered, at
    most 20 s) when the chosen source's rate is under 2x its bitrate; unknown: no hold."""
    return rate is not None and needed is not None and rate < 2 * needed


AHEAD_MARGIN = 0.8


def fits_window(size, rate, window, margin=AHEAD_MARGIN):
    """ahead.ts fitsWindow (step 8c, the user's rule): a download at `rate` stays ahead of
    playback through the next episode when rate x (time left + next runtime) >= size; only
    AHEAD_MARGIN of the measured rate is counted."""
    return bool(rate) and rate > 0 and window > 0 and rate * margin * window >= size


def resolution_of(key):
    token = key.replace('|', ' ').split()[0] if key else ''
    if token == '8k':
        return 4320
    if token == '4k':
        return 2160
    return int(token[:-1]) if token.endswith('p') and token[:-1].isdigit() else None


def ahead_groups(tiers, start_key):
    """ahead.ts aheadGroups: the remembered tier, then each lower resolution (all its tiers).
    tiers: [(key, [sources])] in addon order. (The fallback when start_key isn't offered is
    simplified to the first tier at its resolution.)"""
    start = next((t for t in tiers if t[0] == start_key), None)
    if start is None:
        res = resolution_of(start_key)
        start = next((t for t in tiers if resolution_of(t[0]) == res), tiers[0] if tiers else None)
    if start is None:
        return []
    r0 = resolution_of(start[0])
    lower = sorted({resolution_of(k) for k, _ in tiers if resolution_of(k) is not None and r0 is not None and resolution_of(k) < r0}, reverse=True)
    return [(start[0], start[1])] + [(r, [s for k, srcs in tiers if resolution_of(k) == r for s in srcs]) for r in lower]


def choose_ahead(tiers, start_key, rate_of, window):
    """ahead.ts plan's choice: in each group in turn, the largest source that fits the window."""
    for g, (label, sources) in enumerate(ahead_groups(tiers, start_key)):
        fit = [s for s in sources if s.get('size') and s.get('fileIdx') is not None and rate_of(s) is not None and fits_window(s['size'], rate_of(s), window)]
        if fit:
            return max(fit, key=lambda s: s['size']), g > 0
    return None, False


def ahead_rate(source, measured, playing_ih, on_disk, slow):
    """ahead.ts rateOf (step 9c): a copy complete on disk fits; a source in the torrent being played
    can't be probed (the server holds its other files back), so it fits on trial (checked after
    60 s); otherwise only a rate measured now; sources found too slow never."""
    key = (source['infoHash'], source.get('fileIdx'))
    if source['infoHash'] in slow:
        return None
    if key in on_disk or source['infoHash'] == playing_ih:
        return float('inf')
    return measured.get(source['infoHash'])


MEMORY_TTL = 24 * 3600.0


class RateMemory:
    """rateMemory.ts: each source's last measured rate / bitrate for a day (optionally a JSON file,
    standing in for the app's localStorage across runs)."""

    def __init__(self, path=None):
        self.path, self.entries = path, {}
        if path:
            try:
                with open(path, encoding='utf-8') as f:
                    self.entries = json.load(f)
            except (OSError, ValueError):
                pass

    def remember(self, ih, ratio):
        self.entries[ih] = dict(ratio=ratio, at=time.time())
        if self.path:
            with open(self.path, 'w', encoding='utf-8') as f:
                json.dump(self.entries, f)

    def recall(self, ih):
        e = self.entries.get(ih)
        return e['ratio'] if e and time.time() - e['at'] < MEMORY_TTL else None


def known_rate(meter, src, runtime):
    """supply.ts knownRate: (rate, remembered) from the live rate, else the remembered ratio."""
    live = meter.rate(src['infoHash'])
    if live is not None:
        return live, False
    n = needed_rate(src, runtime)
    ratio = meter.memory.recall(src['infoHash']) if meter.memory else None
    return (ratio * n, True) if n is not None and ratio is not None else None


def known_rate_value(meter, src, runtime):
    k = known_rate(meter, src, runtime)
    return k[0] if k else None


def choose_keeping_up(sources, meter, runtime, measured_only=False):
    """supply.ts chooseKeepingUp: (chosen, passed) or None; with measured_only, None while a larger
    source may keep up (no rate, or only a remembered one that kept up)."""
    rated = [(s, meter.rate(s['infoHash']), needed_rate(s, runtime)) for s in sources]
    viable = [x for x in rated if keeps_up(x[1], x[2]) is True]
    if not viable:
        return None
    best = max(viable, key=lambda x: x[2])
    larger = [(s, n, known_rate(meter, s, runtime)) for s, _, n in rated if n is not None and n > best[2]]
    if measured_only and any(k is None or keeps_up(k[0], n) is True for _, n, k in larger):
        return None
    passed = [dict(infoHash=s['infoHash'], size=s['size'], rate=k[0], needed=n, remembered=k[1])
              for s, n, k in larger if k is not None and keeps_up(k[0], n) is False]
    return best[0], passed


class SmartPlay:
    def __init__(self, server, t0, ev, start_fraction=0.0, prime_resume=True, runtime=None):
        """start_fraction: where the player starts (resume). prime_resume=False leaves it out of the
        preload (the pre-6c behaviour, for a baseline); the race still reads there. runtime: the
        episode's seconds, for bitrates (None: the pre-6e first-to-arrive rules)."""
        self.server, self.t0, self.ev = server, t0, ev
        self.start_fraction, self.prime_resume, self.runtime = start_fraction, prime_resume, runtime
        self.meter = RateMeter()
        self.passed = []

    def now(self):
        return round(time.perf_counter() - self.t0, 3)

    def req(self, path, body=None, timeout=30):
        r = urllib.request.Request(self.server + path, data=json.dumps(body).encode() if body is not None else None,
                                   headers={'Content-Type': 'application/json'} if body is not None else {})
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            return resp.read()

    def stats(self, ih):
        try:
            s = json.loads(self.req(f'/{ih}/stats.json', timeout=5) or b'null')
        except Exception:
            return None
        return s

    def sample(self, ih):
        st = self.stats(ih) or {}
        if st:
            self.meter.add(ih, time.perf_counter(), st.get('downloaded', 0))
        return st

    def stream_path(self, src):
        q = urllib.parse.urlencode([('tr', t) for t in src['trackers']] + [('probe', '1')])
        return f"/{src['infoHash']}/{src['fileIdx'] if src['fileIdx'] is not None else -1}?{q}"

    def preload(self, sources):
        for src in sources[:PRELOAD_SOURCES]:
            body = {'torrent': {'infoHash': src['infoHash']},
                    'peerSearch': {'sources': list(src['trackers'])}}
            if src['fileIdx'] is not None:
                body['fileIdx'] = src['fileIdx']
            if self.prime_resume and 0 < self.start_fraction < 1:
                body['startFraction'] = self.start_fraction
            threading.Thread(target=self._fire, args=(f"/{src['infoHash']}/create", body), daemon=True).start()
            self.ev('preload', infoHash=src['infoHash'])

    def dwell(self, sources, secs):
        """The time on the streams page: like useSmartPlay, sample the preloaded sources' download
        every 2 s."""
        end = time.perf_counter() + secs
        while True:
            for s in sources[:PRELOAD_SOURCES]:
                self.sample(s['infoHash'])
            left = end - time.perf_counter()
            if left <= 0:
                return
            time.sleep(min(DWELL_POLL, left))

    def _fire(self, path, body):
        try:
            self.req(path, body, timeout=120)
        except Exception:
            pass

    def remember_rates(self, sources):
        """race.ts rememberRates: each source's live rate / bitrate, for later plays."""
        if self.meter.memory is None:
            return
        for s in sources:
            rt, n = self.meter.rate(s['infoHash']), needed_rate(s, self.runtime)
            if rt is not None and n is not None:
                self.meter.memory.remember(s['infoHash'], rt / n)

    def _rate_fields(self, src):
        n = needed_rate(src, self.runtime)
        r = self.meter.rate(src['infoHash'])
        return dict(size=src.get('size'), neededMBs=round(n / 1e6, 2) if n else None,
                    rateMBs=round(r / 1e6, 2) if r is not None else None, keepsUp=keeps_up(r, n))

    def race(self, sources, capture):
        """Runs the race; returns (winner source, racers record); self.passed lists larger sources
        passed over. capture(ih) is called for each loser before it is removed."""
        self.passed = []
        if len(sources) <= 1:
            return sources[0], []
        cands = sources[:MAX_RACERS]
        sts = [self.sample(s['infoHash']) for s in cands]
        ready = [bool(st.get('files')) for st in sts]
        dwell = choose_keeping_up([s for s, r in zip(cands, ready) if r], self.meter, self.runtime, measured_only=True)
        if dwell is not None:
            best, self.passed = dwell
            self.remember_rates(cands)
            at = self.now()
            for s in sources:
                if s is not best:
                    capture(s['infoHash'])
                    try:
                        self.req(f"/{s['infoHash']}/remove")
                    except Exception:
                        pass
            rec = [dict(infoHash=s['infoHash'], fileIdx=s['fileIdx'], decided='dwell', peers=st.get('peers', 0),
                        metadata=bool(st.get('files')), done8MiB=at if s is best else None, winner=s is best,
                        **self._rate_fields(s)) for s, st in zip(cands, sts)]
            return best, rec
        queue = [s for s, r in zip(cands, ready) if r] + [s for s, r in zip(cands, ready) if not r]
        started = time.perf_counter()
        racers, lock, done = [], threading.Lock(), threading.Event()
        winner = []

        def win(r, how, passed=()):
            if not winner:
                r['done8'], r['how'] = self.now(), how
                winner.append(r)
                self.passed = list(passed)
                done.set()

        def larger_may_keep_up(src):
            n = needed_rate(src, self.runtime) or float('inf')
            return time.perf_counter() - started < LARGER_GRACE and any(
                (needed_rate(o['src'], self.runtime) or 0) > n and may_keep_up(known_rate_value(self.meter, o['src'], self.runtime), needed_rate(o['src'], self.runtime))
                for o in racers)

        def passed_over(src):
            """race.ts passedOver: larger racers known (live or remembered) not to keep up."""
            cn = needed_rate(src, self.runtime)
            out = []
            for o in racers:
                n, k = needed_rate(o['src'], self.runtime), known_rate(self.meter, o['src'], self.runtime)
                if n is not None and k is not None and cn is not None and n > cn and keeps_up(k[0], n) is False:
                    out.append(dict(infoHash=o['src']['infoHash'], size=o['src']['size'], rate=k[0], needed=n, remembered=k[1]))
            return out

        def alone_may_win(r):
            n = needed_rate(r['src'], self.runtime)
            if n is None:
                return True
            return keeps_up(self.meter.rate(r['src']['infoHash']), n) is True and not larger_may_keep_up(r['src'])

        def start(src):
            r = dict(src=src, read=0, started=self.now(), firstByte=None, metadata=None, done8=None, how=None, peers=0)
            racers.append(r)
            size = src.get('size')
            off = max(0, min(int(size * self.start_fraction), size - RACE_BYTES)) if size else 0
            threading.Thread(target=reader, args=(r, off), daemon=True).start()

        def reader(r, off):
            while not done.is_set():
                try:
                    hdr = {'Range': f"bytes={off + r['read']}-{off + RACE_BYTES - 1}"}
                    resp = urllib.request.urlopen(urllib.request.Request(self.server + self.stream_path(r['src']), headers=hdr), timeout=120)
                    while r['read'] < RACE_BYTES and not done.is_set():
                        b = resp.read(65536)
                        if not b:
                            break
                        with lock:
                            if r['firstByte'] is None:
                                r['firstByte'] = self.now()
                                r['firstAt'] = time.perf_counter()
                            r['read'] += len(b)
                            if r['read'] >= ALONE_WIN_BYTES:
                                el = max(time.perf_counter() - r['firstAt'], READ_RATE_FLOOR)
                                self.meter.add_read(r['src']['infoHash'], r['read'] / el)
                            if r['read'] >= ALONE_WIN_BYTES and all(o is r or o['read'] == 0 for o in racers) and alone_may_win(r):
                                win(r, 'alone', passed_over(r['src']))
                    resp.close()
                    if r['read'] >= RACE_BYTES:
                        # With a bitrate the rate decides (the poll loop); without, the first to 8 MiB.
                        if needed_rate(r['src'], self.runtime) is None:
                            with lock:
                                win(r, 'race')
                        return
                except Exception:
                    pass
                if done.wait(RETRY):
                    return

        for s in queue[:START_RACERS]:
            start(s)
        last_widen = started
        while not done.wait(POLL):
            now = time.perf_counter()
            with lock:
                with_data = [r for r in racers if r['read'] > 0]
                if with_data and now - started >= SETTLE_AFTER:
                    def score(r):
                        n, rt = needed_rate(r['src'], self.runtime), self.meter.rate(r['src']['infoHash'])
                        return rt / n if n and rt is not None else r['read']
                    win(max(with_data, key=score), 'settle')
                    break
            if not with_data and now - last_widen >= WIDEN_AFTER and len(racers) < len(queue):
                last_widen = now
                for s in queue[len(racers):len(racers) + WIDEN_BY]:
                    start(s)
            for r in list(racers):
                st = self.sample(r['src']['infoHash'])
                if r['metadata'] is None and st.get('files'):
                    r['metadata'] = self.now()
                r['peers'] = st.get('peers', 0)
            if self.runtime:
                with lock:
                    choice = choose_keeping_up([r['src'] for r in racers if r['read'] > 0], self.meter, self.runtime)
                    if choice is not None and not larger_may_keep_up(choice[0]):
                        win(next(r for r in racers if r['src'] is choice[0]), 'keepsup', passed_over(choice[0]))
        w = winner[0]
        self.remember_rates([r['src'] for r in racers])
        for r in racers:
            if r is not w:
                # Its read ends when the server closes the removed torrent's stream (closing the
                # response here would block on the reader thread's read).
                capture(r['src']['infoHash'])
                try:
                    self.req(f"/{r['src']['infoHash']}/remove")
                except Exception:
                    pass
        rec = [dict(infoHash=r['src']['infoHash'], fileIdx=r['src']['fileIdx'], started=r['started'], metadata=r['metadata'],
                    firstByte=r['firstByte'], done8MiB=r['done8'], decided=r['how'] or 'race', read=r['read'],
                    peers=r['peers'], winner=r is w, **self._rate_fields(r['src'])) for r in racers]
        return w['src'], rec
