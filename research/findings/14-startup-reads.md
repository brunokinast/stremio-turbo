# 14 — Start-up reads (step 5a)

Date: 2026-11-18.

Commits (local, nothing pushed):
- **anacrolix `9d2baa40`:** `ClientConfig.ReaderOffsetFirst` and the start-up instruments.
- **rivulet-engine `4ec187b`:** responsive stream readers, pins only before a stream, `warmMoov` removed, trace fields, an in-RAM cache fix.

Lab: `utplab-fork -reads/-responsive/-offsetfirst`, `run-step5a-lab.sh`; data `R-*.txt`. Play: `data/play-20261118-045942-hotd-step5a-cold`.

## What changed
- **Responsive stream readers:** a read returns chunks once they are written, instead of waiting for the whole verified 16 MiB piece.
- **Offset-first order (fork):**
  - a piece a reader is in is requested from the reader's chunk, wrapping around;
  - two such pieces interleave chunk by chunk, instead of one taking every peer's queue.
- **Pins only before a stream:** the head and tail pins at `Now` are released when the first stream reads the file. `warmMoov`, the second reader that drained the last 8 MiB and could only start after piece 0 verified, is gone.
- **In-RAM cache:** a piece a responsive stream moved past before it verified now joins the "consumed" class when it completes. Without this the budget evicted read-ahead the player still needed: the e2e eviction test failed 2 of 6 runs.

## Lab: one peer, 150 ms each way
Setup: an mpv-style head read (32 KiB, then sequential) from the moment the info arrives, plus, 1 s after the first chunk, a read of the last 100 KiB of piece 2.

| | Head 32 KiB, after first chunk | End-of-piece 100 KiB read |
|---|---|---|
| Old (R-off2) | 11.24 s | 5.68 s after it started |
| **New (R-on)** | **0.30 s** | **0.82 s** after it started |

In the old order the end-of-piece reader's piece won the `Now` tie and took the peer's queue, so the head waited for it. Throughput was the same in both runs (82 MiB in 30 s).

## Real play: House of the Dragon S02E01, cold
| | Step 4 | 4c | 4c diag | **5a** |
|---|---|---|---|---|
| Metadata | 7.0 s | 6.0 s | 5.3 s | 7.0 s |
| First chunk → mpv's first read returns | 5.5 s | 8.7 s | 9.3 s | **1.0 s** |
| mpv's tail (Cues) read | 1.6 s | 32.0 s | 0 s | **0.52 s** |
| **First frame** | 14.6 s | 47.2 s | 15.5 s | **9.8 s** (2.4 s after the first chunk) |
| Stalls | 1 × 2.75 s | 0 | 1 × 3.35 s | 1 × 0.54 s |

- **Acceptance met:**
  - first byte ≤ 1.5 s after the first chunk (1.0 s);
  - tail ≤ 3 s (0.52 s);
  - first frame ≤ first chunk + 5 s (2.4 s).
- **After the first frame,** reads block for 0.35–1.2 s at a time inside pieces (responsive), instead of 2–7 s per whole piece.

## What the new instruments show (this decides the next steps)
- **Urgent-chunk latency** (request → receipt, chunks of `Now` pieces). **Corrected in findings/15:** it counted every chunk of each 16 MiB `Now` piece, so it measured queue depth, not what the player waited for:
  - the two biggest peers had a median of **1.6 s** and a p90 of 2.3 s;
  - a third peer: median 0.4 s;
  - a uTP peer: median 2.4 s.
  - Urgent chunks queue behind each peer's ~3 s of requests (`RequestQueueTime`). This is now the main remaining start-up cost after discovery. It is plan 05 Part C: urgent chunks to the peer with the shortest queue in time, and stealing by delivery time.
- **Blocked pieces:**
  - at the first read, piece 760 had 0 of 1024 chunks, 54 outstanding across 2 peers;
  - later blocks were on pieces 30–80 % done, with 1–4 peers holding requests;
  - no piece was rare (availability 3–12).
- **Pending-write waits:** 1630 waits totalling 149 ms, max 44 ms. Responsive reads waiting for writes elsewhere in the piece is negligible, so plan 05 B2's range-scoped wait is **not needed**.
- **Bytes per file, first 60 s:** the played file 1209 MiB; the next episode 16 MiB (1.3 %, the head prefetch); file 0 0.5 MiB. Next-episode prefetch (A3) has **no meaningful cost** here.
- **Helper endpoints:** they came after the first frame and took 0 ms (A2 not needed).

## Where start-up time goes now (9.8 s)
| Phase | Time |
|---|---|
| Metadata | 7.0 s |
| Metadata → first chunk | 0.4 s |
| First chunk → first frame | 2.4 s |

Discovery and metadata (plan 06) are now the larger lever. Of the 2.4 s, most is urgent chunks waiting in peer queues (Part C).

## Open issues
- **Lab runs where the seeder never sends data:**
  - 2 of the 9 lab runs since 4c (`M1-S10`'s first attempt, `R-off1`), against 0 of 11 between steps 3 and 4b;
  - both had a 1 MiB open window and 32 requests out; the seeder sent ~1 packet per 2 s and no data;
  - the cause is unknown (seeder state, or 4c). Next lab session: snapshot qBittorrent's view of the peer (flags, choke state) at +10 s in every run.
- **Pre-existing in-RAM cache bug:** reading a whole file through the opt-in in-RAM cache and then seeking back to evicted pieces hangs until the reader's timeout; it reproduces on the code before this step. It doesn't affect disk storage (what the owner uses); recorded for the memory-cache users.
