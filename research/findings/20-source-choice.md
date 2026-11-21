# 20. Choosing the source: the best picture that keeps up (step 6e)

2026-11-18. House of the Dragon S02E01 (runtime 3485 s) and S02E02, quality "4k DV | HDR",
three sources: fe521cc (9.9 / 11.9 GB), 0a02c4fe (1.56 / 1.87 GB), a380d54e remux (31.5 / 36.9 GB).
stremio-web `8c2c714b7` (local, `wip/streaming-research`); Rivulet `2cc06e5` (trace counts
preload downloads) installed in Tools (`.prev.exe` = `ac45a75`).

## Problem
The 6b race crowned the first source to read 1 or 8 MiB, and the dwell decision took the most
`unchoked` peers. Neither looks at whether a swarm can sustain the file's bitrate. In 6d run A
the remux won and stalled 30 of 60 s. Rivulet's `unchoked` also always equals `peers`, so the
dwell decision was really "most peers".

## Does early speed predict sustained speed? (gate)
| Source | Needs | First 5 s after metadata | Sustained (30–90 s) | Ratio | Played |
|---|---|---|---|---|---|
| Remux a380d54e | 9.7 MB/s | 0.29 MB/s (0.03×) | 3.2–4.0 MB/s | 0.4 | 13 stalls, 54.5 s of 90 (M2) |
| fe521cc | 2.84 MB/s | 2.65 MB/s (0.93×) | 20–22 MB/s | 7.8 | 1 stall, 0.56 s (6d-B) |
| 0a02c4fe | 0.45 MB/s | 0.32 MB/s (0.7×) | 13 MB/s | 29 | 1 stall, 1.09 s (M3) |

The 30 s dwell (M1) gives the same order: fe521cc reached 10–12 MB/s, 0a02c4fe about 0.5 MB/s,
and the remux about 0.2 MB/s. Early rates understate sustained ones by 3–15× but keep the order,
so the gate passed. Data: `dwell-20261118-164804-hotd-6e-M1`, `play-…-164857-…-M2-remux-direct`,
`play-…-165033-…-M3-small-direct`.

## The rule (`src/smartPlay/supply.ts`, `race.ts`)
- **Bitrate:** size ÷ runtime. Runtime comes from the library item's duration, else the
  metadata's runtime text. Size comes from `behaviorHints.videoSize`, else the "💾 X GB" text.
- **Rate:** the average over the last 3 sample intervals of stats `downloaded` (2 s while on
  the page, 1 s in the race). Intervals under 256 KiB don't count. A race read's rate also
  counts, with a 50 ms floor, so data already on disk counts.
- **Keeps up:** rate ≥ 1.0 × bitrate. An earlier 0.5× threshold, and peak rather than average,
  both let the remux through: it peaked at 1.4×.
- **Choice:** the highest-bitrate source that keeps up.
  - After a dwell, it plays without a race, unless a larger source has no rate yet.
  - In the race, the decision is checked every second.
  - For 10 s, a racer can't win while a larger one may still keep up (no rate yet, or at least
    0.5× its bitrate).
  - If nothing keeps up, at 30 s the best rate ÷ bitrate wins.
  - Without a runtime, the old first-to-arrive rules apply.
- **Telling the user:** a toast names the largest source passed over, e.g. "Playing the 11.9 GB
  source: the 36.9 GB one can't keep up (1.6 of 8.9 MB/s)". The racing panel shows "keeps up" or
  "too slow" for each racer.

## Bugs found by the real app (each fixed before acceptance)
1. **A small file on disk won.** 0a02c4fe's S02E02 file read 8 MiB in 54 ms. Under the planned
   alone-win rule (wins if it keeps up or is the smallest), it won before fe521cc delivered
   anything. Fix: the 10 s grace for larger, promising racers.
2. **A cached larger file looked too slow.** fe521cc's file was on disk. It downloaded a few KB
   (a "rate" far below its bitrate), and its 8 MiB read finished inside the old 200 ms minimum,
   so it never recorded a read rate. Fix: the 256 KiB interval minimum and the 50 ms floor.

## Acceptance
| Run | Result |
|---|---|
| Emulated click at once (A3, `play-20261118-170723-…`) | Chose fe521cc. Play → frame 6.8 s, against a target of ≤ 6 s (A1, at 0.5×: 5.7 s). **1 stall of 2.1 s in 60 s, missing the ≤ 1 s target.** The stall came 1 s after the first frame, while the swarm ramped (2 → 20 MB/s). The choice was right; the start is ramp-bound. |
| Emulated 15 s dwell (A4, `play-20261118-170940-…`) | Chose fe521cc (5.0 of 2.85 MB/s). Play → frame 0.2 s, no stalls. The record lists the remux as passed (0.35 of 9.03 MB/s). |
| Real app, S02E02, 15 s dwell, press the row | Chose the 11.9 GB source. The toast named the 36.9 GB remux. Playback started (muted, stopped after). |

The emulated runs came before the two real-app fixes. Those fixes only change cases with data on
disk; the offline test covers them (`test_smartplay_emul.py`).

## Notes
- The lab emulation now mirrors the rule (`smartplay_emul.py`; `mpvplay.py dur=` enables it) and
  records `passed` in `play.json`.
- `load_sources` used to get size 0 for Torrentio streams; it now parses the "💾" text.
- **Open:** a stall of about 2 s just after the first frame on a click-at-once start. The fix is
  more buffer before starting, or ramp-aware starting, not a different source.
