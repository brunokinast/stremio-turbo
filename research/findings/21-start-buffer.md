# 21. Start buffer: hold a ramping start on its first frame (step 6f)

2026-11-18. stremio-web `7b990b332` (local, `wip/streaming-research`). This is plan 07 Part C
option 2 ("buffer first"), done early and only for smart-play starts.

## Problem
After 6e, click-at-once starts chose the right source but stalled once for about 2 s just after
the first frame.

**A3** (`play-20261118-170723-hotd-6e-A3-click-v2`):
- fe521cc won at 6.2 s with 3.09 MB/s, against the 2.85 MB/s its file needs (1.08×). Its swarm was
  still ramping: 1.4 → 2.2 → 2.2 → 17.5 MB/s over seconds 4–7.
- mpv started with 0.79 s buffered. The shell runs mpv on defaults, `cache-pause-initial=no`.
- It ran dry 0.7 s in, then waited 2.1 s.

So the start was ramp-bound; the source choice was right. The web UI can't set mpv's cache options
(the shell's allowlist, `findings/03`). It can pause, and it can read `demuxer-cache-time`.

## Replay of past runs (offline, from mpv poll series)
Content available = position + cache duration. Each run was replayed as if playback had started
only once X s were buffered, resuming after 1 s as mpv does.

| Run | Real stalls | X = 0.8 (mpv default) | X = 4 |
|---|---|---|---|
| 6e-A1 click | 1 / 2.07 s | 2.3 s stall | start +5.3 s, **no stall** |
| 6e-A3 click | 1 / 2.11 s | — | start +5.2 s, **no stall** |
| 6d-B click | 1 / 0.56 s | — | start +2.2 s, no stall |
| step 4 / 4b / 4c cold | 2.75 / 4.59 / 3.35 s | stalls | start +6.4 / 8.8 / 7.2 s, **no stall** |
| dwell runs (6b-R5, 6e-A2, A4) | none | none | start +0–0.5 s |
| 6d-A dial10, 6e-M2 remux | 29.8 / 54.5 s | — | still 10 / 30 s (supply below bitrate: only the source choice helps) |

## Rule (`src/smartPlay/startBuffer.ts`)
- **Hand-off:** smart play passes the player the chosen source's measured rate and the rate its
  file needs (`session.handoff`).
- **Hold:** when the rate is under 2× the needed rate, the player pauses right after load (first
  frame shown) and plays once 4 s is buffered ahead of the position.
- **Cap and override:** a hold lasts at most 20 s. Play or pause by the user ends it.
- **Shown:** the Buffering layer, with "Buffering 4 s before playing: this source is still
  speeding up" (your choice: the line under the spinner).
- **No hand-off** (no runtime, a manual pick): no hold.

## Acceptance
| Run | Result |
|---|---|
| Emulated click at once, `startbuf=1` (B1, `play-20261118-180321-…`) | Discovery was slow this time: the race ended at 10.3 s with 11.3 MB/s (4×), so no hold. Play → frame 10.4 s, no stalls. The hold path wasn't exercised. |
| Emulated 15 s dwell (B2, `…-180447-…`) | 6.1 MB/s (2.2×), no hold. Play → frame 0.2 s, no stalls. |
| Mechanism check, `startbuf=force` (B3, `…-180605-…`) | Held 0.5 s until about 4 s was buffered, then played. Play → playing 5.7 s, no stalls. |
| Real app, S02E03 (not on disk), click the 4K row at once | Toast "Playing the 11.3 GB source: the 35.5 GB one can't keep up (3.1 of 8.6 MB/s)". The hold line showed for 6.7 s (8.9 → 15.6 s, which includes mpv's load). Then no buffering for the remaining ~30 s observed. Click → playing 12.9 s. |

## Notes
- The real-app hold (6.7 s) was longer than the replay suggested (1–5 s). It counts from when
  the spinner appears, so it includes mpv opening the file. Without the hold that part would still
  have shown a spinner, so the hold itself added roughly 4–5 s.
- **Open:** mid-play pauses still resume after 1 s of buffer (mpv default). That's plan 07 Part A
  (an mpv config file), which needs the user's OK to touch `%APPDATA%\Stremio`.
