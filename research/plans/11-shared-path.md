# Plan 11: one shared path for downloading and playing, plus a Downloads screen

## Context
- **The problem:** the work in plans 6–9 grew six separate download mechanisms:
  - board warm-up;
  - preload;
  - race;
  - playback;
  - Rivulet's next-file prefetch;
  - download ahead with its tests.
- **Each has its own trigger, its own rule for "fast enough", and its own cleanup.** As a result:
  - the episode you stopped is deleted after 5 min, or kept forever if the server was killed (15.6 GB of Peaky partials from 2026-11-15 are on disk now, invisible);
  - Play and the next episode judge sources by different rules;
  - Resume can race past a copy that's already on disk.
- **The user walked through the whole flow and chose a simpler model.** Decisions are recorded in `streaming-research/plans/11-shared-path-decisions.md`.
- **Process rules, as before:**
  - one step at a time, each ending with a report and the user's OK;
  - local commits on the `wip/streaming-research` branches;
  - 4K HDR muted tests, with playback stopped after;
  - UI choices shown as previews first;
  - i18n for user-facing text.

## The model (what every step builds toward)
1. **Two wanted slots.**
   - **Now:** the episode whose page is open, or that is playing.
   - **Next:** the episode after Now, while Now plays.
2. **Health, one formula.**
   - need = bytes still missing ÷ seconds until playback reaches the end of the file;
   - healthy = speed ≥ **1.25** × need.
   - On disk → healthy. Partly downloaded data lowers the need automatically.
   - The 24 h speed memory stays, as part of health.
3. **The picker, one procedure:**
   1. Build candidates: Torrentio 10 per quality, top 3 by live seeders.
   2. Order the qualities: exact label → same resolution → each lower resolution.
   3. Test start-only at the start point, up to 20 s per step.
   4. The healthiest healthy candidate wins. None → a toast, step down, repeat.
4. **Storage, two kinds.**
   - **Kept:** the source chosen for an episode. It downloads whole, finishes after the window closes, and only the user deletes it.
   - **Temporary:** test candidates. Deleted when the test ends, unless chosen.
   - Leftovers from earlier runs show as "Left over".
5. **One watch** keeps Now and Next healthy.
   - Unhealthy for ~30 s → the picker runs again.
   - For Now, the winner is swapped in the player at the same second.
   - For Next, the download is replaced.
   - The source left behind is deleted.

## Background actions get a toast (user request)
Everything that happens without its own UI shows a short toast. All toasts go through one `notify(event)` helper (`src/smartPlay/notify.ts`, i18n keys) with a per-event cooldown, so repeats don't spam.

| Event | Example toast |
|---|---|
| Page picker done | "Ready: 11.5 GB 4k DV \| HDR (5.0× its need)" |
| Step down | "4k DV \| HDR can't keep up (best 0.9×): trying 4k HDR" |
| Next queued | "Downloading S02E06 in 4k DV \| HDR, ready in ~12 min" |
| Next replaced | "S02E06's download was too slow, switched to the 9.9 GB source" |
| Next done | "S02E06 is downloaded" |
| Now switched | "Switched to the 9.9 GB source: the previous one couldn't keep up" |
| Kept downloads still running on close | Shown on the next start: "Finished 2 downloads while Stremio was closed" |
| Temporaries cleaned | No toast |
| Leftovers found on start | "Found 15.6 GB of old data: review in Downloads" (once, with a button) |
| Low disk | Toast with a Downloads button |

The existing quality-change and port toasts move to the same helper.

## Removed
- **Board warm-up** (and its setting).
- **Rivulet's `prefetchNext`.**
- **The start hold** (`startBuffer.ts`).
- **Separate test logic:** the race, the preload meter, and download ahead's probes, trial and 4-min replace.
- **Two rules replaced by one:** the 1.0× rule and the 0.8 rule.
- **The "downloaded copy" check.**
- **Server deletion of anything kept:** the 5-min idle deletion, size-based cache eviction, and the 60 GB `makeRoom` budget.

---

## Step 1: Rivulet, one record of everything on disk
**Files:** `internal/engine/downloads.go` (grows into the record), `engine.go`, `internal/api/downloads.go`, `internal/types/types.go`, `internal/api/openapi.json`, `internal/app/app.go`.
- **Entry fields:** each `(infoHash, fileIdx)` gets:
  - `kind`: kept or temporary;
  - `origins[]`: picker, play, next, manual-card, left over;
  - `meta {metaId, videoId, type}`;
  - `name`, `size`, `bytesOnDisk`, `state`, `rate`, `lastPlayed`, `addedAt`, `watched`.
- **Persistence:** extend `downloadEntry` and `downloads.json`; existing entries become kept.
- **API, generalised from `/downloads`:**
  - `PUT /downloads/:ih/:idx {kind, origin, meta, startFraction}`, idempotent: a temporary entry can be promoted to kept;
  - `DELETE` (removes data);
  - `GET /downloads` lists everything, including leftovers, plus `disk {free, total}`.
- **Temporary entries have a lease.**
  - **Refresh:** the app re-PUTs every 30 s.
  - **Expiry:** deleted after 2 min without a refresh, and at startup.
  - **Why:** a closed or crashed app can't leave test data behind. This is the one server-side cleanup, and it covers temporary data only.
  - A temporary entry primes the start, the end and the window at `startFraction` (reuses `PrimeFile`).
  - A kept entry downloads whole (reuses `startDownload`).
- **Leftovers:** at startup, cache folders with no entry are registered as `origin: leftover`.
  - **Names:** the file paths inside the folder.
  - **Size on disk:** the allocated size (`GetCompressedFileSizeW` on Windows, `st_blocks` elsewhere).
  - They don't download.
- **Remove:** the deleting branch of `evictIdle` and `evictTick`.
  - **Idle torrents are unloaded without deleting:** reuse `unloadLocked`, keeping peers and RAM bounded.
  - **Also removed:** `makeRoom`, `SetDownloadBudgetFn`, and the `prefetchNext` call (engine.go:1556).
  - **Stays:** a plain `/stream` with no entry (e.g. an external player) still works. Its data is registered as origin `play` and kept, so it shows on the screen.
- **No more holding back wanted files:** `ensureDownloading`'s demotion skips files that have an entry, so a test of another file in the playing pack measures truly. That removes the need for the 9c "trial".
- **Unchanged:**
  - exit-when-done finishes **kept** entries that are incomplete;
  - keep-awake.
- **Tests:**
  - lease expiry;
  - promotion;
  - leftover scan, including a folder with no entry;
  - no deletion on idle;
  - demotion skips entries;
  - API and OpenAPI.

  Run `go test -p 1 ./...` with goexec, plus gofmt and vet.
- **Verify live:** the Peaky 15.6 GB shows as a leftover in `GET /downloads`, and nothing is deleted after 6 min idle.

## Step 2: the Downloads screen (stremio-web)
**New:** `src/routes/Downloads/` (route `#/downloads`, registered in `src/routes/index.js` and the router config), and a nav entry. Its placement is shown as **2–3 previews first**: the side nav icon vs the account menu.
- **Layout (chosen):**
  - **groups by show,** episodes in order; Left over last;
  - **header:** total on disk and free space;
  - **filter chips:** Downloading / All / Left over;
  - **sort:** size, last played, added.
- **Row:** episode (S02E05), size on disk / total, state (✓ complete / % · MB/s · min left / idle), an origin chip, last played, and a checkbox. Group header: total, select all, delete.
- **Bulk bar:** "Delete selected (N, X GB)", with a confirm dialog that names the size.
- **Names:** show names and posters come from the core library (`metaId`). Leftovers show their file names.
- **Low disk:** under 10% free or under 20 GB (default, said in the warning):
  - on app start, a toast with a "Downloads" button (mounted next to `PortCheck` in `App.js`);
  - a banner on the screen.
- **Reuse:** `episodeDownloads.ts` polling becomes `useDownloads()`, one shared hook for the screen and the episode chips (Video.tsx).
- **Verify:** lint, build, and the real app: delete one Peaky leftover, check the disk is freed, and check the totals.

## Step 3: shared health and picker, with Now on the page and Play
**New:** `src/smartPlay/health.ts`, `candidates.ts`, `picker.ts`.
- **health.ts:** `need(missingBytes, secondsUntilEnd)`, `isHealthy(rate, need)`, `MARGIN = 1.25`. Reuses `RateMeter` and `rateMemory` (from `supply.ts`, `rateMemory.ts`).
- **candidates.ts:** the quality order, reusing `groupByQuality`, `rankTiers`, `resolutionOf` and `fallbackTier`. Returns the steps with a match tag ("Exact match" / "Same resolution" / "Highest available"), each holding the top 3 by live seeders.
- **picker.ts:** `pick({steps, startFraction, secondsUntilEnd, signal, onStep})`.
  - Tests one step: PUTs temporary entries and keeps their lease, samples stats, decides once all 3 have a rate or at 20 s.
  - Steps down with a toast, deletes the losers, and returns `{source, step}`.
  - On-disk candidates have need 0 and win at once.
  - Built from the useful parts of `race.ts` and `supply.ts`, which are then deleted.
- **useSmartPlay → `useNowSlot`:**
  - **on page open:** the picker runs for Now in the background;
  - **on page leave:** its temporaries are deleted;
  - **Play** uses the result, or shows the loader while it finishes;
  - **once the winner plays,** it's PUT as kept with meta.
- **Loader:** a step timeline in place of the quality rows (`QualityRows.tsx` racing branch).
  - Done steps, the live step with its 3 candidates (size, seeders, ×health), then Buffer and Play.
  - Buttons: "Play leader now" and Cancel.
- **Board poster ▶** (`LibItem.js` `onPlayClick` for Continue Watching): goes through `requestAutoplay` + the streams page, i.e. the Resume path.
- **Remove:** `warmup.ts`, its setting row in `Settings/Streaming`, and `startBuffer.ts` with its use in `Player.js`.
- **Source card:** clicking a specific card plays it directly, PUT as kept with origin `manual-card`.
- **Torrentio limit 10:** back up the account add-ons (`stremio-addons-backup-*.json` pattern), then change `limit=3` → `limit=10`. Also check the live seeder count over 10 per quality stays quick (time to settled).
- **Verify:**
  - lint and build;
  - Jest tests for health, candidates and the picker decisions;
  - real-app 4K HDR plays: on disk → instant; dwell → instant; cold → loader → plays;
  - a forced step-down, by stubbing the rates.

## Step 4: Next slot and the watch (replaces `ahead.ts`)
- **`useNextSlot` in Player.js** (replaces `useDownloadAhead`):
  - when Now plays, the picker runs for Next with secondsUntilEnd = time left in Now + Next's runtime;
  - the winner is PUT as kept with origin `next`.
- **`useWatch`:** every 10 s, health for Now (from the position and its entry's bytes) and for Next.
  - Unhealthy for 30 s → the picker runs again.
  - For Next: the new winner is PUT and the old entry deleted, with a toast.
- **Unchanged:**
  - **watched past 90%:** PATCH watched (shown on the screen, deletes nothing);
  - **the setting:** "Download next episode", now 1 episode.
- **Verify:**
  - Jest for the watch's timing;
  - a live 4K run: Next queued, a stubbed slow rate is replaced, closing the window finishes kept entries, and the server exits.

## Step 5: switching sources mid-episode
- **On Now unhealthy for 30 s:** the picker runs with startFraction = current position.
- **The swap:** through the shell's mpv `loadfile <url> replace start=+<seconds>`, the same command `ShellVideo` uses on load.
  - **How:** a new player action in `Player.js` that sends `mpv-command` over the shell transport, then re-applies the selected audio and subtitle tracks by language.
  - **Fallback when not in the shell:** reopen the player at the saved position.
- **After the swap:** a toast names the change and why. The new source becomes kept and the old entry is deleted.
- **Measure first** (diagnostic): on 2 releases of one 4K episode, check that the gap is ≤ 3 s and the timelines align. If not, fall back to the reopen path.
- **Verify:** live, force a switch by throttling the playing source (stubbed health) and check the gap, the tracks and the toast.

## Step 6: close-out
- **Findings:** `findings/34` onward, one per step.
- **Update:** TODO, START-HERE and memory (stremio-custom-fork decisions).
- **Open items carried over:**
  - the overdue-steal fix (committed in the anacrolix fork, not installed; it needs a well-supplied source A/B);
  - R5 (stall pause count);
  - R6 (404s);
  - R7 (language filter).

## Verification summary
| Step | Checks |
|---|---|
| 1 Rivulet | `go test -p 1 ./...` via goexec, gofmt, vet; live check that leftovers are listed and nothing is deleted on idle |
| 2 Downloads screen | `corepack pnpm run lint` and `build`; real-app delete and totals |
| 3 Picker on the page | lint, build; Jest; real-app 4K HDR plays (instant / dwell / cold / forced step-down) |
| 4 Next and the watch | Jest; live 4K run, including close → finish → exit |
| 5 Switching | gap ≤ 3 s, tracks re-applied, toast |
| Rivulet installs | `Tools\rivulet\rivulet-engine.exe`, with Stremio closed and a backup of the previous build |
