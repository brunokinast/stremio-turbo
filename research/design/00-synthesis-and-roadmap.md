# Synthesis and roadmap: fast, low-buffering 4K HDR streaming

> **Superseded roadmap.** The ordered path is now `plans/00-START-HERE.md`. Still valid here: the engine decision (§3) and the deadline-scheduler design (§4, target of `plans/09-later.md`). §6's live-swarm and lab-swarm ideas were replaced by the uTP transport lab (`findings/10`).

Read after `findings/01–05`. This is the reasoning and the ordered plan; each roadmap step is sized so a less capable model can execute it from a short plan with the acceptance test given.

## 1. The goal in numbers
- Reference episode: ≈ 6.7 GB over ≈ 58 min → **≈ 1.9 MB/s** to play in real time.
- The reference swarm gave qBittorrent **1.6–2.1 MB/s**. Supply ≈ demand.
- So there are two different problems:
  1. **Start-up** (click → first frame): today 45–115 s; qBittorrent's equivalent is ~18 s; the floor with what is designed below is a few seconds after the first peer unchokes.
  2. **Sustained playback**: on a swarm at ~1× bitrate *no* client can stream without stalls unless it (a) wastes nothing, (b) gets more supply, or (c) starts with a buffer. The honest product answer includes "ready in N minutes" and "download first".
- Metrics to judge everything by: time to first frame; time to 30 s of buffer; rebuffer count and seconds per hour; fraction of downloaded bytes that were in playback order (waste); download rate ÷ bitrate.

## 2. Why Rivulet was slow (ranked by measured effect)
| Cause | Where | Fix class |
|---|---|---|
| Tail piece fetched before head (E2) | Rivulet priming + anacrolix rarest-first tie-break | Rivulet policy (1 line) |
| Reads wait for a whole verified 16 MiB piece (E3) | Rivulet never enables responsive reads | Rivulet policy |
| Earliest blocks stuck on a slow peer (E5); one peer hoards a piece (E6) | anacrolix request model: batch-drain-double, count-based stealing, no timeout | fork: scheduler |
| ~40 s to work through the address list (E7) | anacrolix defaults: 25 half-open, 10 dials/s client-wide, 4–20 s timeouts, random order | Rivulet config + peer memory |
| Only 4 pieces requestable (E1) | anacrolix default | Rivulet config (done, uncommitted) |
| Rest of the file fetched rarest/random, not in order | anacrolix ordering; Rivulet's sub-piece readahead | Rivulet policy + small fork change |
| Wrong episode primed (E8) | Rivulet guess | done, uncommitted |

qBittorrent's lead did **not** come from libtorrent's streaming mode (qBittorrent doesn't use it). It came from head-first sequential order, fast dialling, short queues that refill continuously, and adaptive timeouts. All of that is reachable from where we are.

## 3. Engine decision: stay on anacrolix, port libtorrent's ideas
- Most of the gap is Rivulet policy and anacrolix *defaults*; the rest is a ~500-line request module (`requesting.go`) we now own in the fork.
- anacrolix has one thing libtorrent cannot offer: **responsive reads**. libtorrent only exposes hash-verified pieces, so with 16 MiB pieces its floor to first frame is "download 16 MiB" (≈ 8 s at full swarm speed, 18 s measured). Serving blocks as they arrive puts our floor at the first few hundred KiB. For large-piece 4K packs this is a structural advantage.
- A libtorrent switch would cost the engine layer (≈ 6,800 lines across 10 files), the static cgo-free build, and easy upstream merges, to buy a scheduler we can write.
- Revisit only if the lab (§6) shows the fork cannot reach parity with libtorrent's request behaviour after roadmap step 4. A libtorrent sidecar spike is the fallback experiment, not the plan.

## 4. The design that is better than both: a deadline scheduler over blocks
libtorrent's time-critical mode gives each urgent block to the peer that will deliver it soonest and drops slow peers from urgent work. With responsive reads the unit that matters is the **block**, and slow peers are still useful if they are given work with far deadlines. The scheduler:

- **Inputs**: player read position; bitrate (file size ÷ duration from the probe or Stremio metadata; fallback: a size-class estimate); per-peer rate as an **EWMA** over ~5 s (peak rate if idle > 30 s; swarm mean for a peer unchoked < 5 s ago, as libtorrent does); per-peer response time mean and deviation.
- **Urgent window**: blocks from the read position up to the buffer target (§5), each with deadline = now + (buffered seconds before that block).
- **Assignment** (every 250–500 ms and on unchoke/chunk/seek): walk blocks in deadline order; give each to the peer with the smallest ETA = (bytes outstanding + block) ÷ rate + response time, **provided ETA × 2 < time to deadline**; otherwise the block waits for a faster peer. A slow peer therefore naturally takes blocks far ahead of the playhead instead of the next one (this is the part neither engine has; it removes E5 without wasting the slow peer).
- **Queue depth**: ≤ 2–3 s of each peer's rate, refilled at a low-water mark (half), never batch-and-drain. Every unchoking peer always has ≥ 3 s of work: libtorrent seeds keep a slot for whoever pulls fastest (findings/01 §8).
- **Late blocks**: per-peer adaptive timeout = mean + 4 × deviation of its response time, clamped 2–60 s. On expiry the block is offered to another peer as a **duplicate without cancelling** the original (libtorrent's snub semantics); at most one duplicate per block per timeout period. A block inside the last ~2 s before its deadline is duplicated immediately if a faster peer is free.
- **Seek**: re-plan everything; unsent work is free to move. This needs a local unsent queue per peer (libtorrent's `m_request_queue`), i.e. only ~1 s of requests on the wire and the rest held locally.
- **Outside the window**: the rest of the file in index order (not rarest-first) while the torrent has ≥ 2 seeds; then the next episode (§5).
- **Fit in anacrolix**: a new `streaming-scheduler.go` in the fork that owns request assignment for pieces under reader priorities and leaves the existing code for everything else; behind `ClientConfig` so upstream can take it.

## 5. Supply and time levers (the sustained-playback half)
**Supply**
1. **Fast, ordered dialling**: 100 half-open per torrent, 500 total, 50 dials/s, dial timeout 8 s / floor 3 s (all `ClientConfig`, set from Rivulet). Order: scrape hints and **remembered good peers** first (anacrolix dials `Trusted` peers first).
2. **Peer memory**: persist, per info hash, addresses that delivered data (with rate and last-seen). A season pack is watched for weeks; the next episode should connect to last night's productive peers in the first second.
3. **Merged swarms**: other torrents in the same quality tier often carry the byte-identical file (single-episode release vs season pack, same group). Match by exact size + name, prove by hashing a piece-aligned range fetched from torrent B against torrent A's piece hashes, then treat B's peers as extra sources for A's byte ranges. Largest supply gain, largest design; do it after the scheduler.
4. **Reciprocity**: libtorrent leechers unchoke whoever uploads to them. anacrolix stops at 100 KiB of credit. Raise the credit (several MiB) for peers that have pieces we want. Cheap to try in the lab and live.

**Time**
5. **Buffer policy owned by us**, enforced from the web UI with `pause` (the shell allows it) using server data: start and resume only when the buffer covers the predicted shortfall. If rate ≥ bitrate: start at ~10 s of buffer. If rate < bitrate: needed buffer = (bitrate − rate) × remaining duration; show "ready in N min" or offer "download first". This replaces mpv's default (resume after 1 s buffered), which the web UI cannot change (findings/03).
6. **Download intents** in Rivulet, independent of open readers: "keep this file", "fetch next episode", "download for later", persisted and resumed. Next-episode prefetch runs when rate > bitrate or playback is paused/ended.
7. Optional: fork `stremio-shell-ng` only to add mpv cache options (`cache-pause-wait`, `demuxer-max-bytes`) if step 5 is not enough.

## 6. Measurement (replaces the approved "swarmbench" plan)
Live side-by-side variants are invalid (findings/05): same-IP instances peer with each other and libtorrent peers accept one connection per IP.
- **Lab swarm (primary)**: a Go program in the Rivulet repo (`bench/labswarm`, own module) that serves a synthetic torrent (16 MiB pieces, generated data) from **scripted peers on loopback**: each with a rate, an unchoke delay, choke cycles, a bitfield (seed / partial / "has only the tail"), stalls after N blocks, rejects, dead addresses that time out. Scenarios reproduce E2, E5, E6, E7 and a "1× bitrate swarm". A **virtual player** reads at the bitrate with mpv's buffer rules and reports the metrics of §1. Deterministic, seconds to minutes per scenario, loopback only (no firewall prompts), runs in CI.
- **Live (validation)**: one variant at a time, interleaved A B A B…, ≥ 5 runs each, medians; fresh cache; the user's real server settings; own public and LAN addresses blocked as peers. Mechanism checks (which piece is requested first, queue sizes, dial funnel) from `/debug/status` and `/debug/dials` are valid in a single run.
- The existing diagnostics (`/debug/status`, `/debug/dials/{ih}`, `/{ih}/peers`, `data/stream-diag.py`, `data/ab.py`) stay as the live tools.

## 7. Roadmap
Each step: scope → acceptance. Steps 1–2 are safe and independent; 3 gates 4.

1. **Land what is proven** (Rivulet, fork): commit the partial-piece cap (fix its comment: the cap is per torrent on disk, shared only for the RAM cache), `PrimeFile`/`fileIdx`, real peers endpoint, debug endpoints, dial log + `OutgoingConnResult` callback, peer hints, tracker cap 25. Keep `request-health.go` but default it **off** (`RequestQueueTime = 0`, `PieceTimeout = 0`) until the lab judges it. Point `go.mod` at the pushed fork. → suites pass; installed build runs S06E01.
2. **Start-up policy in Rivulet** (replace the two `RIVULET_EXPERIMENT_*` env gates in `engine.go` with real settings):
   a. head at Now, tail at High, no tail pre-read; the player's own seek raises the tail when it needs it;
   b. responsive reads on for pieces ≥ 4 MiB (setting, default on);
   c. dial limits of §5.1;
   d. readahead = max(60 s of bitrate, 4 pieces) once the bitrate is known, else 4 pieces.
   → live mechanism check: first requests on piece 0; first byte < 15 s on the reference; mpv starts (verify with a real 4K HDR play, muted, that MKV start and resume still work with the tail at High).
3. **Lab swarm + virtual player** (§6). → the current build reproduces E5 (head-of-line stall) and E7 (dial delay) in the lab; results are identical across 3 runs.
4. **Fork: request model**, one change at a time, each kept only if the lab improves:
   a. index before availability for reader priorities;
   b. refill at low-water mark instead of empty;
   c. EWMA rate;
   d. adaptive timeout + duplicate without cancel;
   e. the deadline scheduler of §4 (subsumes b–d for the urgent window).
   → lab: first frame and rebuffer metrics at parity or better than a libtorrent reference peer set; no regression in the fork's own tests.
5. **Buffer policy + honest UI** (Rivulet endpoint: contiguous bytes ahead of a position, rate, bitrate; stremio-web: gate start/resume, "ready in N min", "download first"). → on the lab's 0.8× swarm, zero stalls after start, and the predicted wait is within 20% of actual.
6. **Download intents, next-episode prefetch, "download for later"** (Rivulet + UI with previews of the options, per the UI rule).
7. **Peer memory** (persisted good peers, dialled first).
8. **Merged swarms** (own design document first).
9. Optional experiments: reciprocity credit; shell fork for mpv cache options; libtorrent sidecar spike only if step 4 fails its bar.

Also pending from before tonight: stremio-web smart play (race, preload, panel) is uncommitted and untested in the app. After step 2 the race needs re-thinking: with a few-second first byte, racing 3–7 torrents mostly dilutes the shared dial and unverified budgets; a better default is preload the top source and race only on failure.
