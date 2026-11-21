# 11 — Measured plays (step 2 baseline)

Date: 2026-11-16. Source: House of the Dragon S02E01, 4K DV/HDR (`fe521cc30c71415237f8e423db3588174cfa1cf8`, file 1 of an 8-episode, 87 GiB season pack; 16 MiB pieces; the file is pieces 760–1395, 10.66 GB, 58 min, so ≈ 2.9 MiB/s average bitrate). Built from `rivulet-engine` `wip/streaming-research` `823abf6`/`5a08313`, `anacrolix-torrent` `d963140a`, local `go-utp` `d70d74d`. Disk storage, the user's server settings, peer port 49881.

Data: `data/play-20261116-221700-hotd-cold`, `-222837-hotd-warm`, `-223055-hotd-resume50`, `-*-hotd-app`. Each holds `trace.json` (`GET /debug/trace/{ih}`), `dials.json`, `locks.json`, `status.txt`, `stats.json`, and for driver plays `play.json` (mpv timeline). Read them with `lab/player/summ-play.py <dir>`.

Tools (all in `lab/player/`): `mpvplay.py` (the shell's libmpv, the shell's `stream-lavf-o`, mpv default cache, `vo=null`, muted; creates the torrent like stremio-core, calls `/opensubHash` on file-loaded, captures after stopping), `appplay.mjs` (drives the real shell's web UI over the WebView2 DevTools port), `summ-play.py`.

## Results

| Play | Metadata | First chunk | First piece verified | First frame | Stalls after first frame |
|---|---|---|---|---|---|
| cold (process just started) | 6.9 s | 7.4 s | 22.0 s | **24.0 s** | 2, 13.1 s total (5.5 s + 7.6 s) |
| warm (process up 10 min, torrent removed) | 2.7 s | 4.2 s | 20.0 s | **21.0 s** | 1, 1.6 s |
| resume at 50 % (warm process) | 2.7 s | 3.4 s | 28.8 s | **48.3 s** | 2, 21.4 s total (2.7 s + 18.6 s) |
| real app, cold torrent, warm process | 3.2 s | 4.0 s | 19.1 s | ≈ 33 s (stream #4 opens at 32.8 s) | not recorded by the app driver |

Server times are seconds since the torrent was added; the driver's first frame is from `loadfile`, which follows `create` (6.9 s cold, 2.7 s warm).

Steady state is not the problem: from ~50 s on, useful download runs 16–38 MiB/s, 6–12× the bitrate, and the player's forward cache reaches 57–67 s. Every second lost is in the first 50 s, or at a seek.

## Where the start goes (cold play)

| t (s) | Event | Cost |
|---|---|---|
| 0 → 6.9 | Discovery and metadata (first unchoke 3.9 s, from a DHT peer, before metadata) | 6.9 s; warm process: 2.7 s |
| 6.9 | mpv opens `bytes=0-`; the reader waits for piece 760 (the whole 16 MiB first piece) | — |
| 7.4 → 17 | **Every peer's request limit sits at 2.** 4–6 peers, 2 requests each, ~0.1 MiB downloaded in 10 s | **~10 s** |
| 16.9 → 22.0 | A qBittorrent TCP peer arrives; its limit grows 4 → 20 → 59 → 169 → 520 → 856 by doubling per drain; it delivers 38 MiB in ~7 s | 5 s |
| 22.0 | Piece 760 verified (MarkComplete 127 ms), first byte at 23.7 s (16.9 s after the request) | — |
| 23.7–23.9 | mpv: tail `[Cues, EOF)` 19 ms (tail piece already primed), stream from byte 7137; Rivulet's own probe (Go client) 3 requests, < 5 ms | 0.2 s |
| 24.0 | First frame | — |
| 23.9 → 47 | Reads of pieces 761 and 762 wait 9.4 s and 13.7 s: the rate is still ramping (0.5 → 6.4 → 2.6 → 6.7 MiB/s per 10 s) | the two stalls |

**Why the limit stuck at 2.** `nominalMaxRequests` takes the minimum of twice the last refill and our request-health cap, `rate × RequestQueueTime (3 s)`, floored at 2 (`request-health.go`). `rate` is the peer's lifetime average over its expecting time. A peer that answers 2 requests slowly has a low rate, so its cap stays 2, so it can only ever answer 2 at a time: a feedback loop. It breaks only for a peer fast enough to beat the floor (here, a TCP peer). Two hundred and thirty-five samples also show `outstanding > maxRequests` (e.g. 859/856, 80/1): the cap is applied after requests are already out. This is the measured case for plan 04 (R0 short-window rate, R1 deep first queue) and for R3 (request-health off until those pass).

**16 MiB pieces.** Nothing can be served until a whole piece passes its hash. At the ~1 MiB/s of the first 15 s, piece 760 alone costs ~15 s. Responsive reads (plan 05) and offset-first chunk order matter more on this size than on the reference's.

## Resume at 50 %

- mpv reads the head and the tail before seeking. Here the head request waited **26.1 s** (piece 760), then the tail **15.7 s** (piece 1395 was not ready this time), then the seek target (piece 1082) 3.4 s. The first frame came at 48.3 s, of which 41.8 s was header and Cues.
- After the seek, bandwidth went to the wrong place: of the 100 pieces verified, **85 were from the start of the file** (760 onwards) and **11 at the resume point** (1082 onwards). Peers held queues of 500–960 requests, which is whole 16 MiB pieces, committed to the head. New reads at 1083–1092 waited 2–7 s each, then **34.6 s** at piece 1092 while 20–30 MiB/s arrived elsewhere.
- This is plan 05's case: only the player's bytes are urgent, re-plan on seek, and cancel or steal queued requests for pieces nobody is waiting for. The exact-tail rule (SeekHead) applies too: the tail wait was a whole extra piece.

## The real app matches the driver

The app's request pattern is the driver's: `bytes=0-` (waits for the first piece), then the tail, then a stream from byte 7137, then Rivulet's probe (3 Go-client requests). Two differences:
- The app does not call `/create` first. Torrentio's stream carries the sources, and the stream request creates the torrent.
- The app opened the tail only after the head returned (at 19.2 s), and the tail then waited **13.5 s**. Those 13.5 s were on top of the head wait.

So the libmpv driver is a valid stand-in for start-up, and both orders (head then tail; the tail late) occur.

## Transport and peers

- uTP RTT 165–336 ms (median ≈ 250 ms), matching `findings/10` §E.
- In every play, several uTP peers fill the 1024-packet reorder buffer (peak 1021–1023), never `tooFar`. Duplicates are low (0–96). Hole bursts are normal on the real swarm.
- uTP peers delivered ≤ 50 MiB each. The top TCP peers delivered 100–620 MiB, mostly qBittorrent with limits of 500–960. Step 3 (selective ack, socket buffer) has to move these uTP numbers.

## Storage

| Play | Chunk writes | Total write time | Mean | MarkComplete median / max |
|---|---|---|---|---|
| cold | 124,929 | 178 s | 1.4 ms | 0.5 / 366 ms |
| warm | 143,232 | 287 s | 2.0 ms | 0.5 / 462 ms |
| app | 356,794 | 446 s | 1.25 ms | 0.5 / 808 ms |

- A chunk write blocks its connection's read loop. Summed write time exceeds wall time (178 s in 120 s), so on average 1.5–2.5 connections are blocked writing at any moment. In the last minute of each play, every slow-write slot (1000, all ≥ 10 ms) filled, with a maximum of 94–115 ms.
- MarkComplete (which readers wait on) usually takes 0.5 ms, but 28 pieces in the cold play took 50–366 ms, and first-piece verifications took 123–127 ms.
- **Corrected in `findings/12`:** the slow-write ring had evicted the first minute. The first tail write zero-fills the non-sparse file (7.5 s), blocks the head writes and every peer's read loop, and is most of the "limit stuck at 2" window above. The original reading follows.
- No multi-second zero-fill stall appears at the tail. The first tail piece (1395) verified 1.8 s before the head; MarkComplete took 123 ms. The non-sparse-file question is answered for these sizes: it costs milliseconds, not seconds.
- These timings justify a storage item in step 3: per-write cost and MarkComplete outliers, which the plan only allowed for "if timed".

## Other answers

- **Client lock holds ≥ 50 ms: none** in any play. Lock contention is not a cause; the item can leave the unknowns.
- **`/opensubHash`** costs 8–36 ms once the file is loaded (head and tail are local by then). Rivulet's probe costs < 20 ms. Neither delays the first frame. Rivulet has no `/subtitleSignature` route.
- **Discovery, cold vs warm process:** metadata 6.9 s vs 2.7 s; first unchoke 3.9 s vs 1.7 s. A warm process (DHT routing table) saves ~4 s. That is the whole discovery gain plan 06 can claim on a healthy swarm, versus ~58 s on a dying one (`findings/10` §D).
- **`prefetchNext` warms the wrong episode**, now measured: it pre-read pieces 2117–2119, the tail of file index 2, **S02E03**. S02E02 is file index 0: the pack is not in episode order, and "next" means next index.

## Instrument limits found

- The app driver's mute check failed (it looked for an icon name the markup does not have), so the app play ran ~3 min **with sound**. Fixed: the button is now found by its localized title, and a failed check leaves the player within 60 s. The fix is untested in a play.
- The slow-write ring (1000) fills within the last minute; the total count is not kept. Add a counter in step 3 if needed.
- A remove kept the old requests in the trace's request log. Fixed in `5a08313`.
