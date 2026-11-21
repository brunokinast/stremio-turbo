# 15 — Short queues and critical chunks (step 5c)

Date: 2026-11-18.

Commits (local, nothing pushed):
- **anacrolix `c8a450e8`:**
  - `RequestQueueRTTs`, `RequestQueueMinTime`, `PeerConnRTT`;
  - `ReaderCriticalBytes`;
  - the callbacks `ReaderWaited` and `CriticalRequestStolen`;
  - `PeerRequestStats.RTTEstimate` and `QueueTime`.
- **rivulet-engine `7be4cfe`:**
  - sets 4 round trips, a 0.5 s floor and 1 MiB critical;
  - `connRTT` reads uTP's RTT, or the kernel's smoothed TCP RTT (`SIO_TCP_INFO` on Windows, `TCP_INFO` on Linux);
  - the trace gains `readerWaits` and `criticalSteals`, and the peer samples gain `queueMs` and `rttMs`.

Lab: `run-step5c-lab.sh` (old settings), `run-step5c-lab2.sh` (new); data in `data/transport-lab/Q-*.txt`. Play: `data/play-20261118-125826-hotd-step5c-cold`.

## Correction to findings/14
- findings/14 called a median of 1.6 s "urgent-chunk latency". It counted every chunk of every `Now` piece: all 1024 chunks of the 16 MiB piece the player is in, most of them not needed yet. So it measured queue depth, not what the player waited for.
- The step 5a samples gave the real lever:

| Peer | Rate | Queue |
|---|---|---|
| µTorrent | 6.2 MiB/s | 0.33 s (its own 255-request cap) |
| libtorrent peers | 4.5–6 MiB/s | 1.2–2.9 s |

- Every newly urgent chunk (a seek, mpv's Cues) lands behind that queue.
- The per-piece instrument is replaced by `readerWaits`: one entry per blocked read, recording who holds the chunk, how many requests are ahead of it, and the expected delivery on the holder and on the best other peer.

## What changed
- **Queue by round trip.** The queue covers 4 transport round trips (at least 0.5 s, at most 3 s) instead of 3 s of rate. With refill at half, the sender only idles below 2 round trips.
- **Critical chunks.** The missing chunks in the 1 MiB after a blocked reader's position:
  - sort first;
  - may exceed a peer's request limit by 16;
  - move to a peer expected to deliver them at least 2× and 200 ms sooner, estimated from requests ahead ÷ recent rate.
  - A blocked read makes peers replan only when its chunk is unrequested or ≥ 200 ms back in its holder's queue. Otherwise a reader at the download frontier, which blocks many times a second, would replan every peer each time.

## The first RTT estimator failed in the lab, and was replaced
- **The plan's estimator:** a request's latency minus the time the peer spent sending what was queued ahead of it (bytes received meanwhile ÷ recent rate), taking a low percentile.
- **What happened in the lab:**
  - it settled at **1 ms** on a 300 ms path;
  - the queue fell to the 0.5 s floor, under two round trips, and the sender idled;
  - since the limit is rate × queue time, it shrank with the rate: 500 → 13 requests and **38.6 MiB instead of 83 MiB** in 30 s (`Q-*-sampledrtt.txt`).
- **Why:** with seconds of queue, the sample is the difference of two large numbers, and the 5 s rate lags.
- **The fix:** the round trip now comes from the transport through the `PeerConnRTT` hook. In the lab that gave 303 ms on the 300 ms path; in the play, 217–375 ms on real peers.

## Lab: one peer, 150 ms each way
| | Old (3 s queue) | New (4 RTT) |
|---|---|---|
| 30 s download | 83.2 MiB | **82.0 MiB** |
| Seek runs (download) | 82.6, 81.8 MiB | 81.9, 82.0 MiB |
| Average limit / queue time | 477 requests / 3.0 s | **266 / 1.21 s** |
| Minimum outstanding after 3 s | 40–71 | 32–41 (never ≤ 1) |
| **Seek: 256 KiB at 120 MiB, after closing the head reader** | 1.30, 2.22 s (mean 1.76) | **1.00, 0.72 s (mean 0.86, 49 %)** |

Pass: throughput ≥ 95 % ✓, seek ≤ 60 % of old ✓ and ≤ 1.5 s ✓.

## Real play: House of the Dragon S02E01, cold, seek to 50 % at 60 s
| | Step 5a | **5c** |
|---|---|---|
| Metadata | 7.0 s | 4.9 s (swarm) |
| First chunk → mpv's first read returns | 1.0 s | **0.86 s** |
| mpv's tail (Cues) read | 0.52 s | **0.41 s** |
| **First frame** | 9.8 s (2.4 s after the first chunk) | **6.8 s (1.5 s after the first chunk)** |
| Median reader wait before the first frame | — | **64 ms** (7 waits) |
| **Seek → playback restart** | not measured | **1.96 s** |
| Pauses | 1 × 0.54 s | 1 × 1.09 s at 7.1 s (see below) |
| File bytes in the first 60 s | 1209 MiB | 1697 MiB |

Acceptance: first frame ≤ first chunk + 2 s ✓, wait median ≤ 0.4 s ✓, seek ≤ 3 s ✓, cancels < 5 % on every peer ✓ (max 1.2 %).

### Per peer: queues shortened, rates held
| Peer | Rate (median) | Outstanding | Queue | RTT |
|---|---|---|---|---|
| qBittorrent 5.3, tcp | **18 MiB/s** | 745 | 0.94 s | 250 ms |
| qBittorrent 5.3, tcp | 5.5 MiB/s | 211 | 1.01 s | 260 ms |
| µTorrent 3.6, tcp | 6.1 MiB/s | 129 | 0.84 s | 219 ms |
| Deluge / libtorrent, tcp | 3.8 MiB/s | 197 | 1.16 s | 297 ms |
| qBittorrent 5.1, uTP | 2.7 MiB/s | 123 | 0.91 s | 230 ms |

A peer delivered 18 MiB/s with under 1 s of queue, so the 3 s queue wasn't needed for speed. The swarm differs from 5a's, so the higher total isn't credited to this step.

### Steals
- **1183 critical steals.** Each pair went one way only, with no request moving back and forth. The median gain was 2.7 s.
- **Most came from peers that deliver slowly** (one peer gave up 650+).
- They cluster at start-up and at the seek, when reads wait on unrequested or far-back chunks.

### What the seek waited on (from `readerWaits`)
- **Two first reads,** 16 MB apart (mpv's seek target, then the cluster it lands on). Each waited ~0.9 s on a chunk not yet requested: about 0.4–0.5 s of the best peer's queue plus a round trip.
- **Between them,** waits of 0.1–0.24 s on chunks at the head of their holder's queue (ahead = 0), i.e. in flight.
- **What's left is queue and round trip.** A shorter floor, or the queue jump (cancelling unsent requests ahead), would trade throughput margin for it.

### The pause at 7.1 s
- **What happened:** mpv paused 1.09 s at 0.75 s of playback, while the reader sat at the download frontier during the ramp.
- **What the reader waited on:** every wait from 7.1 to 8.6 s was a chunk with **0 requests ahead**, 0.4–1.1 s after it was requested (in flight, round-trip bound).
- **Not this step's lever:** the mechanism is the same as 5a's 0.54 s pause, and the length depends on how fast the first peers ramp. mpv buffers only ~0.75 s before starting, which is plan 07's lever (start threshold).

## Notes for the next steps
- **The holder estimate ignores in-flight time:** a chunk with 0 ahead is estimated at a few ms, but takes ~1 RTT. This errs toward not stealing, which is safe, because stealing an in-flight chunk costs a round trip again. Adding RTT/2 for the holder would make the instrument read truer.
- **What remains in start-up (6.8 s):**

| Phase | Time |
|---|---|
| Metadata | 4.9 s |
| Metadata → first chunk | 0.4 s |
| First chunk → first frame | 1.5 s |

  Discovery and metadata (plan 06) is now about 70 % of start-up.
- **The lab's earlier "seeder sends nothing" runs:**
  - This session's first lab attempt ran two copies of the series at once (an interrupted background job kept running), with interleaved runs, shared ports and seeder confusion.
  - Its new-settings run reproduced exactly that symptom: handshake, 32 requests out, then silence.
  - Earlier empty runs may have had a similar overlap. The clean series had none.
  - The seeder snapshot (`qbt-peers.py`) is now saved in every run (`*.qbt.txt`).
