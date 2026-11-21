# 35. Plan 11 steps 2–2c: the Downloads screen, saved file lists, disk copies play directly

2026-11-19. Rivulet `a7d8994`, stremio-web `1f4dfa28d` (both `main`, local, not pushed). Rivulet installed in `Tools\rivulet` (previous builds: `rivulet-engine.pre11c.exe`, `rivulet-engine.pre11c-b.exe`).

## Server (Rivulet)
- **Saved file lists.** A torrent's metadata is written to `<cache>/<infoHash>.torrent` when known (temp file + rename), reused when it loads again, deleted with its data.
  - Reload after unload: the file list is there at once (test `TestMetainfoSavedAndReused`; live: the Peaky 4K pack's 7 files listed instantly on `/create`).
- **Fetch job for old data.** At start, torrents with data and no saved list get one from the swarm, one at a time, 2 min max each, then unload again unless a download wants them.
  - Live, first run: 0a02c4fe in 4.0 s, 828537d1 in 2.3 s, bf7b0a1f in 3.3 s; 21166a85 (an 8K upscale) got no answer in 2 min → "missing", retried at the next start.
- **Unkept files.** `GET /downloads` lists data with no record as `kind: "unkept"`, one row per file, with the real index, size and progress, loaded or not.
  - Kinds are now `kept`, `temporary`, `unkept`; `leftover` and `partial` are gone from the API. A folder with no record keeps an internal marker so the janitor leaves it alone.
  - Without a file list: `size` 0, `info: "pending" | "missing"`.
  - Files under 1 MB (`RARBG.txt`) are hidden unless they're all a folder holds.
- **Deleting.** The last record of a torrent no longer takes the unkept files beside it (they stay under a marker). Deleting the last unkept file of a folder removes the folder, marker and saved list. Tiny files don't keep a folder alive.

## Part-file completion (found during the Peaky import)
- anacrolix runs in part-files mode: a file still downloading is `X.part`, renamed when complete. On load, **a file with its final name and full size is taken as complete without hashing** (`storage/file-torrent.go`, `setCompletionFromPartFiles`).
- Measured: the 45 GB Peaky pack, moved in from qBittorrent, read complete with 0 bytes hashed (3.1 GB read in total by the process, all from playback probes). A diagnostic test with a corrupted file also read complete.
- Consequences:
  - Rivulet's unkept progress uses the same rule (final name + full size = complete; `.part` = complete pieces from the piece database), so the screen agrees with what anacrolix will do.
  - **Data copied in by hand isn't hash-checked.** Fine for the Peaky pack (qBittorrent had verified it); a bad copy would play corrupt until its pieces fail elsewhere.
  - fe521cc3 S02E03 has 10.9 GB on disk but 2.98 GB verified: earlier file deletes marked those pieces incomplete (`deleteFileData`), and a `.part` with Ok-incomplete marks is not rechecked, so resuming downloads them again. Not changed here.

## Peaky Blinders S06 4K import
- RAZORS pack (`9247c52e…`, 6 × 2160p DV HDR, 45.5 GB) moved from `Downloads` into Rivulet's cache with qBittorrent's .torrent as its saved list; six kept records with show meta.
- Silent libmpv play of S06E01: first frame 0.3 s, 0 stalls, 3840×2160 HEVC PQ, 0 peers, 0 bytes downloaded. Range reads from the server: first byte 0.33 s, 390 MB/s; a mid-file seek 3 ms.

## Web
- **Downloads tab:** show > season > episode cards; quality + bitrate; states complete (nothing), moving (accent bar), stopped (grey bar, Resume), no file list ("Reading file info…" / "No file info"); Pause/Resume + Delete; "Stop and delete?" for moving files; duplicates best/worse (a complete copy beats a partial one); filters All / Downloading / Stopped; an Unknown group for files that name no show; low-disk banner and toast. The old-data toast is gone.
- **Episode screen:** a quality whose source is all on disk shows a purple download icon + "On disk" in place of its seeder count (the matching source too); clicking it plays that copy directly, no race, with "Playing the downloaded copy (6.7 GB)".
  - Why the race didn't guarantee it: only the top 3 sources race (up to 7 when none delivers); a preload measurement can decide without a race; and the highest-bitrate source that keeps up wins (with a 10 s grace for larger ones), so a bigger release with a healthy swarm beat a complete copy on disk.
  - Checked with player navigation intercepted: the 4K HDR click chose `9247c52e…`/0 (RAZORS) and showed the toast.

## Tests
- Rivulet: new `TestUnrecordedFolder`, `TestUnkeptFiles`, `TestUnkeptDelete`, `TestRecordWithoutPath`, `TestFetchMetainfo` (local seeder + a dead torrent), `TestMetainfoSavedAndReused`. Full suite passes except the two known local-only failures.
- Web: lint 0 errors, our files type-clean, build passes; demo with every state reviewed; real app: Downloads states, "Playing" row with Delete disabled, episode chips, on-disk tier.
