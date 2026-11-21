# Plan 08: download ahead (8a–8e)

## Context
Starts are fast now (6a–6h, 7A), but a source whose swarm can't keep up live still buffers, and only time-shifting fixes that. The goal: the next episode is fetched while you watch the current one, so it plays from disk at once, with no buffering, in the best picture that can make it in time.

**Your decisions:**
- **Automatic** next episodes.
- **Server:** keeps running after Stremio closes until downloads finish, and keeps Windows from sleeping (not the screen) while a download runs.
- **Display:** markers on the episode list.
- **Defaults:** a 60 GB budget, watched episodes deleted first, uploads at the existing limit.
- **Source rule, in your words:** "the best that will keep up… its download time is what's left of the current episode and what it downloads while it's playing". The downloaded copy is the choice, so it plays directly.

**The rule, as a formula:**
- **Inputs:**
  - L = time left in the current episode when the download starts;
  - D = the next episode's runtime;
  - S = a candidate's size.
- **Fit test:** the candidate never buffers if its rate ≥ S ÷ (L + D). That rate fills the deficit before the episode starts and keeps ahead while it plays.
- **Choice:** the highest-bitrate candidate that fits.
- **Example** (HotD, 30 min left, 66 min episode):
  - the 11.3 GB file needs 1.96 MB/s (fe521cc measured 3.8–11×);
  - the 35.5 GB remux needs 6.2 MB/s (measured 0.3), so it doesn't fit.

**What Rivulet does today** (and why 8a is needed):
- **Closing the last reader drops the file.** `pinnedReader.Close` sets the file to priority None once its last reader closes (`internal/engine/engine.go:1765`).
- **Playing one file drops the others.** `ensureDownloading` demotes every other selected file when one is played (`:158`).
- **Idle torrents lose their data.** After 300 s (`RIVULET_TORRENT_IDLE_TIMEOUT`) the janitor removes an idle torrent and **deletes its data** (`purgeReaderless` → `beginPurge` → `os.RemoveAll`, `:722`, `:1291`).
- **The cache cap deletes whole torrents.** LRU eviction does the same, over loaded torrents only (`evict`, `:1059`).
- **So does `remove`.** The web's `remove` (race losers, warm-up expiry) also deletes data.
- **What survives:** piece completion is persisted per torrent directory (`filestorage.go:33`), so a re-added torrent keeps its verified pieces.

**Work rules:**
- Rivulet: gofmt / vet / `go test -p 1 ./...` (`GOFLAGS=-exec=…goexec.exe`, `GOWORK=off`), deadcode, dup, OpenAPI.
- stremio-web: lint, build, tried in the real app.
- Local commits only (stremio-web on `wip/streaming-research`; Rivulet local as before).
- 4K HDR, muted, stopped after each test.
- Each step ends with a report. You approve before the next step starts.

## 8a. Rivulet: download intent (the core)
**State:**
- **Registry:** a new `internal/engine/downloads.go`, a registry keyed by (infoHash, fileIdx). Each entry has `state` (queued | downloading | complete | paused), `watched`, `addedAt` and `trackers`.
- **Persistence:** JSON at `<APP_PATH>/downloads.json`, written on change.
- **On start:** torrents with incomplete entries are re-added (`EnsureEngine` with their trackers). Complete entries stay on disk unloaded.

**Engine:**
- **Background files:** a `background` set per engine. `ensureDownloading` and `pinnedReader.Close` never demote a background file. It gets `files[i].Download()` (Normal), so a playing file's readahead (High/Now) still comes first.
- **Completion:** watched via the file's bytes completed, which updates the entry.
- **Unloading a finished torrent:** when a torrent's entries are all complete and it has no readers, it's unloaded **without deleting data** (a new `unload` path: `Drop` without `RemoveAll`).

**Protection:**
- `purgeReaderless`, `evict` and `RemoveEngine` skip or keep torrents with registry entries.
- `RemoveEngine` on such a torrent stops its non-download files only.
- So a race loser that's also a download, or a warm-up timer firing, never deletes a download.

**API** (`internal/api`, OpenAPI):
- `POST /downloads` `{infoHash, fileIdx, trackers}` returns the entry;
- `GET /downloads` lists entries with name, size, bytesCompleted, rate (from the engine's speed samples) and state;
- `PATCH /downloads/{ih}/{idx}` `{paused?, watched?}`;
- `DELETE /downloads/{ih}/{idx}` removes the entry and its data unless the file is playing.

**Tests:**
- registry round trip (persist, reload);
- a background file survives its reader closing and another file being played;
- the janitor and `RemoveEngine` keep download data;
- unloading keeps the data and completion;
- API handlers.

**Not included:** plan 05 A5 (reader-close grace), no longer needed for this, since background files are exempt by design.

## 8b. Rivulet: budget and eviction
- **Setting:** `downloadBudget` in Rivulet settings, default 60 GB, counted over registry entries (allocated bytes on disk).
- **Over budget on add:** delete complete **watched** entries oldest first, then complete unwatched ones oldest first.
- **Still over:** refuse with 409 and a reason (the web shows it).
- **Deleting one episode inside a season pack:** remove that file's data file and mark completion false for pieces wholly inside it. Boundary pieces shared with a neighbouring file stay.
- **Tests:** the eviction order, the 409, deleting in a pack keeping neighbouring files intact.

## 8c. Web: automatic next episode (`src/smartPlay/ahead.ts`, plus small touch points)
**When:**
- once the current episode has played steadily for 60 s (after any start hold);
- again on stop, for the rest of the current episode;
- the episode after the next is queued once the next one completes, for season packs only (same torrent, Rivulet's `nextVideoIdx`), with lead L + D.

**Candidates for the next episode:**
- **Season pack:** if the player's `nextVideo` stream (its player link, decoded with `core.transport.decodeStream`) is in the **same torrent**, that's the candidate. Its rate is the current torrent's live rate.
- **Otherwise:** fetch the next episode's streams from your installed stream add-ons, the same requests core makes (add-on `transportUrl` + `/stream/{type}/{id}.json`). Then reuse `groupByQuality` and the remembered tier (or `fallbackTier`).

**Rates:**
- rate memory (`rateMemory.ts`) where fresh;
- otherwise preload the top 3 candidates for 30 s in the background and sample `downloaded` (the existing `preload` + `RateMeter`), then remove the ones not chosen.

**Choice:**
- the highest-bitrate candidate with rate ≥ S ÷ (L + D);
- if none in the current resolution fits, step down one resolution and apply the same rule there (the user's correction, 2026-11-18);
- if nothing fits at any resolution, nothing is queued, and a notice says why.

**Queue and notices:**
- `POST /downloads`.
- Toast: "Downloading S02E04 ahead: 11.3 GB, ready in about 25 min" (your rule on automatic decisions).
- A finished episode (ended, or past 90%) gets `PATCH watched`.

**Playback:**
- When the opened episode has a download entry, `useSmartPlay` plays that source directly (it was chosen to fit), with the toast "Playing the downloaded copy".
- The race and preload are skipped.

**Setting:** Settings → Streaming, "Download next episodes ahead", on by default, with a description.

**Tests:** offline unit tests for the fit rule and the candidate choice (pure functions); lint, build.

## 8d. Keep the server running, and the PC awake
**Rivulet:**
- **Drain mode:** `POST /downloads/exit-when-done` `{on}`. In drain mode the process exits cleanly when no download is incomplete and no reader is open.
- **Keep awake:** while any download is active, `SetThreadExecutionState(ES_CONTINUOUS | ES_SYSTEM_REQUIRED)` on Windows (a build-tagged file), cleared when idle. The screen and lock are unaffected.
- **Tests:** drain exit, the keep-awake toggle (behind an interface).

**Launcher** (`scripts/start-stremio.ps1`):
- **On start:** if our Rivulet already listens on 11470, adopt it (turn drain off) instead of killing it (`:21`).
- **On shell exit:** `GET /downloads`. If anything is incomplete, turn drain on and leave Rivulet running; otherwise stop it as today.

## 8e. Web: episode-list markers
- **States:** on disk / % · speed · time left / queued / paused, from `GET /downloads` polled every 5 s while the details page is open.
- **Before building:** two or three visual variants with previews for you to pick, as your rules ask.
- **Removal:** a row's context menu gets "Remove download" (`DELETE`).

## Verification (end of plan 08; real app, HotD, muted)
1. Play an episode. After about 60 s the toast names the next episode's source, and `GET /downloads` shows it downloading at Normal while the current one plays without new stalls (stall count vs 6h).
2. Stop mid-episode and close Stremio. Rivulet keeps running (drain), Windows doesn't sleep, and it exits once the queue is empty (log).
3. Next day / later: open the next episode. Play → playing **< 2 s** from disk, the "downloaded copy" toast, and zero `paused-for-cache` in 5 min.
4. Budget: synthetic entries over 60 GB evict watched ones first (Rivulet test plus `GET /downloads`).
5. Report bytes per hour downloaded while idle.

**Output:** `findings/25`–`29` (one per step), plan 08 / TODO / START-HERE updates, a report after each step.

## Not covered by any plan
Manual downloads (a user-chosen "Download", which would use the best healthy source). You'll elaborate them yourself after reviewing plans 1–9.
