# Everything that's left (after 9c)

## Context
Plans 01–08 are built, and plan 09 is done except 9e. You asked to plan all the rest, including every small leftover. Two things are out of scope:
- **Landing / pushing:** you'll build a monorepo with the forks later.
- **Manual downloads:** you'll design those yourself.

**Rules for every step:**
- One step at a time, each ending with a report and your OK.
- Measure before building, and only diagnostic tests.
- Tests run 4K HDR, muted, with playback stopped after.
- Local commits on the `wip/streaming-research` branches.
- UI choices are shown as 2–3 previews first.

Order: small and already designed first, then correctness, then measurements, then new features.

## R1. 9e: port warning (toast only), as already approved
- **Rivulet:**
  - an incoming-connection counter in `dialLog.onPeerConnAdded` (`internal/engine/dials.go:108`);
  - `GET /network-info` gains `incoming {count, lastAt, listenPort, activeSince}` (`handleNetworkInfo`, api.go:845);
  - OpenAPI and a test.
- **Web:** `src/smartPlay/usePortCheck.ts`. While a torrent is active it polls once a minute. After ≥ 5 min active with 0 incoming, it shows one toast per session (i18n).
- **Verify:**
  - a live run shows count > 0 on port 49881;
  - a test instance on unforwarded port 49885 shows the toast once.

## R2. Loose bugs (plan 08 correctness)
1. **The S02E05 download entry vanished between runs (8c).**
   - **Code paths that delete an entry:** the `DELETE` API, `makeRoom` eviction, and the web's 4-min replace check (`ahead.ts`).
   - **Read first:** whether a replace or eviction explains it, cross-checking the run-D/E trace and the `smartPlay.ahead` memory, which records `slow`.
   - **Add** a log line with the reason for every registry removal (`downloads.go`), so a repeat can be traced. Fix it if the cause is found.
2. **About 10 s stalls on the remux a380d54 (6e/8c runs).**
   - Read the stall log and `readerWaits` in the saved traces (`data/`) to tell a supply gap (peers choked or empty) from our side (requests parked on a slow peer, critical steals not firing).
   - One new play only if the saved data can't tell. Fix only if the cause is ours.

## R3. Plan 08 next-day check and idle yield (no code)
- **Next-day check:** the next time you watch, check that the next episode is on disk, starts in under 2 s, and has zero `paused-for-cache` in 5 min.
  - Read from the Rivulet log, `/downloads` and the player trace.
  - It happens on its own when you watch; S02E05 is downloading now for exactly this.
- **Idle yield:** report bytes per hour downloaded while idle, from the registry's samples.

## R4. Plan 05 A5/B3: measure, build only if they lose time
- **A5, reader-close grace.**
  - **Measure:** count, during normal plays (existing traces plus one 10-min play with a mid-play seek), how often the last reader of a playing file closes and its priority drops to None and comes back. mpv reconnects and the race/helper reads are the candidates. Then measure what each one costs (stall seconds after it).
  - **Build only if the cost is > 0:** keep priority and pins for 30 s after the last reader closes; an explicit `remove` ends it at once.
- **B3, re-plan every peer on a seek.**
  - **Measure:** seek-to-data latency (findings/15 has the harness) vs requests parked on pieces no longer wanted.
  - **Build only if parked requests delay the first byte after a seek:** cheap re-plan once per seek, and cancel only requests at priority None (anacrolix fork).

## R5. Plan 07: pause count before/after the mpv config
- **The claim to test:** the config (done in 7A) should group pauses into fewer, longer ones without adding total pause time.
- **Measure:** an A/B on one slow 4K source, the HotD remux (≈ 0.3–4 MB/s against 9.7 needed), 3 min each:
  - config on vs off (rename `mpv-config.txt` for the off run, then restore);
  - pause count and total seconds from the player trace.
- **Output:** a finding. Config tweaks only if the data shows a better setting.

## R6. The two server 404s
1. **`/subtitleSignature`**
   - **What it is:** after a video loads, the web client asks the server for a subtitle "signature" of the file, used to match subtitles. Rivulet has no such route, so it returns 404 (findings/11).
   - **Research:** what the official `server.js` returns and what the web client/core does with it (`withStreamingServer.js`, `fetchVideoParams`).
   - **Then:** implement it in Rivulet, with helper reads at background priority (findings/08: such reads mustn't jump ahead of a starving player). If the result is unused, answer the request cheaply instead.
2. **HLS far-seek 404**
   - **What it is:** seeking far into a video that is being transcoded (the HLS path, used by the web player when the file can't play directly) got a 404 from Rivulet.
   - **Reproduce first** with an HLS stream and a far seek, then fix in Rivulet's `hlsv2` handler. The shell's mpv doesn't use HLS, so this matters only for transcoded playback.

## R7. Per-episode language filter (designed with previews before building)
1. **Per-episode language filter**
   - **What it is:** on an episode's streams list, filter sources by audio/subtitle language (Torrentio lists flags like 🇬🇧 🇧🇷). The choice is remembered per show, and smart play respects it when ranking and racing.
   - **Research first:** which languages the source descriptions actually expose for your shows.

## R8. Seamless switching (from the original agreed list, the largest item)
- **What it is:** when the playing source can't keep up mid-play, switch to another source at the same position without restarting the player.
- **First:** research how the shell's mpv can load a new URL at a time offset (`loadfile` with `start=`), and what the web player's state allows.
- **Then:** discuss whether it's still needed now that download-ahead and smart start exist. Build only after that discussion and its own plan.

## Kept out (with reasons)
- **Landing on main / pushing:** your monorepo later.
- **Manual downloads and the pre-loads/downloads page:** yours, later (the page belongs with the download option).
- **Deadline scheduler:** no late urgent chunks seen since 5c.
- **Merged swarms:** no shared 4K files.

## Close-out after each step
One finding per step (`findings/32` onward). Update TODO (also mark 6, 7A and 8 done, which it still shows open), START-HERE and memory.
