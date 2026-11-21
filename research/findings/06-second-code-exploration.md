# Second code exploration (2026-11-16, read-only session)

Everything here comes from reading code and the recorded data in `data/`; nothing was run or changed. It adds to, and in places corrects, `findings/01–05` and `design/00`. Read §1 and §9 first.

Confidence marks: **[code]** read in the source (file:line given), **[data]** seen in a recorded dump, **[hyp]** a hypothesis that follows from code but has not been measured, **[verify]** a cheap check that would settle it.

Repos read: anacrolix fork (`Documents\git\anacrolix-torrent`), `github.com/anacrolix/go-utp@v0.0.0-20260908033909` (Go module cache), Rivulet (`Documents\git\rivulet-engine`), `@stremio/stremio-video` 0.0.103, `stremio-shell-ng` 5.0.27 (`main`, cloned to the session scratchpad `ref\stremio-shell-ng`).

---

## 1. The reframing: the reference swarm is one fast peer

**[data]** `data/qbt-peers-150s.json`: of everything qBittorrent downloaded in 150 s, **250.5 MB came from one peer, `peer-2`** (qBittorrent 5.1.4, **µTP**, 19% complete, 2.05 MB/s). The next three peers gave 1.9 MB, 1.1 MB and 1.0 MB in total (12–27 KiB/s each). 22 other listed peers gave nothing.

**[data]** `data/rivulet-status-150s.txt` (old build, 64 MiB cap): 5 connected peers, 2 seeds, 0 pending, 0 half-open. `peer-2` was connected over uTP but we were **never interested** in it (the 4 requestable pieces were not among its 514). The other fast seed, `peer-3` (uTP), served **956 chunks at 795 KiB/s and then choked us for 83 s**: 956 is one request batch (cap 963). We sent one batch, had nothing else requestable, sat idle on an unchoked seed, and lost the slot.

Consequences:
- "Supply ≈ demand (1.6–2.1 MB/s)" really means "one µTP peer at long distance can carry the stream". Peer count and dial speed matter for *finding* that peer early; after that, **the efficiency of one uTP connection and never idling on it decide everything**.
- Piece availability was 2–4 for the whole torrent. The swarm is tiny. Per-connection behaviour beats swarm-wide tuning here.
- Earlier notes say the same peer gave Rivulet ≤ 700 KiB/s (later runs) against 1.9–2.05 MB/s for qBittorrent. That gap is the most valuable unexplained number in the project. §4 lists what could cause it.
- Incoming connections work **[data]** (two peers with source `I` in the dump), so UPnP/firewall is fine for the installed exe.

---

## 2. What really happens between click and first frame

**[code]** `stremio-video/src/withStreamingServer/withStreamingServer.js:229–452`, `fetchVideoParams.js`, Rivulet `internal/media/media.go`, `internal/api/api.go:918`.

1. `convertStream` builds `http://127.0.0.1:11470/{infoHash}/{fileIdx}`.
2. **Probe, blocking**: `/hlsv2/probe` must return before mpv is told to load (`getPlayability`, `:262`). Rivulet runs `ffprobe -show_format -show_streams` with **default probe sizes** (`media.go:344`): libavformat reads up to 5 MB / 5 s of content to find stream info (more likely with ~30 subtitle tracks), and for Matroska it also seeks to every SeekHead target except Cues (Tags usually sit at the end of mkvmerge files) → **head 5–10 MB + a tail read** **[hyp for the exact bytes; verify with an access log]**.
   - The probe has a **hard 30 s timeout** and a failed result is **cached for 30 s** (`media.go:331, 386`). On today's cold start (45–115 s to first byte) the probe always fails first; the UI then falls back and loads mpv anyway. So ~30 s of every slow start is the probe timing out.
   - A successful probe is cached only 5 minutes, in memory.
3. mpv `loadfile`: reads the head again (now on disk), the tail (Tags; Cues when resuming or seeking), then the play position.
4. After the player loads: `/opensubHash` reads the **first and last 64 KiB** of the file (`media.go:679–707`) and `/subtitleSignature` runs when the probe showed subtitle tracks. Both go through the torrent reader.

Every one of those HTTP requests is a separate `engine.NewReader` (`api.go:958`). Every tail read today costs a whole 16 MiB piece.

**Byte budget for a resume on a cold torrent** (16 MiB pieces):

| | Today | With §9 items 1–3 |
|---|---|---|
| Probe head | 16 MiB (whole verified piece) | 1–2 MB (small probesize, blocks served as they arrive) |
| Tail (Tags, Cues, hash) | 16 MiB | 0.3–2 MB (only the chunks under the reads) |
| Resume position | 16–32 MiB | 2–4 MB |
| Total at ~2 MB/s | 48–64 MiB → 25–35 s after peers unchoke, plus the tail-first detour | 4–8 MB → 2–4 s |

---

## 3. Windows storage: three findings nobody was looking for

### 3.1 Cache files are not sparse → NTFS zero-fills on the first far write **[code] [verify]**
- anacrolix opens data files with plain `os.OpenFile(O_RDWR|O_CREATE)` (`storage/file-io-common.go:13–35`). There is no Windows-specific code: `storage/sys-lame.go` covers `wasm || windows`, and nothing sets `FSCTL_SET_SPARSE`.
- On NTFS a write beyond the file's valid data length forces the gap to be zeroed, synchronously, before the write returns. The first chunk of the **tail piece** of a fresh 6.7 GB episode is such a write: ~6.7 GB of zeros (seconds on NVMe, ~15 s on SATA SSD, most of a minute on a hard disk). libtorrent avoids this by creating sparse files.
- While that write blocks, the peer's read loop is stuck in it (`peerconn.go:1011–1015`), its uTP receive buffer fills, the advertised window closes and the sender backs off. Other writers to the same file wait too.
- Side effects: every episode start writes its full size in zeros (SSD wear, I/O contention), and the file's full size is allocated at once, so the cache budget (`cacheSize`, 10 GB default) sees ~7 GB per 4K episode after a few seconds of play and evicts other torrents early.
- Check: time a 1-byte write at offset 6 GB of a new file in the cache folder, with and without the sparse flag. Fix: fork `storage` — set the sparse flag at creation on Windows (and implement `seekData` with `FSCTL_QUERY_ALLOCATED_RANGES` so hashing skips holes).

### 3.2 fsync per completed piece under a client-wide mutex **[code]**
`MarkComplete` → `torrentCheckpointBuffer.queuePiece` → `checkpoint()` (at most once per second) → `classicFileIo.flush` → `f.Sync()` **while holding `classicFileIo.mu`** (`storage/file-torrent-checkpoint.go:49–100`, `file-io-classic.go:51–61`). `openForWrite` takes the same mutex for every chunk write, so all chunk writes of all torrents pause for the duration of each fsync. Small on NVMe; real on slow disks or with antivirus. A verified (non-responsive) read also waits for it.

### 3.3 Responsive reads wait for the whole piece to have no write in flight **[code]**
`storage.go:56, 87`: `ReadAt` calls `waitNoPendingWrites()` on the piece, not on the byte range (upstream TODO). With several peers writing 16-chunk batches into one 16 MiB piece, a reader can be held back while unrelated chunks are written. Negligible on a fast disk, a stutter source on a slow one. Fix belongs in the fork (wait per range).

---

## 4. uTP: how the one fast peer reaches us

**[code]**
- Rivulet builds with `CGO_ENABLED=0`, so `go-utp` selects the **pure-Go port** of libutp (`default_purego.go`), not the C++ library.
- One UDP socket carries **all uTP connections and the DHT**, read by **one goroutine**, every packet processed under **one mutex** (`purego/socket.go:238–278`); `Conn.Read`/`Write` take the same mutex.
- The OS receive buffer is never raised: `NewSocket` is `net.ListenPacket` and nothing calls `SetReadBuffer` (Windows default ≈ 64 KiB ≈ 45 packets ≈ 32 ms of a 2 MB/s flow). **Rivulet can fix this without a fork**: `ClientConfig.ListenPacket` (`config.go:109`, used at `client.go:445`) lets it create the UDP socket itself and set 4–8 MiB buffers.
- Receive window: 1 MiB per connection minus unread bytes (`conn.go:325`). Reorder buffer 1024 packets. **Selective ack covers only 30 packets** past the hole (`conn.go:429`), as in libutp; libtorrent's own receiver reports up to 256.
- Acks are batched on a 1 ms timer (`socket.go:20`).
- TCP and uTP are dialled at the same moment and **the first to connect wins** (`client.go:921–937`); the loser is closed. Which transport we get per peer is close to a coin toss.
- LEDBAT is a scavenger: on a seed's busy uplink its uTP flows yield to its TCP flows. A leecher connected over TCP gets a larger share of a busy seed than one over uTP.

**[hyp] Why we might get a third of qBittorrent's rate from the same uTP peer** (the peer is far away; from Brazil to Europe/Turkey RTT is ~230–280 ms, so 2 MB/s needs ~350 packets in flight):
1. Burst loss in the 64 KiB OS buffer whenever our reader goroutine is late by > 30 ms; with a 30-packet SACK horizon and 250 ms RTT, each loss burst costs several round trips or a retransmit timeout (window back to one packet).
2. The read loop blocked in a disk write (§3.1, §3.2) closes the receive window.
3. Gaps between request batches (§5.2) let the sender's pipeline run dry once per batch.
4. Not us at all: the peer was sharing its upload with others in that minute (swarm drift). The earlier A/B could not separate this.

**[verify] Diagnostics that explain the cause, in order of cost:**
- Windows counter `UDPv4\Datagrams Received Errors` before/after a run (buffer overflow shows here). No code.
- Per-connection uTP counters exposed in `/debug/status`: packets received out of order, duplicates, current advertised window, time spent with window < 100 KiB. Needs a small go-utp fork (`purego/conn.go`); a fork is justified because this transport is the whole stream.
- Time spent by each peer's read loop inside `write()` (storage) per second.
- Sequential A/B on the same peer: uTP vs TCP (dial TCP only), and 64 KiB vs 8 MiB socket buffer.

If the pure-Go port is the limit, options are: fix it in a go-utp fork (bigger SACK, drain loop, buffers), or prefer TCP whenever the peer is reachable by TCP (delay the uTP dial by ~300 ms in `dialAndCompleteHandshake`; fork, ~10 lines).

---

## 5. Request model: exact behaviour, with the changes it implies

All **[code]** in the fork (`requesting.go`, `peerconn.go`, `torrent.go`, `internal/request-strategy/order.go`).

### 5.1 A seek or priority change only re-plans idle peers
`updatePeerRequestsForPiece` (`torrent.go:1666–1691`) triggers a request update only for peers with **zero** outstanding requests (`isLowOnRequests`, `peerconn.go:2104`). Priority drops never cancel anything (`cancelRequestsForPiece` is called only from `onPieceCompleted`, `torrent.go:2898`). So after a seek, every peer first finishes its old queue: up to 963 requests ≈ 15 MiB ≈ 20 s on a 800 KiB/s peer upstream; ≈ 3 s with our rate cap. mpv's start is three jumps (head → tail → position), each paying this.

A reader's window also does not exist until its first `Read` (`reader.go:161`: `if !r.reading { ra = 0 }`), and a seek inside the same piece changes nothing at all (no event, chunk order unchanged).

### 5.2 Refill happens only when the queue is empty
Updates after a received chunk fire only when the peer's queue is empty (`peerconn.go:2131`). The peer's send pipeline therefore drains completely once per batch and restarts one round trip later. Cost ≈ RTT ÷ batch duration: ~1% with 963-request batches, **~8% with the 3 s cap in `request-health.go` at 250 ms RTT**. The cap fixed hoarding but made this worse; the two must ship together with a low-water refill.

`peakRequests` is "requests added in the last update" (`requesting.go:482–486`) and the limit is `2 × peakRequests`: a doubling slow start that only works with batch-and-drain. With low-water refill it stops growing, so queue sizing must become rate-based (libtorrent: queue = rate × time, +1 per block while ramping).

### 5.3 Idle on an unchoked seed = losing the slot **[data + code]**
§1's 956-chunk episode. qBittorrent seeds default to the "fastest upload" choker; a peer that stops pulling ranks last. Rule for the scheduler: **an unchoking peer always has a non-empty queue**, even if the only work left is far ahead of the playhead.

### 5.4 Order inside the reader's piece ignores the reader's offset
Candidates are collected from chunk 0 upward (`iterUndirtiedRequestIndexesInPiece`, `torrent.go:3545`) and the final tie-break is the request index (`requesting.go:207`). A read at the end of a 16 MiB piece (Tags, Cues, the 64 KiB hash window, a mid-piece resume) waits for the whole piece in order, even with responsive reads.

### 5.5 Availability outranks index in two places
`pieceOrderLess` (`order.go:24–42`): priority → partial → **availability** → index. `lessByValue` (`requesting.go:196–206`): for Readahead pieces availability → index; for Now and Normal availability → **random** (`t.pieceRequestOrder`). Candidate collection stops at `max(2 × chunks per piece, 4 × queue)` (`requesting.go:330`), i.e. about two 16 MiB pieces in *that* order. In a swarm with partial peers the window is not fetched in playback order, and the rest of the file (Normal, from `File.Download()`) is fetched rarest-then-random.

### 5.6 Other facts worth keeping
- Stealing (`requesting.go:445–468`) balances request *counts* and compares last-chunk times; it knows nothing about rates or deadlines. Upstream added `ClientConfig.StealRequestGrace`.
- Choke from a fast-extension peer keeps our requests until it rejects them (`peerconn.go:1089–1103`); nothing times out upstream. Our `request-health.go` snub (20 s) is the only timeout.
- A responsive reader is woken per chunk (`piece.go:175–179`). Chunks are marked "have" before they are written (`peer.go:451`); readers are protected only by §3.3's wait.
- Chunk writes are synchronous in each peer's read loop, in batches of ≤ 16, with the client lock released (`peerconn.go:1000–1023`).
- Tracker `numwant` is 200 while fewer than 50 addresses are in reserve, else **0** (`torrent.go:2420`). Fine.

---

## 6. Rivulet: policy and settings

**[code]** `internal/engine/engine.go`, `internal/app/app.go`, `internal/api/api.go`.

- **`btMaxConnections` (the user set 200), `btHandshakeTimeout`, `btRequestTimeout` are stored and validated but never reach the engine.** The engine uses `RIVULET_PEERS_PER_TORRENT` → 50 established / 25 half-open / 500 reserve (`engine.go:376`, `peerBudget`).
- **The soft speed limit is not a throttle** (`engine.go:928–989`): above the limit *and* with ≥ `btMinPeersForStable` peers it clamps the torrent's connection cap to the current count (stops adding peers). The user's 4 MiB/s never triggered. The **default hard limit is 3.5 MiB/s** (`settings.go:86`), under 2× a 4K bitrate; fine for the user (37.5 MiB/s) but a poor default for building a buffer.
- Dial limits are anacrolix defaults (`config.go:328–356`): 20 s nominal / 3 s minimum dial timeout, 25 half-open per torrent, 100 total, 10 dials/s (the rate-limiter sleep happens *inside* a half-open slot, `client.go:910–917`), 4 s handshake timeout. All settable from Rivulet.
- **Closing the last reader of a file drops the file to priority None** (`engine.go:1846–1868`). mpv/ffmpeg close and reopen the HTTP connection on every long seek and on reconnect, ffprobe closes after the probe, and the probe's 30 s timeout closes it too. Each time: 399 piece priorities drop and are raised again by the next `NewReader`. A grace period (keep the file wanted for ~60 s after the last reader) removes the flapping and is the first step towards download intents.
- Readahead is 2 s of measured speed, clamped 8–64 MiB (`engine.go:1678`): one to two pieces. Everything past it is the Normal-priority whole-file download in rarest/random order (§5.5).
- `NewReader` → `warmMoov` parks a second reader on the last 8 MiB and `primeBoundary` pins head and tail at Now (`engine.go:1449–1452, 2435`). With §5.4/§5.5 this is the tail-before-head detour (E2).
- `startupReadTimeout` 90 s (`engine.go:1653`); mpv's own `network-timeout` default is 60 s, after which ffmpeg reconnects (new reader, see flapping above).
- UPnP port mapping is on (anacrolix default), the listen port is random per start (`BT_LISTEN_PORT=0`).
- `diskMaxUnverifiedBytes` comment is still wrong (per torrent on disk, not client-wide).

---

## 7. Player side

- **The official shell loads a user mpv config** **[code]**: `stremio-shell-ng/src/stremio_app/mpv_config.rs` reads a *path* from `%APPDATA%\Stremio\mpv-config.txt`; `player.rs:462` calls `load_config(path)` when mpv is created, after the shell's own options. Our web fork already shows the picker (Settings → Device, when the shell reports the capability; `src/routes/Settings/Device/useMpvConfig.ts`). **No shell fork is needed for cache options**, which reverses `findings/03` and `design/00` §5.7. [verify] that the installed shell is new enough (the Settings row is visible).
  Starting point for that file:
  ```
  cache=yes
  cache-pause-initial=yes     # buffer before the first frame instead of starting on 1 s
  cache-pause-wait=8          # after an underrun, resume with 8 s buffered, not 1 s
  demuxer-max-bytes=1GiB      # ~8 min of 4K ahead; RAM unless cache-on-disk=yes
  demuxer-max-back-bytes=256MiB
  network-timeout=180         # do not reconnect (and drop the reader) after 60 s of stall
  ```
- The runtime allowlist is unchanged (`communication.rs:158–226`): `pause`, `time-pos`, `speed`, track and subtitle properties; `paused-for-cache`, `cache-buffering-state`, `demuxer-cache-time` are readable. No cache property can be set at runtime, so an *adaptive* policy still has to be driven with `pause` from the web UI, or done on the server (§8.9).
- The static config removes the worst case (play 1 s, stall, play 1 s) today, with zero code.

---

## 8. Improvements, including the ones that are easy to miss

Ordered by expected effect on the two goals (start fast; no stutter). Forks are named where they give the better result.

1. **Fetch only what a read needs** (anacrolix fork + Rivulet): responsive reads on; chunk order in a reader's piece starts at the reader's offset (rotate); the readahead of a ranged HTTP request is capped to the range length; a seek re-plans all peers. Turns each tail read from 16 MiB into a few hundred KiB.
2. **Head first, tail on demand** (Rivulet): head at Now, no tail pre-read; the tail is raised by whoever actually reads it (probe, mpv, hash).
3. **Cheaper, earlier probe** (Rivulet + web): `-probesize`/`-analyzeduration` ~1–2 MB/1 s for the playability probe; probe timeout tied to the stream's startup deadline instead of a fixed 30 s, no caching of timeouts; persist successful probe results per (info hash, file) instead of 5 minutes in memory; **the smart-play preload calls the probe and the subtitle hash for the top source while the user is still on the details page**, so head, tail, metadata and probe are all warm at click time.
4. **Sparse cache files on Windows** (anacrolix fork, §3.1).
5. **Never idle an unchoking peer; short queues with low-water refill; rate-based sizing** (fork, §5.2–5.3). Replaces batch-and-drain. Queue ≈ 1–2 s of the peer's rate so a seek takes effect within that time.
6. **Playback order by index** inside the reader window and for the rest of the played file (fork, §5.5), with a readahead window sized in seconds of bitrate. This is what qBittorrent's sequential mode did.
7. **uTP receive path** (§4): socket buffers through `ListenPacket` now; counters; then either fix the pure-Go port in a go-utp fork or prefer TCP where reachable.
8. **mpv config** (§7) today; adaptive start/resume ("ready in N s", "download first") later from the web UI with server data.
9. **Server-side rebuffer hysteresis** (Rivulet, works for every player, no UI): when the stream's reader has caught up with the download frontier and the rate is below the bitrate, withhold the HTTP body until N seconds are contiguous on disk, then release in one burst. The player sees one longer pause instead of many 1 s ones. Complements the mpv config for players we do not control (casting, external players).
10. **Resume-aware preload** (web + Rivulet): the web UI knows the resume time before the click. Send it with `/create`; the server reads the MKV Cues once (a few hundred KiB at the tail), maps time → byte offset, and prefetches the head, the Cues and the first seconds at the resume point. Resume then needs no swarm round trip after the click. The same Cues map gives exact buffered seconds and exact deadlines for variable-bitrate 4K instead of an average bitrate.
11. **Download intent with a grace period** (Rivulet, §6): the file stays wanted for a while after the last reader closes; later "keep downloading this episode" and "fetch the next one".
12. **Wire `btMaxConnections`**, raise dial limits (100 half-open per torrent, 50 dials/s, 8 s nominal timeout), and remember productive peers per info hash so the one peer that matters is dialled in the first second next time (anacrolix dials `Trusted` peers first).
13. **Source choice that knows about piece size and swarm supply** (web smart play): prefer the source whose measured first seconds of supply are best; with item 1 piece size matters less, but a single-episode torrent with 4 MiB pieces still verifies and hashes faster than a 16 MiB season pack. After items 1–3, racing several torrents mostly splits the dial budget; preload one, race on failure.
14. **Per-range pending-write wait and fsync outside the global mutex** (fork, §3.2–3.3).
15. Later: merged swarms (byte-identical file in another torrent), reciprocity credit, timeouts with duplicate requests near the playhead, the full block-deadline scheduler of `design/00` §4.

---

## 9. Revised order of work

Steps 0–2 need no new design and no lab; each has a one-run check that explains itself.

0. **Instrument first** (one build, one muted 4K HDR play, nothing else changed):
   - HTTP access log in Rivulet: method, Range, bytes sent, duration, first-byte time per request (settles exactly what ffprobe, mpv and the hash read, and in which order).
   - Per peer: transport, read-loop time spent in storage writes.
   - UDP receive-error counter before/after; time of the first write to each file and how long it took (zero-fill).
1. **Settings and config only**: mpv config file; UDP socket buffers via `ListenPacket`; wire `btMaxConnections`; dial limits; reader-close grace; probe flags and timeout; persisted probe cache.
2. **Fork, small and independent changes** (each verifiable with step 0's log): sparse files; offset-first chunk order; re-plan on seek; index before availability for reader priorities; responsive reads on in Rivulet; head-first/tail-on-demand.
3. **Preload does the probe and hash; resume-aware preload.**
4. **Request model**: rate-based queue with low-water refill, never-idle rule, timeouts; then the deadline scheduler. The lab swarm (`design/00` §6) is the judge here, with one scripted "single fast far peer" scenario added, since that is the real swarm.
5. uTP: act on step 0's numbers (fix the Go port or prefer TCP).
6. Buffer UX, download intents, next-episode prefetch, peer memory, merged swarms.

Land the already-proven uncommitted work (partial-piece cap, `PrimeFile`, endpoints, dial log, hints, trackers) before step 1, as `design/00` §7.1 says.

---

## 10. Corrections to earlier documents

- `findings/05` E9/E10 and `design/00` §1: qBittorrent's rate was one uTP peer, not "the swarm" (§1).
- `findings/05` E5: over-stated for run `ab-1116-033418` (the slow seed was the only peer serving piece 0 there); the real head-of-line evidence is `data/diag2.txt`.
- `findings/03` and `design/00` §5.7: mpv cache options do **not** need a shell fork (§7).
- `findings/03`: "check whether the soft limit throttles" → it does not (§6).
- `design/00` §2: add "Windows zero-fill on the tail write" and "idle on an unchoked seed" as causes; the "~40 s dial funnel" matters less on this swarm than finding and holding one peer.
- `request-health.go`: its rate cap adds a per-batch idle gap (§5.2); keep it off by default until the low-water refill exists. Minimum queue should be 1 for very slow peers.
- `STATUS.md`: "64 MiB default: 4 pieces client-wide" → per torrent on disk.

## 10a. Checked on the user's machine after writing (2026-11-16)

- Installed shell: `%LOCALAPPDATA%\Programs\Stremio\stremio-shell-ng.exe`, version **5.0.27**, the same version as the source read here, so the mpv-config feature is present. `%APPDATA%\Stremio\mpv-config.txt` does not exist yet: no custom mpv config is in use.
- The only disk is a **Kingston NV2 1 TB NVMe SSD**; the cache (`%LOCALAPPDATA%\rivulet-engine`) is on it. The zero-fill of §3.1 should therefore take a few seconds per episode, not tens (the NV2 is DRAM-less, so sustained writes can drop well below its peak). It is a stall at start and ~7 GB of wasted writes per episode, not the main cause of slow starts. Still to be timed.

## 11. Open questions

- Exact bytes ffprobe and mpv read at a fresh start and at a resume (step 0's access log).
- Whether the uTP gap is ours (buffer, SACK, write stalls, batch gaps) or the peer's load (§4 diagnostics).
- How long the zero-fill takes on the user's cache disk (NVMe, see §10a; expected seconds).
- Whether `peer-2`-type peers are reachable over TCP.
