# 40. Plan 13 Part E: plan 10 closed

2026-11-21. Rivulet `a363c93` (`/subtitleSignature`), `a935936` (HLS probe); anacrolix `a1d5ce4f` (overdue steal, already installed since findings/34). Lab data in `data/play-20261121-*`; scripts `data/classify-waits.py`, `data/seek-waits.py`; `lab/player/mpvplay.py` gained `mpvconf=1`.

## E1. Overdue critical steal: kept
- **State:** the fix was already in the installed build (findings/34 merged it), not "committed, not installed" as plan 13 said.
- **Earlier A/B (findings/33 source, a slow remux):** stuck time halved; the source was supply-bound, so total wait was unchanged.
- **This A/B:** a well-supplied source (HotD S2E1 4K DV/HDR WEB, 0a02c4fe file 1, 31 seeders). A = the same build with the overdue check switched off (lab copy, not committed); B = installed. Cold each run (the torrent deleted before and after), 120 s with a seek to 50 % at 60 s, A/B alternated three times.

| Run | Reader waits | Stuck at head | Stalls | First frame | Seek → playing | Downloaded |
|---|---|---|---|---|---|---|
| A1 | 59.9 s | 17.6 s | 1 (1.6 s) | 4.7 s | 1.12 s | 1,686 MB |
| B1 | 11.0 s | 1.1 s | 0 | 1.15 s | 0.06 s | 1,683 MB |
| A2 | 12.0 s | 4.0 s | 0 | 1.8 s | 0.07 s | 1,683 MB |
| B2 | 19.8 s | 1.1 s | 0 | 1.10 s | 0.06 s | 1,690 MB |
| A3 | 54.0 s | 21.8 s | 4 (7.7 s) | 8.6 s | 0.50 s | 1,681 MB |
| B3 | 19.7 s | 1.3 s | 0 | 1.14 s | 0.06 s | 1,689 MB |

- Stuck-at-head time falls from 4–22 s to 1.1–1.3 s, with throughput unchanged. B never stalled; A stalled in two of three runs.
- **Lab note:** since findings/34 a played file becomes a kept record, so `/<ih>/remove` no longer clears its data. The first attempt's B1 was warm and was discarded. Cold runs now need `DELETE /downloads/<ih>`.

## R4. Plan 05 A5 and B3: closed without building
- **A5 (reader-close grace):** no longer applies. Every played file has a record, and closing the last reader demotes only files with none (`!isWantedLocked`), so an mpv reconnect can't drop priority.
- **B3 (re-plan on seek):** at the seek target, the first wait is for an **unrequested** chunk in every play: 0.36–1.2 s, with one 5.5 s outlier on the slow remux. It's not parked behind other requests. Waits with requests queued ahead are few and short (36–527 ms). A blocked read already re-plans peers when its chunk is unrequested (5c). B3's build condition isn't met.

## R5. mpv.conf: keeps total pause time, groups pauses
HotD S2E1 remux (a380d54e, needs ≈ 9.7 MB/s), cold, 180 s each, libmpv with and without `%APPDATA%\Stremio\mpv.conf` applied as the shell does.

| Run | Downloaded | Pauses | Total | Longest |
|---|---|---|---|---|
| without 1 | 1,700 MB | 22 | 53.6 s | 10.2 s |
| with 1 | 2,644 MB | 1 | 16.4 s | 16.4 s |
| without 2 | 2,704 MB | 12 | 19.3 s | 5.7 s |
| with 2 | 1,612 MB | 2 | 39.6 s | 21.2 s |

- Supply varied (1.6–2.7 GB in 3 min), so compare runs with similar supply:
  - ~2.7 GB: 12 pauses / 19.3 s without, 1 / 16.4 s with;
  - ~1.65 GB: 22 / 53.6 s without, 2 / 39.6 s with.
- Plan 07's claim holds: the config makes pauses fewer and longer, without adding total pause time. Kept as is.

## R6. The two server 404s
- **`/subtitleSignature`:** the web player asks for it after load. stremio-core-web 0.67.1 never reads it: no `embeddedSubtitleSignature`, `fpsMilli` or `durationMs` in its wasm. Rivulet now answers `{"result":{"signature":null,"videoFpsMilli":null}}` without reading the file. Tested and documented in openapi.json.
- **HLS "far-seek" 404, reproduced and fixed:**
  - With a working session, a far seek works: segment 755 of 944 (80 %, past the 57 % on disk) returned 200 in 5.8 s.
  - The 404 happens when the probe of an incomplete file runs before its index pieces arrive (just after start-up): over the 30 s timeout. That failure was cached as "not media" for 5 min, and the master still answered 200, so the playlist and every segment were 404 "hls: unknown duration".
  - **Fix:** a timed-out probe isn't cached, and a master without a duration is 503 (retryable). Covered by a test.
  - Only matters for transcoded (browser) playback; the shell plays through mpv.

## R3. Next-day check: passive
To be read from the logs the next time an episode prepared by the Next slot is played.

## R7, R8
- R7 (language filter): dropped by you.
- R8 (seamless switching): done by plan 11 step 5.
