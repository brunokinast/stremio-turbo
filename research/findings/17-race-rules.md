# 17 — Race rules: decide sooner, choose on evidence, keep the player's pieces (step 6b)

Date: 2026-11-18.

Commits (local):
- **rivulet-engine `5206e8c`:** `GET /{ih}/{idx}?probe=1` opens a probe reader (`Engine.NewProbeReader`). It keeps the head and tail pins, starts no next-episode prefetch and doesn't demote the file on close.
- **stremio-web `5c84d8f2e`** (branch `wip/streaming-research`, not pushed):
  - **Dwell decision:** the preloaded source with metadata and the most unchoked peers plays without a race.
  - **Alone win:** a racer alone with data wins at 1 MiB.
  - **Probes:** race reads use `?probe=1`.
  - **Tracker fix:** the preload sends the addon's sources unprefixed.
- **Lab:**
  - `smartplay_emul.py` and its offline test mirror the new rules;
  - `appplay.mjs` gains `tier:<label>` (press a smart-play row) and a dwell argument.

## Emulated plays (test Rivulet, warm DHT, cold cache, muted, one run each)
| | R2 (6a): click at once | **R4 (6b): click at once** | R3 (6a): 15 s dwell | **R5 (6b): 15 s dwell** |
|---|---|---|---|---|
| Play → first frame | 8.8 s | **9.1 s** | 0.4 s | **0.2 s** |
| DHT nodes loaded | 127 | 72 | 48 | 67 |
| First unchoke (winner) | 2.5 s | 4.3 s | dwell | dwell |
| Winner's first byte | 4.85 s | 7.14 s | cached | not read |
| **Winner's first byte → first frame** | **3.9 s** | **2.0 s** | — | — |
| Race won → first frame | 1.65 s | 0.61 s | 0.32 s | 0.2 s |
| mpv's tail read after the race | 1.44 s | 0.38 s | 7 ms | 4 ms |
| How it decided | 8 MiB | 8 MiB (another racer had data) | 8 MiB of cached data | unchoked 17 / 7 / 4 |

**What R4 shows:**
- **The part 6b controls halved:** the winner's first byte → first frame fell 3.9 → 2.0 s.
  - 8 MiB took 1.36 s instead of 2.25 s.
  - The tail wait fell 1.44 → 0.38 s: the pin was kept, but the tail piece wasn't fetched yet, because peers had arrived only 3 s before.
- **The Play → first frame target (≤ 6 s) failed**, because discovery was slower this run:
  - first unchoke came at 4.3 s instead of 2.5 s, and the winner's first byte at 7.1 s instead of 4.9 s;
  - the slower 1.56 GB source delivered first (4.9 s) but took over 2 s to reach 1 MiB, so it never won alone;
  - once the 9.9 GB source delivered, it reached 8 MiB in 1.4 s and won. That is the right choice, made on data.
- Per the test rules this wasn't rerun.

**What R5 shows:** the dwell decision chose by peers (17 vs 7 vs 4), read nothing, and played in 0.2 s.

## Real app (Stremio shell + dev build + new Rivulet, driven over WebView2 DevTools, muted, stopped after)
| Run | What happened | Click → muted (first frame up) |
|---|---|---|
| S02E01, 15 s on the page, **no remembered quality** | No preload: `useSmartPlay` preloads only the show's remembered quality or the last one played anywhere, and this profile had neither. Race at the resume point (535 MB in) with `?probe=1` on all three sources; the winner's 8 MiB took 11.7 s (first byte 7.6 s). mpv then got the head in 51 ms and the tail in 17 ms. | 12.5 s |
| S02E02, 15 s on the page, quality now remembered | Preload 2 s after the list settled, then Play chose fe521cc without probe reads and removed the others | **0.74 s** |

- Confirmed from the page's resource list: race reads carry `probe=1`. The tracker sources reached the engine with a single `tracker:` prefix.
- The DevTools port was opened for the test only, from the test command, and the shell was closed afterwards.

## What this leaves
1. **Discovery is now the click-at-once cost.**
   - In R4, 7.1 of 9.1 s passed before the winner's first byte; in the real S02E01 run, 7.6 of 12.5 s.
   - Candidates: Part B's B2 (announce to all trackers at once) and B5 (dial faster).
   - **Preloading earlier** also helps: the remembered quality is unknown on a first visit, so **preload the best tier with sources when nothing is remembered.**
2. **A race at a resume point is cold even after a dwell.** The preload primes the head and tail, not the resume position, which is where the race and the player read. Preloading the resume position's piece too is a small change (`/create` has no offset today).
3. **The tail can still wait about 0.4 s when peers are only seconds old.** An exact tail pin from the Matroska SeekHead (out of scope here) would narrow what has to arrive.
