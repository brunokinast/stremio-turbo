# 19 — The dial rate limiter held back the first peers (step 6d)

Date: 2026-11-18.

Commits (local):
- **anacrolix-torrent `7f1157f2`:** `OutgoingConnResultEvent.DialWait`, the part of an attempt's duration spent waiting for `ClientConfig.DialRateLimiter`.
- **rivulet-engine `ac45a75`:**
  - the client-wide dial rate is now **30 attempts/s** (burst 30, libtorrent's `connection_speed`), set with `RIVULET_DIAL_RATE`, where `0` keeps anacrolix's 10;
  - the dial log records `waitMs`;
  - the effective rate is logged at start.

## Cause
- **The limiter:** anacrolix's `DialRateLimiter` defaults to 10 dials/s for the whole client.
- **Where the wait falls:** an attempt takes its half-open slot first, then waits for the limiter (`dialAndCompleteHandshake`). The wait counts as connect time in the dial log.
- **The effect:** when the web client's preload and race start three torrents together, 64 attempts queue in the first 0.5 s (R4 in findings/18). They finished every 0.1 s until 9.5 s, and the best peers waited behind unreachable addresses.

## Measured (emulated click-at-once, same three sources, warm DHT file, cold cache, muted, one run each)
| | A: 10/s (old) | **B: 30/s (new)** |
|---|---|---|
| Limiter wait, first 64 attempts: median / max | **3.10 s / 6.19 s** | **0 s / 1.33 s** |
| First 5 connections completed (s) | 0.06, 1.25, 1.47, 1.88, 2.75 | 0.09, 0.28, 0.93, 0.93, 1.45 |
| Connect time excluding the wait (median) | 1.38 s | 1.36 s |
| First unchoke (winner torrent) | 2.7 s | 0.8 s |
| Winner's metadata | 5.9 s | 1.8 s |
| Winner's first byte | 6.6 s | **2.6 s** |
| Race decided | 8 MiB at 12.4 s, by **a380d54e (31 GB remux)** | 1 MiB alone at 3.7 s, by fe521cc |
| **Play → first frame** | 12.6 s | **4.6 s** |
| Stalls in 60 s | 8 / 29.8 s | 1 / 0.56 s |

**What this shows:**
- **The limiter wait was the delay.** The connect time itself, excluding the wait, was the same 1.4 s in both runs. Without the queue, metadata, the first byte and the race all came about 4 s sooner.
- **A click-at-once play (4.6 s) now beats direct play from step 5c (6.8 s)**, and comes close to 6b's target of ≤ 6 s with room to spare. One run each, so swarm variance applies, but the wait itself is deterministic.

**Side finding from run A:** the race crowned the 31 GB remux because it delivered 8 MiB first, and then it stalled for 30 s of the first minute. Reading 8 MiB first says nothing about whether a source can sustain its bitrate; a remux needs about 70 Mbit/s. The fix is to choose by measured rate against the file's bitrate (plan 09, "choose sources by measured supply").

## Where the click-at-once path stands
| Step | Play → first frame |
|---|---|
| 6a, empty DHT | 13.7 s |
| 6a, warm DHT | 8.8 s |
| 6b | 9.1 s |
| **6d** | **4.6 s** |
| After a dwell on the page (6b) | 0.2 s |
| Resuming after a dwell (6c) | 0.9 s |

**Remaining candidates:**
1. **A bitrate-aware race decision** (the remux case above).
2. The half-open limit and the 3 s minimum dial timeout. Run B dialled 30 in 3 s, so they matter less now.
3. Continue-watching preload (plan 06 Part C, which needs your OK).
