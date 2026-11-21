# 16 — The real Play path, and a warm DHT (step 6a)

Date: 2026-11-18.

Commits (local):
- **rivulet-engine `9d92006`, `22c27ef`:** the DHT routing table is kept across restarts.
- **Lab:**
  - `lab/player/smartplay_emul.py` (the web client's preload and race, emulated), with an offline test `test_smartplay_emul.py`;
  - `mpvplay.py` gains `race= tier= limit= preload=`.

Sources: `data/sources/hotd-s02e01-torrentio.json` (Torrentio's public list). Plays: `data/play-20261118-13{1850,2122,2354}-hotd-6a-*`.

## What the app does, and what had never been measured
- **On the streams page,** smart play preloads the chosen quality's first 3 sources (`POST /create`).
- **On Play,** it races them: each reads 8 MiB from the start position, the first to finish wins, and the losers are removed. **Only then does the player open the stream.**
- **The emulation follows the code exactly,** bugs included. With the user's Torrentio limit of 3, the "4k DV | HDR" tier is:

| Source | Seeders | Size |
|---|---|---|
| `fe521cc` (the one plays so far used) | 121 | 9.9 GB |
| `0a02c4fe` | 30 | 1.56 GB |
| `a380d54e` | 25 | 31 GB remux |

## Results (House of the Dragon S02E01, cold cache, muted, one run each)
| | R1: click at once, empty DHT | R2: click at once, warm DHT | R3: 15 s on the page, warm DHT | 5c: direct play, no race |
|---|---|---|---|---|
| **Play → first frame** | **13.7 s** | **8.8 s** | **0.4 s** | 6.8 s |
| First DHT peer connected | 9.6 s | 2.2 s | (during the dwell) | 3.7 s |
| DHT dials in the first 5 s | 4 | 38 | — | — |
| Winner's metadata | 6.3 s | 3.9 s | before Play | 4.9 s |
| Winner's first byte → 8 MiB (race won) | 8.0 → 12.5 s | 4.9 → 7.1 s | instant | — |
| Race won → first frame | 1.2 s | 1.65 s | 0.32 s | — |
| mpv's tail (Cues) read after the race | 0.73 s | 1.44 s | 7 ms | 0.41 s |
| Losing racers | never had metadata, or never delivered | 1 had metadata, no data | all 3 delivered at 15.036–15.037 s | — |

## What this shows
1. **The race costs 2.3–4.5 s on a click-at-once play.** That is the 8 MiB from the winner's first byte, when mpv needs under 1 MiB for its first frame. Three swarms also share our download while racing. Today's app is about 2 s slower than playing the best source directly (R2 vs 5c).
2. **After a dwell, the race is a coin flip.** All three preloaded heads were cached, so all three delivered within a millisecond and the first to finish 8 MiB won. This time that was the best source by chance; it could as easily have been the 1.56 GB file or the 31 GB remux.
3. **The race releases the tail pin.**
   - Step 5a keeps the head and tail pins only "before a stream", and the race's range reads are streams.
   - So mpv's Cues read after the race waits: 0.73 s and 1.44 s, which is most of race-won → first frame.
   - With a dwell, the preload had already fetched the tail (7 ms).
4. **A warm DHT is worth about 2.5 s at metadata and 5 s on the click-at-once play** (R1 → R2, one run each).
   - It works by finding many DHT peers early (4 → 38 dials in 5 s), not by a faster first dial. R1 also dialled its first DHT peer at 0.28 s, so the plan's "first dial ≤ 0.5 s" criterion was the wrong measure.
   - Nodes loaded: 127 in R2, then **48** in R3: R2's process saved once at 60 s, and its younger table replaced R1's fuller file. Fixed in `22c27ef` (saves merge with the previous file).
5. **Preload with a dwell gives what the user wants: 0.4 s from Play to first frame.**
   - It only helps when the user stays on the page about 5 s or more (metadata took 2.2 s, the head piece verified at 5.7 s).
   - "Continue watching" never shows the streams page, so it gets nothing (plan 06 Part C).
6. **Side finding:** the app passes the addon's `tracker:`-prefixed sources as trackers, and the preload wraps them again (`tracker:tracker:udp://…`). Rivulet strips one prefix and then drops them as host-less URLs, so preloaded torrents get no trackers. It matters little, since the productive peers came from the DHT, but it's a bug.

## Proposed step 6b (to plan next)
Ordered by gain on the click-at-once path (R2: 8.8 s) and by risk:

| # | Where | Change | Expected |
|---|---|---|---|
| R1 | Rivulet | Race reads don't count as a player stream: a request marked as a race read (a query flag) keeps the head and tail pins | race won → first frame 1.65 → ~0.4 s |
| R2 | stremio-web `race.ts` | Decide sooner: a racer wins at 1 MiB if no other racer has any data, otherwise at 8 MiB or by rate as now | −2 to −4 s on a click-at-once play |
| R3 | stremio-web | After preload, choose by what the dwell measured (bytes per second per source, from `/stats`), not by an 8 MiB read of cached data | no coin flip after a dwell |
| R4 | stremio-web | Pass the addon's sources to `/create` as given (no second `tracker:` prefix) | the trackers reach the engine |

With R1 and R2 the click-at-once path should land near direct play (~6–7 s, or lower with the warm DHT); after a dwell it stays ~0.4 s and picks deliberately.
