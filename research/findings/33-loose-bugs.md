# 33. Loose bugs (R2)

2026-11-19. Rivulet `3a317c4` (log line).

## 1. The S02E05 download that "vanished" (8c): explained, not a bug
- The 1.7 GB 0a02c4fe S02E05 download was deleted by me at 02:13, to reset state for run D. The command that started run D began with that `DELETE`.
- You stopped the tests right after. Two minutes later I saw "one download where there were two" and didn't connect it to my own delete.
- **Added anyway:** Rivulet now logs every client removal ("removed download on request", with infohash and file). Budget evictions already logged theirs.

## 2. Long reader waits: many are requests stuck at the head of a fast peer's queue
- **What was looked at:** the saved traces of 22 plays (`data/play-20261118-*`), each read wait classified by its `readerWaits` record.
- **"Stuck at head":** the chunk was requested, nothing was queued ahead of it on its holder, and it had been outstanding for more than 3× the holder's own delivery estimate + 1 s.

| Run | Reader waits | Total wait | Stuck at head | Never requested |
|---|---|---|---|---|
| 5c cold | 260 | 59.8 s | 73 (15.2 s) | 12 (9.2 s) |
| 6c B resume | 187 | 42.0 s | 142 (24.3 s) | 2 (1.3 s) |
| 6d A | 262 | 48.8 s | 130 (22.5 s) | 1 (1.7 s) |
| 6e M2 remux | 611 | 75.3 s | 412 (**47.3 s**) | 3 (3.6 s) |
| 6e A4 dwell | 94 | 23.4 s | 68 (17.1 s) | 0 |
| (all 22) | | | 13–63 % of wait time | |

- **Example (M2, 67 s):** the chunk was first in the queue of a peer delivering 1.48 MB/s (estimate 10 ms; best other peer 759 ms). It had been requested 2.6 s earlier and arrived after another 3.35 s.
- **Why it isn't stolen:** the critical-steal rule (5c) moves a chunk only when another peer is *estimated* ≥ 2× and 200 ms sooner. The estimate is requests ahead ÷ rate, and **it never considers how long the request has actually been waiting.** A request the peer silently dropped (e.g. around a choke/unchoke without the fast extension, or lost in transport) keeps looking "10 ms away".
- **How much the player saw of it:** reader waits include mpv's readahead reads, so most of this time is hidden behind its buffer. Visible player stalls in these runs were few (6e: one 0.56 s stall on fe521cc). It matters at start-up and seek, and on sources near their bitrate, where the buffer is thin.
- **This corrects plan 09's note.** "No late urgent chunks since 5c" was wrong: late critical chunks do happen, because their estimate is stale rather than because of a scheduling order. That was the condition for plan 09 item 3 (deadline scheduler).
- **Proposed fix (anacrolix fork, its own step):** an age check in the critical-steal path. A critical request outstanding longer than max(2 × its estimate, 2 × the peer's RTT, 500 ms) is duplicated to the best other peer that has the piece; the first copy to arrive wins, and the other is cancelled.
  - **Measure with the same classification:** the stuck-at-head seconds should drop sharply, with throughput unchanged (lab harness of findings/15 for the A/B, then one real play).
  - **Not built yet:** it's a fork change in the request path; it needs your OK.
- **The original "~10 s stalls on a380d54":** the remux needs 9.7 MB/s and its swarm gave 0.3–4 MB/s, so those player pauses were supply (findings/20). The saved trace's longest Rivulet-level wait is 3.4 s; the long pauses are mpv waiting on a source that can't keep up.
