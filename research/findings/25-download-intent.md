# 25. Download intent in Rivulet (step 8a)

2026-11-18. Rivulet `f7e13bc`, local on `main`, installed in Tools (`.prev.exe` = `2cc06e5`).

## Problem
Before this, Rivulet couldn't keep a file downloading for later:
- closing the last reader set the file to priority None;
- playing another file of the torrent demoted it;
- the idle janitor (300 s), the LRU cache cap, the 20-engine cap and the web's `remove` all
  **deleted** the torrent's data.

## What was built
- **Registry** (`internal/engine/downloads.go`): entries keyed by (infoHash, fileIdx), with
  trackers, paused, watched, complete, name, size and addedAt. Persisted to
  `<APP_PATH>/downloads.json` (temp file + rename).
- **Background files:** a per-engine `background` set. The whole file is downloaded at Normal
  priority, so a playing stream's readahead (High/Now) still comes first. `ensureDownloading`,
  `PrimeFile`'s demotion and `pinnedReader.Close` never demote a background file.
- **Protection:**
  - the idle janitor, LRU eviction and the engine cap skip torrents with downloads;
  - `RemoveEngine` on such a torrent stops only its other files, so a race loser or warm-up
    expiry can't delete a download.
- **Janitor tick:** re-adds the torrents of incomplete, unpaused downloads (also at start, so they
  resume after a restart). Once all of a torrent's downloads are complete and it has been idle
  for 1 min, it's unloaded **without deleting data**.
- **API** (OpenAPI updated, tag Downloads):
  - `GET /downloads`;
  - `POST /downloads {infoHash, fileIdx, trackers?, sources?}`;
  - `PATCH /downloads/{ih}/{idx} {paused?, watched?}`;
  - `DELETE /downloads/{ih}/{idx}`, which deletes the data once no other download of the torrent
    remains and nothing reads it.
  - Writes need a browser origin, a loopback peer or api_password (as `/settings`). The in-RAM
    cache refuses with 409. CORS allows PATCH.
- **Not done:** plan 05 A5 (reader-close grace). It isn't needed: background files are exempt by
  design.

## Tests
- `internal/engine/downloads_test.go`:
  - the registry round trip;
  - a background file survives its stream closing and another file playing (a plain stream's
    file still drops to None);
  - the idle janitor, the engine cap and RemoveEngine keep data, and removing the last download
    deletes it;
  - completed downloads are unloaded keeping data;
  - pause stops the background download;
  - the in-RAM cache is refused.
- `internal/api/downloads_test.go`: add, list, patch, delete, plus 400/403/404/405/501.
- Full suite: only the two known local failures.

## Live check (standalone Rivulet, user's cache, no player)
- **Source:** HotD S02 2160p DV/HDR pack `fe521cc`, file 3 = S02E04 (9.38 GiB, 3.13 GiB already on
  disk), added with `dht:` only.
- **Download:** 3.13 → 9.38 GiB in about 3.5 min (27–37 MB/s) with no reader open. `GET /downloads`
  states: downloading → complete.
- **Unload:** about 1 s after the next janitor tick, the torrent was unloaded
  ("unloaded completed downloads"). The file stayed on disk (10 074 195 295 bytes), and it
  stayed through the 300 s idle timeout.
- **Re-add:** metadata in 2.3 s; the download reported complete; the file's stream progress was
  0.998 with `downloaded` 0, so nothing was fetched again. It plays from disk.

S02E04 is left downloaded (it's the user's next episode). `DELETE /downloads/fe521cc…/3` removes
it.
