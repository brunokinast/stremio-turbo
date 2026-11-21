# 18 — Preload the closest quality and the resume point; where click-at-once time goes (step 6c)

Date: 2026-11-18.

Commits (local):
- **rivulet-engine `0e28e7c`:** `POST /create` accepts `startFraction`. `PrimeFile(idx, frac)` pins the pieces covering 32 MiB around the resume point with the head and tail, and that window counts wherever pinned spans are released or protected. A probe reader keeps it, the player's reader releases it, and a moved resume point releases the old window.
- **stremio-web `cb1abf036`** (branch `wip/streaming-research`, not pushed):
  - **Fallback tier:** when the remembered or last quality isn't offered, the preload uses the most-seeded tier at the same resolution, else the first tier at 4K or below. You chose this rule; the first version took the addon's first tier, which on House of the Dragon is "4320p HDR" (8K).
  - **Toast:** when that happens, a toast says so once the episode's streams have loaded (your request: "inform the user when such decisions happen"). Autoplay plays the fallback quality too, without replacing the remembered one.
  - **Resume preload:** the preload sends `startFraction`.
- **Lab:**
  - `smartplay_emul.py` and `mpvplay.py` gain `start=+N dur=S resumeprime=0|1`;
  - the offline test checks the resume body and offsets;
  - `appplay.mjs` gains `tier:*`, searches only inside the quality rows, and clears the page's resource-timing buffer.

## Resume after a 15 s dwell (emulated, S02E01 from 600 s = 17 %, warm DHT, cold cache, one run each)
| | A: preload without the resume point (6b behaviour) | **B: preload with it** |
|---|---|---|
| **Play → first frame** | 6.0 s | **0.9 s** |
| Decision | dwell: fe521cc (11 unchoked peers) | dwell: fe521cc (11) |
| mpv's first read at the resume point (1.84 GB in) | 1226 ms | 211 ms |
| Head and tail reads | 1–6 ms | 3–15 ms |
| Pieces primed per source at the resume point | — | 3 × 16 MiB (fe521cc, a380d54e), 17 × 2 MiB (0a02c4fe) |

- **Cost:** at most about 130 MB more downloaded across the three preloaded sources, bounded by the pieces primed. It wasn't measured, because the losers' traces have no peer samples before a reader opens.
- **Log check:** Rivulet's debug log confirmed the windows, e.g. fe521cc file 1: resume pieces 868–871 at fraction 0.172.

## Real app (Stremio shell + dev build + new Rivulet, over WebView2 DevTools)
| Check | Result |
|---|---|
| Resume wiring (S02E02 page, 15 s, no play) | `/create` carried `startFraction` 0.034, and Rivulet raised pieces 150–159 with the head and tail |
| Fallback + toast (S02E03 page, last quality "4k hdr", 15 s, no play) | Toast: "4k hdr isn't offered for this episode, so 4k DV \| HDR is used instead". The three 4K DV sources were preloaded during the dwell, not the 8K source |

**Incidents in testing:**
- **An unmuted play.** The first `tier:*` matcher took the board's "Continue Watching" button. The app played S02E02 unmuted for about 90 s before I stopped it, which likely saved about 2 minutes of progress on S02E02. The driver now matches only inside the quality rows and fails rather than searching further.
- **A localStorage key removed by mistake.** Clearing my test's quality keys also removed `smartPlay.quality.tt2442560` = "4k hdr" (Peaky Blinders). It was restored at once. The global `smartPlay.quality` is set to "4k hdr", its likely value before my 6b test.

## Where the click-at-once time goes (plan 06 B1, from R2, R4 and the real S02E01 run)
| | R2 (6a) | R4 (6b) | Real S02E01 |
|---|---|---|---|
| Winner's first byte | 4.5 s | 6.7 s | 7.5 s |
| Outgoing dials by 3 s / 5 s / 8 s | 28 / 41 / 50 | 25 / 28 / 35 | 25 / 25 / 35 |
| Median time from dial start to connected | 3.1 s | **4.3 s** | 2.1 s |
| Median failed dial | 3.0 s | 6.4 s | 3.2 s |
| Productive peers (> 1 MB) | 20 | 7 | 13 |
| Best peers: dial began → connected | 0.23 s → 2.3–3.3 s | 0.19 s → 3.6–5.2 s | 0.37 s → 5.1–6.9 s |

**What this shows:**
1. **Addresses aren't the problem.** The peers that later delivered most were known and dialled within 0.2–0.4 s, from the DHT and trackers. So B2 (announce faster) would gain little.
2. **Connecting is.** A successful connection took 2–4 s at the median, and the best peers took 2–6.5 s.
   - The duration covers the dial plus the BitTorrent handshake, including anacrolix's retry without header obfuscation (`dialAndCompleteHandshake`).
   - Many connections finished just past 4 s, which is `HandshakesTimeout` (4 s). That fits a first, obfuscated attempt hanging to the timeout and the plain retry then succeeding.
3. **Dialling stalls at 25.** In R4 and the real run the outgoing dials stop at 25 between 3 s and 5 s. That is the half-open limit (`HalfOpenConnsPerTorrent` 25), filled by these slow attempts.

**Proposed 6d:**
1. **Measure the handshake phases per dial:** TCP or uTP connect, the obfuscated attempt, the fallback, and the BitTorrent handshake. anacrolix has a counter for "initiated conn with fallback header obfuscation".
2. **Then fix what that shows:** for example, try plain and obfuscated in parallel or with a short first timeout, as libtorrent's settings allow, and raise the half-open limit while connects are slow.
3. **Target:** the winner's first byte under 3 s on a click-at-once play.
