# 34. Plan 11 step 1: one record of every file on disk

2026-11-19. Rivulet `58bb5fc` (main), installed in `Tools\rivulet` (the previous build is `rivulet-engine.pre11.exe`).

## What changed in the server
- **One record (`/downloads`) of everything on disk,** in three kinds:
  - **kept:** whole download, deleted only on request;
  - **temporary:** primed, 2-min lease, deleted when it lapses and at startup;
  - **leftover:** an unrecorded cache folder found at startup.
- **Any file a player streams becomes kept** (origin `play`, `lastPlayed`). Probe reads don't count.
- **API:**
  - `POST /downloads` takes `kind`, `origin`, `meta` and `startFraction`;
  - new `GET /downloads/disk` and `DELETE /downloads/:infoHash`;
  - a leftover's `fileIdx` is -1.
- **Removed:**
  - the download budget (`makeRoom`, the `downloadBudget` setting);
  - Rivulet's next-file prefetch.

  The idle and size janitor passes still reclaim torrents with no record (preloads, race losers) as before.
- **A temporary file isn't demoted when another file of the same torrent plays.** This removes the cause of the 9c "trial" special case (findings/31).
- **Race fixed on the way:** marking a played file as kept was asynchronous, so closing its reader at once demoted the file. Now it's marked synchronously when metadata is known.

## Branches
- Every fork's `main` now holds all the work (local, not pushed); the `wip/*` branches were deleted.
- That includes the overdue-steal fix (anacrolix `a1d5ce4f`, Rivulet trace `2565d1d`), so the installed build has it.

## Tests
- New: `downloads_record_test.go` (played → kept, probe not kept, lease lapses, promotion, held against demotion, lapsed after restart, leftovers, torrent delete) and the API record test.
- Full suite: pass, except the two known local-only failures (symlink privilege, cgo package).

## Live (server started alone with the launcher's settings)
| Record | Kind | On disk |
|---|---|---|
| bf7b0a1f (Peaky 1080p partials, Nov 15) | leftover | 15.57 GB |
| 828537d1 (HotD S02E02 single) | leftover | 0.12 GB |
| 21166a85 | leftover | 0.01 GB |
| fe521cc3 #3, #4 (HotD S02E04/05) | kept, complete | 10.07 / 11.56 GB |
| 0a02c4fe #5 (HotD 1.8 GB pack, S02E05) | kept, complete | 1.86 GB |

- **Disk:** 354.3 GB free of 890.9 GB; records use 39.2 GB.
- **Not counted:** partial pieces of *unrecorded* files inside a recorded torrent's folder (e.g. ~0.2 GB of other HotD 1.8 GB pack episodes).
