# TODO — streaming path

One item per step in `plans/00-START-HERE.md`. Do them in order; report to the user after each and stop. Tick an item only when its "Done when" is met.

- [x] **0. Snapshot uncommitted work** — local `wip/streaming-research` in stremio-web (`cafc99155`), rivulet-engine (`64746c7`), anacrolix-torrent (`4d6ea0fa`). Nothing pushed.
- [x] **1. Finish measurements** — done 2026-11-16, `findings/10` §C–E (lab verdicts; real-swarm probe on House of the Dragon S02E01 because the Peaky Blinders 4K swarms had no supply).
- [x] **2. Instruments** — `plans/02-instruments.md`: timeline, access log, stall log, per-peer samples, storage timings, lock holds; go-utp fork with counters; cold/warm/resume plays. Done when: `findings/11-measured-play.md`.
- [x] **3. Transport fixes** — `plans/03-transport-fixes.md`: selective ack, 4 MiB socket buffer, ack timer; storage fixes if timed. *Creating `brunokinast/go-utp` needs the user's OK.* Done 2026-11-17: `findings/12-transport-fixes.md` (lab + one real play).
- [x] **3b. Sparse cache files on Windows (plan 03 A1)** — done 2026-11-17 (`findings/12` §3b): no start-up write stall; limits rise as soon as data arrives.
- [x] **4. Request queue** — `plans/04-request-queue.md`: short-window rate, deep first queue, low-water refill. Done 2026-11-17 (`findings/13`): lab ramp 9.5 → 3 s, 2.6 → 3.3 MiB/s; real play first frame 23.7 → 14.6 s.
- [x] **4b. uTP window counts reorder bytes** (go-utp `58556c9`). `findings/13` §4b.
- [x] **4c. uTP reorder buffer 2016 + slot-bounded window** (go-utp `2b04c51`): live drops 2163 → 0, uTP share 35 %. `findings/13` §4c. Open: one lab run where the seeder never sent data (unexplained).
- [x] **5a. Start-up reads** (responsive readers, offset-first chunk order, pins only before a stream): first frame 14.6 → 9.8 s, 2.4 s after the first chunk. `findings/14`.
- [x] **5c. Short queues + critical chunks** (plan 05 Part C): queue = 4 transport RTTs, a blocked reader's next 1 MiB moved by delivery time. First frame 9.8 → 6.8 s (1.5 s after the first chunk), seek 1.96 s; lab seek 1.76 → 0.86 s at equal throughput. `findings/15`.
- [x] Lab: snapshot the seeder's peer flags in every run (`qbt-peers.py`, `*.qbt.txt`). The empty runs reproduced when two lab series overlapped; none in the clean series (`findings/15`).
- [x] **5. Start-up rules** (5a, 5c done; A5 no longer applies, B3 measured and not needed, `findings/40`) — `plans/05-startup-rules.md`: player bytes first, exact tail, offset-first chunks, responsive reads, re-plan on seek, reader-close grace, steal by delivery time. Done when: the plan's relative acceptance passes.
- [x] **6a. Real Play path + warm DHT** (Rivulet `9d92006`, `22c27ef`): the app's race costs 2–4 s on a click-at-once play; warm DHT 13.7 → 8.8 s; a 15 s dwell gives 0.4 s Play → frame. `findings/16`.
- [x] **6b. Race and preload rules** (Rivulet `5206e8c`, stremio-web `5c84d8f2e` local on `wip/streaming-research`): dwell → 0.2 s emulated / 0.74 s real app; click-at-once first byte → frame 3.9 → 2.0 s, Play → frame 9.1 s (discovery-bound). `findings/17`.
- [x] **6c. Fallback tier + resume preload** (Rivulet `0e28e7c`, stremio-web `cb1abf036` local): resume after a dwell 6.0 → 0.9 s; fallback tier with a toast; B1 shows connect latency (2–6.5 s) and the half-open limit of 25 dominate a click-at-once play. `findings/18`.
- [x] **6d. Dial rate** (anacrolix `7f1157f2`, Rivulet `ac45a75`): the client-wide 10/s dial limiter queued the first peers (median wait 3.1 s); 30/s → click-at-once Play → frame 4.6 s (was 9–12.6 s), first byte 2.6 s. `findings/19`.
- [x] **6e. Best picture that keeps up** (stremio-web `8c2c714b7`, Rivulet `2cc06e5`, local): rate vs bitrate decides the source; toast when a larger one is passed over. Dwell 0.2 s, no stalls; click-at-once chooses fe521cc but stalls 2.1 s once while ramping. `findings/20`.
- [x] **6f. Start buffer** (stremio-web `7b990b332`, local): a smart-play source under 2x its bitrate is held on its first frame until 4 s is buffered, with a visible line. Real app: held 6.7 s, then no buffering. `findings/21`.
- [x] **6g. Board warm-up** (plan 06 Part C, stremio-web local): the top continue-watching item's last torrent is preloaded at its resume point while the board is open (setting, on by default; removed after 10 min unplayed). Real app: Resume → playing 6.7 s, no hold. `findings/22` (its resume-point bug fixed in 6h).
- [x] **6h. Rate memory** (stremio-web `870700764`, local): each source's rate ÷ bitrate is kept for 24 h; a larger source remembered as too slow no longer holds the race. Race 5.0 → 0.03–1.0 s; Resume → playing 1.75–2.5 s. `findings/23`.
- [x] **6. Preload and discovery** — `plans/06-preload-and-discovery.md`: try smart-play preload; trackers, warm DHT, stable port, dialling. Done when: `findings/13-preload.md`.
- [x] **7A. mpv config** — shell updated 5.0.27 → 5.0.28 (adds config support); `%APPDATA%\Stremio\mpv.conf` loaded, verified (`findings/24`).
- [x] **7. Player buffer** (7A done, `findings/24`; R5 A/B: fewer, longer pauses, no added total, `findings/40`) — `plans/07-player-buffer.md`: mpv config file (any time, *with the user's OK*), then "buffer first" UI. Done when: pause count measured before/after.
- [x] **8. Download ahead** (built 8a–8e; next-day check = R3) — `plans/08-download-ahead.md`, approved plan `plans/08-approved.md`. Decided 2026-11-18.
  - [x] 8a Rivulet download intent — `findings/25` (Rivulet `f7e13bc`; live: S02E04 downloaded with no player, unloaded, kept on disk)
  - [x] 8b budget and eviction — `findings/26`
  - [x] 8c web auto-queue — `findings/27` (+ anacrolix panic and checkpoint fixes)
  - [x] 8d keep the server running and the PC awake — `findings/28`
  - [x] 8e episode-list markers (chip in the meta line) — `findings/29`
- [x] **9. Later** — `plans/09-later.md`, closed by plan 13 (`findings/41`): giving back built (1 MiB/s, only while downloading); merged swarms and deadline scheduler closed with measurements; supply choice done by plan 11; peer memory skipped.
  - [x] 9a thin-swarm diagnosis — `findings/30` (reachable port decides it; qB 0.03 → 7.7 MB/s on the forwarded port)
  - [x] 9b upload cap — not needed: 11 MB/s up with no added latency (`findings/30`)
  - [x] 9c season packs: queued on trial, checked at 60 s — `findings/31` (probe 0.7 vs actual 38.7 MB/s)
  - [x] 9e port warning (toast only) — `findings/32` (+ fork fix: hash failure closed a downloading file's writer)
  - [~] 9d peer memory — skipped (useful peers connect in)

Also before the first merge to `main`: Rivulet `go.mod` → pushed fork version; request-health default off; fix the `diskMaxUnverifiedBytes` comment; remove or convert `RIVULET_EXPERIMENT_*` (see `00-START-HERE.md`).
- [x] **11. One shared path** — `plans/11-shared-path.md`, decisions `plans/11-shared-path-decisions.md`. Steps 1–2c `findings/34`–`35`; steps 3–5 and the Torrentio limit `findings/36` (2026-11-20, stremio-web `48189c869`, `d674f1ad6`, `887a8de33`).
  - [x] 3 one health rule, quality order and picker on the episode page (race, preload, board warm-up removed; start hold kept)
  - [x] 4 Next slot and watch (replaces download-ahead)
  - [x] 5 mid-episode switching via mpv `loadfile replace` (gap 0.26–0.37 s)
  - [x] Torrentio limit 3 → 10 (backup `stremio-addons-backup-2026-11-20-before-limit10.json`)
  - [x] Track re-selection checked live and fixed (base-language match, `findings/39`). The next-day check (R3) applies to the Next slot (passive).
- [x] **12. Manual downloads** — `plans/12-manual-downloads.md`, `findings/37` (stremio-web `597caeb09`, Rivulet order + deadlock fix, go-utp `7b9b337`). Stalled/Try another source and the Next-slot guard checked live (`findings/39`).
- [x] **10. Remaining** — `plans/10-remaining.md`, closed by plan 13 (`findings/40`): overdue steal kept (A/B), R4/R5 measured, R6 fixed, R7 dropped, R8 done by plan 11. Only R3 (passive next-day check) stays open.
- [x] **13. Everything left** — plan `~/.claude/plans/playful-yawning-kettle.md`; review fixes, cleanup and design pass `findings/38`, live checks `findings/39`, plan 10 `findings/40`, plan 9 `findings/41` (2026-11-21).
- [ ] **R3 next-day check** (passive): the next episode prepared by the Next slot is on disk, starts < 2 s, no pauses in 5 min.
- [ ] **Audio normalizer** (asked 2026-11-21; to discuss before planning) — even out loud and quiet passages (and episode-to-episode level) in playback.
