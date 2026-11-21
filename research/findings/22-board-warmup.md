# 22. Board warm-up: get the top continue-watching item ready (step 6g, plan 06 Part C)

> **Correction (6h, `findings/23`):** `progress` is a percent, not a 0–1 fraction. This step primed
> at 75% of the file for a resume at 0.75%, so W1's result relied on data already on disk from 6f.
> Fixed in `f65cbb647`; with the fix and rate memory, Resume → playing measured 1.75–2.5 s.

2026-11-18. stremio-web (local, `wip/streaming-research`). Scope and setting chosen by the user:
the top board item only, and a setting that is on by default.

## Premise (Part 1, read-only)
- The continue-watching items' `deepLinks.player` decode (`core.transport.decodeStream`) to the
  stream last played:
  - HotD → fe521cc file 2 (S02E03);
  - Peaky Blinders → 9247c52e file 0;
  - Pluribus → none.
- The stored stream has no `announce` list, so the warm-up passes `dht:<hash>`.
- `progress` (0–1) is the resume fraction.
- fe521cc is in the "4k DV | HDR" tier smart play races for HotD, so the gate passed.
- HotD was the top item only because of the 6e/6f test plays.

## Built (`src/smartPlay/warmup.ts`)
- **On the board:** for the first continue-watching item, `preload(infoHash, fileIdx, [dht:hash],
  progress)`. Rivulet primes the head, the tail and a 32 MiB window at the resume point.
- **Conditions:** once per torrent per session; only with the internal player and the server
  ready; only when the setting is on.
- **Removal:** an unplayed warm-up is removed after 10 min.
- **Claiming:** the race winner (`useSmartPlay`) and any stream the player loads from that
  torrent (`useStartBuffer`) claim it, so a played torrent is never removed.
- **Setting:** Settings → Streaming, "Warm up continue watching", with a description of what it
  downloads and shares. Stored in localStorage `smartPlay.warmUp`, on by default.

## Results (real app, HotD S02E03, muted, stopped after)
| Run | Result |
|---|---|
| Warm-up timeline (first W1 attempt; stats polled every 2 s) | `create` 0.3 s after the board opened. Metadata in about 4 s; 55 MB downloaded by 18 s (window plus head and tail), then idle; peers 5 → 17 by 20 s, 41 by 64 s. |
| W1: 20 s on the board → details → Resume | The race started at once with fe521cc first; it won after 5.1 s. No start hold (its disk reads are fast); playing 1.6 s later. **Resume → playing 6.7 s.** No buffering in the next ~35 s. The toast named the remux (0.6 of 8.9 MB/s). The played torrent wasn't removed. |
| W2: 10 min unplayed on the board | Board opened 21:24:42 UTC; at 21:35:29 fe521cc's stats were `null`: removed. |
| Settings | Row and description shown, checked by default. Toggling stores off and on. |

**Baseline:** a cold W0 on the same item wasn't run. W1 left the resume window on disk, so a
second run at that point would compare cache against cache. The nearest cold reference is 6f's
real-app click at once on the same episode and torrent: click → playing 12.9 s, a 6.1 s race plus
a 6.7 s hold. The warm-up removed the hold. The race took as long, because:

- **The race still waits for the remux.** fe521cc kept up at once, but the 10 s grace held the
  decision until the larger remux had a rate (5 s, at 0.6 of 8.9 MB/s).
- **Possible follow-up:** have the board warm-up also measure the larger racers, or remember a
  source's measured rate per torrent (plan 09's "peer memory").

## Test-setup notes
- **Two failed W1 attempts were driver errors.** It clicked the wrapper around Resume, Trailer and
  Share; then the remembered key was in the wrong case (tier keys are lowercase). No playback in
  either.
- **HotD's remembered quality** was absent (removed when restoring keys after 6e), and without
  one Resume doesn't autoplay. It was set to `4k dv | hdr` for W1 and removed afterwards.
- **Driver:** `appplay.mjs` gained a `resume` mode (dwell on the board, then the details page's
  Resume).
