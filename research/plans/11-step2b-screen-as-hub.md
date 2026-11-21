# Plan 11, step 2b: the Downloads screen as the one place for everything on disk

Follows step 2 (the screen exists, uncommitted). It adds three things the first pass missed, plus
making the screen the central place for live downloads.

## What was found (measured 2026-11-19)
- **S02E05 is on disk twice, at two qualities.** fe521cc3 #4 is the MAX WEB-DL at 11.6 GB
  (~23 Mbps); 0a02c4fe #5 is a WEB x265 re-encode at 1.9 GB (~3.7 Mbps). The screen called both
  "Duplicate" and didn't show quality at all.
- **Hidden partials.** The 0a02c4fe folder also holds partial E03 (2.0 GB), E04 (1.7 GB) and
  E06 (2.2 GB) with no record. Step 1 counts only recorded files, so ~5.9 GB is invisible and the
  screen's total (39.2 GB) is too low.
- **Live downloads** already show state, a progress bar, speed and time left. The server
  already supports pause (`PATCH {paused}`) and delete. The screen has no pause or cancel, and
  hides temporary files (tests).

## A. Rivulet: every file in a recorded folder is visible
- When a torrent has records, its other files with bytes on disk are listed as records of kind
  `partial` (origin `leftover`, not wanted, not downloading). Found by the same folder walk the
  leftover scan uses, refreshed on each `GET /downloads`, cached by the folder's modification time.
- `DELETE /downloads/:ih/:idx` works on them (deletes that file's data only).
- Totals (`/downloads/disk` `used`) count them.
- Tests: a recorded torrent with an extra partial file lists it and deletes it alone.

## B. Quality on every row
- `parseRelease` also returns resolution (2160p/1080p/…), HDR (DV / HDR10+ / HDR), source
  (Remux / WEB-DL / WEBRip / Blu-ray / re-encode when x265/x264 appears with WEB or BD), and the
  codec.
- Bitrate = size ÷ runtime, when the runtime is known (Cinemeta's runtime, already fetched for
  the poster backfill and stored in the meta). Otherwise not shown.
- **Shown in its own column** (chosen), between the title and the state:
  `2160p DV WEB-DL 23 Mb`. The file name stays on the second line. Narrow windows drop the
  bitrate, then the column.
- "Duplicate" becomes **"Also on disk"**: its hover lists the other release(s) with their quality
  and size, and marks the better one (higher resolution, then higher bitrate).

## C. Live downloads on the screen (chosen: only in groups)
- **No separate section.** A live row (downloading, being tested, or playing) stays in its show
  group, sorted to the top of it. Temporaries (picker tests from step 3) are shown while they
  move, marked "Test"; the file playing is marked "Playing". The "Downloading" filter lists all
  live rows.
- **Progress:** a thin bar along the bottom edge of the row, like the watched-progress bar on
  episodes, with a moving sheen while bytes arrive (the Windows copy-progress effect). It stops
  when paused. The state cell keeps the text: 64% · 18.2 MB/s · 4 min · 31 peers.
- **Actions on a live row:**
  - **Pause / Resume** (kept files): the existing `PATCH {paused}`.
  - **Cancel**: stops it and deletes what was downloaded, after a confirm. A cancelled
    next-episode download is not started again by itself for that episode; it's remembered in the
    download-ahead memory (and later, by the step 4 Next slot).
  - A file that is playing can't be cancelled (the button explains why on hover).
- The nav badge counts live rows.

## D. Toasts
- Cancel and pause/resume from the screen need no toast (the screen shows the result).
- A next-episode download that starts in the background already says so (step 4 brings the
  table); this step adds nothing there.

## Verification
- Rivulet: `go test -p 1` (engine, api) via goexec, gofmt, vet. Live: the 0a02c4fe partials are
  listed with their sizes, and totals rise by ~5.9 GB.
- Web: lint, build, the parser on the real names (the 6 already checked, plus quality fields).
- Real app:
  - S02E05 rows show `2160p · DV · WEB-DL · 23 Mbps` and `2160p · DV · x265 · 4 Mbps`, each
    with an "Also on disk" tag;
  - the 0a02c4fe partials appear under House of the Dragon, tagged "Partial";
  - a test download of a small non-4K file: it appears in "Active now", pause/resume works,
    cancel deletes it. Your real files are left for you.
- Close-out: commit both forks on `main` with step 2, then `findings/35`.
