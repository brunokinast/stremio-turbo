# 23. Rate memory: stop waiting for sources known not to keep up (step 6h)

2026-11-18. stremio-web `870700764` (plus the 6g fix `f65cbb647`), local on `wip/streaming-research`.

## Problem
The race's 10 s grace (`LARGER_GRACE`) waits while a larger racer has no rate yet. On HotD the
35.5 GB remux took about 5 s to show one, and it was always far too slow:

| Run | Remux rate ÷ bitrate |
|---|---|
| M1 / M2 / A1 / A3 / A4 | 0.02 / 0.4 / 0.08 / 0.02 / 0.04 |
| Real app 6e / 6f / W1 | 0.18 / 0.36 / 0.07 |

The sources that keep up were above 1× every time. So that wait was most of Resume → playing in
6g (6.7 s).

## Rule (`src/smartPlay/rateMemory.ts`, `supply.ts` `knownRate`)
- **Remember:** after each decision, each source's live rate ÷ bitrate is stored per infoHash with
  a time (localStorage `smartPlay.rates`, at most 200 entries).
- **Recall:** within 24 h, a source with no live rate yet uses its remembered rate. A live rate
  always wins.
- **The grace:** a larger racer remembered below 0.5× doesn't hold a smaller one that keeps up.
- **The dwell decision:** a larger source remembered below 1× counts as measured.
- **Toast:** "…couldn't keep up earlier (x of y MB/s)" when the passed-over source was judged on
  memory.
- Nothing is chosen on memory alone (only live rates make a source viable).

## Fixed on the way
- **The 6g warm-up primed the wrong place.** Continue-watching `progress` is a percent (the board
  draws `width: ${progress}%`), not a fraction. 6g primed at 75% of the file for a resume at
  0.75%, so its W1 result relied on data already on disk from 6f. Fixed (`progress / 100`).
  `findings/22` is annotated.
- **Alone wins reported nothing passed over,** so the toast was missing when the winner won alone
  at 1 MiB. Both win paths now use one `passedOver`.
- **The streams page re-added the race's losers.** Autoplay can race before the page's debounced
  preload runs, and the preload then re-created the two removed losers, which stayed loaded. The
  preload now skips once a play has started from the page.

## Results (real app, HotD S02E03, board 20 s → Resume, muted, stopped after)
| Run | Race | Resume → playing | Notes |
|---|---|---|---|
| R1, memory empty | 5.0 s | 13.8 s | 8.7 s player load: the warm-up had primed the wrong place (bug above). The memory filled: fe521cc 3.8×, remux 0.034×. |
| R2, memory filled, warm-up fixed | 0.03 s (alone win) | **1.75 s** | Toast "couldn't keep up earlier (0.3 of 8.9 MB/s)". The losers were re-created after the race (the bug above). |
| R3, after the preload fix | 1.0 s | **2.5 s** | Losers stayed removed. Toast "earlier". |

The race time measures the memory alone (5.0 s → 0.03–1.0 s). The player load time also reflects
the corrected priming, and data already on disk from R1.

The offline test covers a remembered-slow racer that never delivers (it doesn't hold the smaller
one, and is passed with `remembered`), a live rate overriding memory, and entries older than 24 h.
