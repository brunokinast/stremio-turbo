# 37. Plan 12: manual downloads, and the server deadlock behind the `/downloads` hang

2026-11-20. stremio-web `597caeb09`; Rivulet `0a4764c` (order) and the deadlock fix (connRTT); go-utp `7b9b337`. All on `main`, local, not pushed.

## What was built
- **Streams page:**
  - Each quality row has a ⬇. It races that quality's top 3 sources and keeps the one that would finish soonest (largest rate ÷ size).
  - Each source in the expanded list has a ⬇ that keeps exactly that source, with no race.
  - While racing, the row says "Choosing a source…", and ✕ cancels. Once kept, the row shows "⬇ N%" in place of its seeders.
- **Episode list:** a season button with live status: "Download season", "Choosing sources…", "N of M on disk · %", or "Season on disk".
- **The season chooser:**
  - It opens as a popover, or as a centered dialog when the content doesn't fit (it measures overflow).
  - It lists every quality any episode offers, highest resolution first. Each row shows episode count, total GB, the weakest episode's live 👤, and a ⬇.
  - **Step-downs** are said up front ("E3, E5–E8 as 4k DV | HDR").
  - **A per-episode breakdown** shows each episode's release, size and 👤; during a race, the live rate; then "Queued".
  - **Footer:** episodes on disk, downloading or not aired (all skipped), and the free space. A row that doesn't fit with 10 GB spare can't start.
- **Race** (`manualDownload.ts` and `seasonPlan.ts`, the latter Jest-tested):
  - A torrent shared by several episodes is tested once, on its earliest episode's file. At most 12 torrents are tested at once, and every episode's top source is always among them.
  - Winners are kept with origin `manual` and order `season*1000+episode`. Losing tests are deleted at once.
  - A toast says what was queued and the step-downs; clicking it undoes the job.
- **Rivulet:** of a torrent's incomplete kept files, one goes first at High priority, the rest stay at Normal. That's the file being played, else the lowest order, else the oldest record. The view reports `order` and `first`.
- **Protection:** the Next slot's watch and the mid-episode switch now leave alone any record with a non-automatic origin (`isAutomatic`).
- **Downloads screen:** a kept file that hasn't gained a byte in 10 min shows "Stalled", with "Try another source" (the same race, skipping that torrent).

## Checked live (HotD S2, muted, nothing played)
- **Chooser:**
  - S2 with 2 episodes on disk: 16 qualities, so it opened as the dialog.
  - S3: 12 qualities, as a popover above the list, with step-down lines.
  - Sources arrived in about 1 s, and the live counts settled within the scrape's 10 s.
- **Season race, 720p:** "Choosing sources…" on the button, and live rates in the breakdown (4.1 MB/s for the GalaxyTV pack).
  - All six missing episodes went to that one pack, tested once.
  - Records `9d5cc012:{0,1,2,3,6,7}` were kept/`manual` with orders 2001–2008. The 7 other tests were deleted.
  - Everything completed within seconds; the button then read "Season on disk".
- **One episode (E8, 720p):** "Choosing a source… ✕", then the toast "Downloading E8 of season 2 in 720p (0.3 GB) · click to undo", and the row showed "⬇ 0%". Clicking the toast removed the record.
- **Source ⬇:** the exact `infoHash:fileIdx` was kept/`manual` with order 2008.
- **Fixed during the checks:**
  - add-on responses have no player links, so `groupByQuality(groups, null)` is used, not the video id;
  - an episode without the chosen quality is now flagged as a step-down, even when the order starts at its resolution;
  - the dialog's width applied to the overlay;
  - the popover was clipped by the panel body;
  - the progress chip pushed the row's play button out of line (it now has its own column).
- All test records were deleted afterwards.

## Not checked live
- **High moving to the next file as one completes:** the 720p pack finished in seconds. Covered by `TestFirstFileOrder` (pausing E1 moves High to E2).
- **"Stalled" and "Try another source":** need 10 min at 0 B/s.
- **A manual record of the next episode surviving the Next slot's watch:** a one-line guard; no live stub run.

## Cross-torrent order (measured; no throttle built)
HotD S2E7 and S2E8, two single-episode 4K DV/HDR torrents (SuccessfulCrab), 60 s each after a 15 s ramp:

| | E7 | E8 | Sum |
|---|---|---|---|
| Alone | 9.2 MB/s | 26.0 MB/s | — |
| Together | 28.5 MB/s | 4.2 MB/s | 32.7 MB/s |

The first episode didn't lose rate with a second torrent beside it; each swarm's supply decides its rate, not the link. Per the plan, the connection cap for later torrents isn't built.

## The `/downloads` hang: a client deadlock (fixed)
- **Reproduced** by the first contention run. `/downloads` timed out while `/settings` answered, the same symptom as findings/36. The pprof dump is in `data/hangs/2026-11-20-downloads-deadlock.txt`.
- **The two lock orders:**
  - The uTP socket reader (`Socket.processPacket`) holds the socket lock and calls anacrolix's `firewallCallback`, which takes the client lock.
  - A peer's read loop holds the client lock (`receiveChunk` → `nominalMaxRequests` → `transportRTT`) and calls Rivulet's `connRTT` → uTP `Conn.Stats()`, which takes the socket lock.
- **Effect:** both wait forever, and 178 goroutines queue behind the client lock.
- **Source:** our own `PeerConnRTT` hook from plan 05c (queue = 4 transport RTTs).
- **Fix:**
  - go-utp `Conn.RTT()` reads an atomic copy of the smoothed RTT, with no lock. `TestRTTWithoutSocketLock` reads it while holding the socket lock.
  - `connRTT` uses it.
  - The trace's `uc.Stats()` runs outside the client lock, so it was left as is.
- **After the fix:** the same contention run completed with the server responsive throughout.
