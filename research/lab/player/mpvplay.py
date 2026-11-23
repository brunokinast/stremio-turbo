"""Measured play: drive the Stremio shell's libmpv against a Rivulet stream, then capture
Rivulet's diagnostics for that torrent.

usage: mpvplay.py <infoHash> <fileIdx> <label> [key=value ...]
  server=http://127.0.0.1:11470  secs=120  start=0 (seconds; resume with start=+N)
  remove=1 (remove the torrent and its cache after capturing, so the next play is cold)
  mpvconf=1 (apply %APPDATA%/Stremio/mpv.conf after the shell's options, as the shell does)
  hwdec=auto-copy  out=<dir> (default: research/data/play-<time>-<label>)
  trackers=<file> (one announce URL per line; default ../transport/trackers.txt)
  seek=AT:PCT (one seek, AT seconds into the run and after the first frame, to PCT % of the file;
  play.json gets seekRestartS, the seek's time to its playback-restart)
  switch=AT:IH:IDX (plan 11 step 5: AT seconds after the first frame, switch to another release of
  the same episode the way the app would, `loadfile <url> replace -1 start=+<time-pos>`; the
  second torrent is created first. play.json gets switch: the gap from the command to its
  playback-restart, the positions before and after, and both files' durations)
  dur=S (the episode's runtime): with race= it gives each source's bitrate (step 6e: the best
  picture that keeps up; without dur, the pre-6e first-to-arrive rules); with start=+N the race
  reads at N/dur of the file, as the app does at a resume, and the preload asks the server to prime
  there (resumeprime=0: it doesn't, the pre-6c behaviour)
  race=<addon response .json> tier=<quality label> limit=3 preload=S: the web client's smart play
  instead of a known source (smartplay_emul.py): preload the tier's first 3 sources, wait S
  seconds, race them, play the winner. <infoHash> <fileIdx> are then ignored ("-" "0").
  play.json gets playS (the race start, i.e. the click), race (per racer) and the winner.

As stremio-core does, the torrent is first created through POST /<infoHash>/create with its peer
sources (trackers and DHT), then the file is streamed.

The player uses the shell's own stream-lavf-o and mpv's default cache options (the shell sets
none), with vo=null and audio off. Like the web client, it calls /opensubHash once the file is
loaded. Nothing is polled on the server during the play; the capture happens after playback stops:
/debug/trace, /debug/dials, /debug/locks, /debug/status and stats.json, plus play.json (mpv
timeline: first frame, paused-for-cache intervals, cache duration every 0.5 s).
"""
import ctypes, json, os, sys, time, urllib.parse, urllib.request, threading

sys.stdout.reconfigure(encoding='utf-8')
if len(sys.argv) < 4:
    sys.exit(__doc__)
IH, IDX, LABEL = sys.argv[1].lower(), int(sys.argv[2]), sys.argv[3]
cfg = dict(server='http://127.0.0.1:11470', secs='120', start='0', remove='1', hwdec='auto-copy', out='', trackers='', seek='',
           race='', switch='', tier='4k DV | HDR', limit='3', preload='0', dur='0', resumeprime='1', startbuf='0', ratememory='', mpvconf='0')
for a in sys.argv[4:]:
    k, v = a.split('=', 1)
    cfg[k] = v
SERVER = cfg['server'].rstrip('/')
HERE = os.path.dirname(os.path.abspath(__file__))
OUT = cfg['out'] or os.path.join(HERE, '..', '..', 'data', time.strftime('play-%Y%m%d-%H%M%S-') + LABEL)
os.makedirs(OUT, exist_ok=True)
STREMIO = os.path.join(os.environ['LOCALAPPDATA'], 'Programs', 'Stremio')
# The shell's options (stremio-shell-ng player.rs); no cache options, so mpv's defaults apply.
LAVF = 'reconnect=1,reconnect_on_network_error=1,reconnect_on_http_error=%23%408,429,500,502,503,504,reconnect_delay_max=15'


def get(path, timeout=30, body=None):
    req = urllib.request.Request(SERVER + path, data=body, headers={'Content-Type': 'application/json'} if body else {})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read()


TRACKERS = cfg['trackers'] or os.path.join(HERE, '..', 'transport', 'trackers.txt')
with open(TRACKERS, encoding='utf-8') as f:
    sources = ['tracker:' + l.strip() for l in f if l.strip()] + ['dht:' + IH]


os.add_dll_directory(STREMIO)
m = ctypes.CDLL(os.path.join(STREMIO, 'libmpv-2.dll'))


class Ev(ctypes.Structure):
    _fields_ = [('id', ctypes.c_int), ('error', ctypes.c_int), ('ud', ctypes.c_uint64), ('data', ctypes.c_void_p)]


class LogMsg(ctypes.Structure):
    _fields_ = [('prefix', ctypes.c_char_p), ('level', ctypes.c_char_p), ('text', ctypes.c_char_p), ('ll', ctypes.c_int)]


m.mpv_create.restype = ctypes.c_void_p
m.mpv_wait_event.restype = ctypes.POINTER(Ev)
m.mpv_wait_event.argtypes = [ctypes.c_void_p, ctypes.c_double]
m.mpv_set_option_string.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_char_p]
m.mpv_initialize.argtypes = [ctypes.c_void_p]
m.mpv_command.argtypes = [ctypes.c_void_p, ctypes.POINTER(ctypes.c_char_p)]
m.mpv_get_property_string.restype = ctypes.c_void_p
m.mpv_get_property_string.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
m.mpv_free.argtypes = [ctypes.c_void_p]
m.mpv_set_property_string.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_char_p]
m.mpv_request_log_messages.argtypes = [ctypes.c_void_p, ctypes.c_char_p]
m.mpv_terminate_destroy.argtypes = [ctypes.c_void_p]

h = m.mpv_create()
for k, v in [('vo', 'null'), ('ao', 'null'), ('aid', 'no'), ('mute', 'yes'), ('hwdec', cfg['hwdec']),
             ('terminal', 'no'), ('idle', 'yes'), ('keep-open', 'no'), ('stream-lavf-o', LAVF)]:
    if m.mpv_set_option_string(h, k.encode(), v.encode()) < 0:
        sys.exit(f'mpv option failed: {k}={v}')
# mpvconf=1: the user's %APPDATA%\Stremio\mpv.conf, applied after the shell's options as the shell does.
if cfg['mpvconf'] == '1':
    for line in open(os.path.join(os.environ['APPDATA'], 'Stremio', 'mpv.conf'), encoding='utf-8'):
        line = line.split('#', 1)[0].strip()
        if '=' in line:
            k, v = (x.strip() for x in line.split('=', 1))
            if m.mpv_set_option_string(h, k.encode(), v.encode()) < 0:
                sys.exit(f'mpv.conf option failed: {k}={v}')
m.mpv_initialize(h)
m.mpv_request_log_messages(h, b'warn')


def prop(name):
    p = m.mpv_get_property_string(h, name.encode())
    if not p:
        return None
    s = ctypes.string_at(p).decode(errors='replace')
    m.mpv_free(p)
    return s


def num(name):
    v = prop(name)
    try:
        return float(v) if v is not None else None
    except ValueError:
        return None


start = int(cfg['start'])
wall0 = time.time()
T0 = time.perf_counter()
events, polls, logs = [], [], []


def ev(name, **kw):
    events.append(dict(t=round(time.perf_counter() - T0, 3), name=name, **kw))


def save_capture(ih, suffix):
    for name, path in [('trace', f'/debug/trace/{ih}'), ('dials', f'/debug/dials/{ih}')]:
        try:
            data = get(path)
        except Exception as e:
            data = f'capture failed: {e}'.encode()
        with open(os.path.join(OUT, f'{name}-{suffix}.json'), 'wb') as f:
            f.write(data)


play_at = race_rec = race_passed = hold_from = None
hold_s = playing_at = None
stream_path = f'/{IH}/{IDX}'
if cfg['race']:
    import smartplay_emul
    frac = start / float(cfg['dur']) if start and float(cfg['dur']) > 0 else 0.0
    runtime = float(cfg['dur']) if float(cfg['dur']) > 0 else None
    sp = smartplay_emul.SmartPlay(SERVER, T0, ev, start_fraction=frac, prime_resume=cfg['resumeprime'] == '1', runtime=runtime)
    # ratememory=<path>: the app's per-source rate memory (step 6h), kept in a JSON file across runs.
    sp.meter.memory = smartplay_emul.RateMemory(cfg['ratememory']) if cfg['ratememory'] else None
    ev('start-fraction', fraction=round(frac, 5), prime=cfg['resumeprime'] == '1')
    srcs = smartplay_emul.load_sources(cfg['race'], cfg['tier'], int(cfg['limit']))
    ev('sources', list=[s['infoHash'] for s in srcs])
    sp.preload(srcs)
    sp.dwell(srcs, float(cfg['preload']))
    play_at = round(time.perf_counter() - T0, 3)
    ev('play')
    win, race_rec = sp.race(srcs, lambda ih: save_capture(ih, ih[:8]))
    IH, IDX = win['infoHash'], win['fileIdx'] if win['fileIdx'] is not None else -1
    stream_path = sp.stream_path(win)
    race_passed = sp.passed
    ev('race-won', infoHash=IH, passed=race_passed)
    # Step 6f (stremio-web startBuffer.ts): hold on the first frame until 4 s is buffered when the
    # chosen source's rate is under 2x its bitrate (cap 20 s).
    w_rate, w_need = sp.meter.rate(IH), smartplay_emul.needed_rate(win, runtime)
    # startbuf=force holds regardless (checks the hold mechanics when the swarm happens to be fast).
    if cfg['startbuf'] == 'force' or (cfg['startbuf'] == '1' and smartplay_emul.should_hold(w_rate, w_need)):
        hold_from = 0.0
    ev('start-buffer', hold=hold_from is not None, rate=w_rate, needed=w_need)
url = SERVER + stream_path
args = [b'loadfile', url.encode()] + ([b'replace', b'-1', f'start=+{start}'.encode()] if start else [])
arr = (ctypes.c_char_p * (len(args) + 1))(*args, None)


def opensub_hash():
    # The web client asks for the hash as soon as the player has the file (subtitle lookup).
    t = time.perf_counter()
    try:
        body = get('/opensubHash?videoUrl=' + urllib.parse.quote(url, safe=''), timeout=120)
        ev('opensubHash-done', ms=round((time.perf_counter() - t) * 1000), body=body[:200].decode(errors='replace'))
    except Exception as e:  # recorded, not fatal
        ev('opensubHash-error', ms=round((time.perf_counter() - t) * 1000), error=str(e))


if not cfg['race']:
    t = time.perf_counter()
    get(f'/{IH}/create', timeout=120, body=json.dumps({'torrent': {'infoHash': IH}, 'peerSearch': {'sources': sources, 'min': 40, 'max': 200}}).encode())
    ev('create-done', ms=round((time.perf_counter() - t) * 1000))
ev('loadfile', url=url, start=start)
m.mpv_command(h, arr)
if hold_from is not None:
    m.mpv_set_property_string(h, b'pause', b'yes')
    hold_from = time.perf_counter() - T0
NAMES = {6: 'start-file', 7: 'end-file', 8: 'file-loaded', 20: 'seek', 21: 'playback-restart', 1: 'shutdown'}
deadline = T0 + float(cfg['secs'])
next_poll = 0.0
paused_since = None
pauses = []
first_frame = None
seek_at_s, seek_pct = (float(x) for x in cfg['seek'].split(':')) if cfg['seek'] else (None, None)
seek_issued = seek_restart = None
sw = None
if cfg['switch']:
    at, ih2, idx2 = cfg['switch'].split(':')
    sw = dict(at=float(at), infoHash=ih2.lower(), fileIdx=int(idx2))
    t = time.perf_counter()
    get(f"/{sw['infoHash']}/create", timeout=120, body=json.dumps({'torrent': {'infoHash': sw['infoHash']}, 'peerSearch': {'sources': ['dht:' + sw['infoHash']] + sources[:-1], 'min': 40, 'max': 200}}).encode())
    ev('switch-create-done', ms=round((time.perf_counter() - t) * 1000))
while time.perf_counter() < deadline:
    e = m.mpv_wait_event(h, 0.05).contents
    if e.id == 2:
        lm = ctypes.cast(e.data, ctypes.POINTER(LogMsg)).contents
        logs.append(dict(t=round(time.perf_counter() - T0, 3), prefix=lm.prefix.decode(errors='replace'),
                         level=lm.level.decode(errors='replace'), text=lm.text.decode(errors='replace').rstrip()[:300]))
    elif e.id in NAMES:
        ev(NAMES[e.id])
        if e.id == 8:
            threading.Thread(target=opensub_hash, daemon=True).start()
        if e.id == 21 and sw is not None and sw.get('issued') is not None and sw.get('restart') is None:
            sw['restart'] = round(time.perf_counter() - T0, 3)
            sw['gapS'] = round(sw['restart'] - sw['issued'], 3)
            sw['posAfter'] = num('time-pos')
            sw['durationAfter'] = num('duration')
            ev('switch-restart', gap=sw['gapS'], pos=sw['posAfter'])
        if e.id == 21 and first_frame is None:
            first_frame = time.perf_counter() - T0
        elif e.id == 21 and seek_issued is not None and seek_restart is None:
            seek_restart = time.perf_counter() - T0 - seek_issued
        if e.id == 7 and not (sw is not None and sw.get('issued') is not None and sw.get('restart') is None and not sw.get('ended')):
            break
        if e.id == 7:
            sw['ended'] = True
    now = time.perf_counter() - T0
    if sw is not None and sw.get('issued') is None and first_frame is not None and now >= first_frame + sw['at']:
        sw['posBefore'] = num('time-pos')
        sw['durationBefore'] = num('duration')
        url2 = f"{SERVER}/{sw['infoHash']}/{sw['fileIdx']}"
        sargs = [b'loadfile', url2.encode(), b'replace', b'-1', f"start=+{sw['posBefore']:.3f}".encode()]
        m.mpv_command(h, (ctypes.c_char_p * (len(sargs) + 1))(*sargs, None))
        sw['issued'] = round(now, 3)
        ev('switch-issued', url=url2, pos=sw['posBefore'])
    if seek_at_s is not None and seek_issued is None and first_frame is not None and now >= seek_at_s:
        sargs = [b'seek', f'{seek_pct:g}'.encode(), b'absolute-percent']
        m.mpv_command(h, (ctypes.c_char_p * (len(sargs) + 1))(*sargs, None))
        seek_issued = now
        ev('seek-issued', pct=seek_pct)
    if hold_from is not None and hold_s is None and now >= next_poll:
        if (num('demuxer-cache-duration') or 0) >= 4.0 or now - hold_from >= 20.0:
            m.mpv_set_property_string(h, b'pause', b'no')
            hold_s, playing_at = round(now - hold_from, 3), round(now, 3)
            ev('start-buffer-released', holdS=hold_s)
    if now >= next_poll:
        next_poll = now + 0.5
        pfc = prop('paused-for-cache') == 'yes'
        if pfc and paused_since is None and first_frame is not None:
            paused_since = now
        elif not pfc and paused_since is not None:
            pauses.append(dict(at=round(paused_since, 2), seconds=round(now - paused_since, 2), pos=num('time-pos')))
            paused_since = None
        polls.append(dict(t=round(now, 2), pos=num('time-pos'), cacheDur=num('demuxer-cache-duration'),
                          pausedForCache=pfc, fwBytes=num('demuxer-cache-state/fw-bytes'),
                          cacheSpeed=num('cache-speed')))
ended = time.perf_counter() - T0
if paused_since is not None:
    pauses.append(dict(at=round(paused_since, 2), seconds=round(ended - paused_since, 2), pos=num('time-pos'), open=True))
info = dict(mpv=prop('mpv-version'), ffmpeg=prop('ffmpeg-version'), duration=num('duration'),
            videoCodec=prop('video-codec'), hwdec=prop('hwdec-current'), width=num('width'), height=num('height'),
            gamma=prop('video-params/gamma'), primaries=prop('video-params/primaries'))
m.mpv_terminate_destroy(h)  # stops playback and closes the stream before the capture

play = dict(infoHash=IH, file=IDX, label=LABEL, start=start, secs=float(cfg['secs']), wallStart=wall0,
            firstFrameS=round(first_frame, 3) if first_frame is not None else None,
            seekIssuedS=round(seek_issued, 3) if seek_issued is not None else None,
            seekRestartS=round(seek_restart, 3) if seek_restart is not None else None,
            playS=play_at, holdS=hold_s, playingS=playing_at if playing_at is not None else (round(first_frame, 3) if first_frame is not None else None),
            race=race_rec, passed=race_passed, winner=IH if cfg['race'] else None, switch=sw,
            stallCount=len(pauses), stallSeconds=round(sum(p['seconds'] for p in pauses), 2),
            pauses=pauses, info=info, events=events, polls=polls, logs=logs)
with open(os.path.join(OUT, 'play.json'), 'w', encoding='utf-8') as f:
    json.dump(play, f, indent=1)

time.sleep(1)  # let the server close the reader
for name, path in [('trace.json', f'/debug/trace/{IH}'), ('dials.json', f'/debug/dials/{IH}'),
                   ('locks.json', '/debug/locks'), ('status.txt', '/debug/status'),
                   ('stats.json', f'/{IH}/{IDX}/stats.json')]:
    try:
        data = get(path)
    except Exception as e:
        data = f'capture failed: {e}'.encode()
    with open(os.path.join(OUT, name), 'wb') as f:
        f.write(data)
if cfg['remove'] == '1':
    get(f'/{IH}/remove')

ff = f'{first_frame:.1f}s' if first_frame is not None else 'none'
sk = f', seek {seek_restart:.2f}s' if seek_restart is not None else ''
if play_at is not None and first_frame is not None:
    if hold_s is not None:
        sk += f', held {hold_s:.1f}s, play->playing {playing_at - play_at:.1f}s'
    sk += f', play->frame {first_frame - play_at:.1f}s (race {[(r["infoHash"][:8], r["done8MiB"]) for r in race_rec]})'
print(f'{LABEL}: first frame {ff}{sk}, {len(pauses)} stalls / {play["stallSeconds"]}s, '
      f'{info["width"]}x{info["height"]} {info["videoCodec"]} {info["gamma"]} -> {os.path.abspath(OUT)}')
