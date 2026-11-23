# 36. Plan 11 steps 3–5: one picker, the Next slot and the watch, switching mid-episode

2026-11-20. stremio-web `48189c869` (step 3), `d674f1ad6` (step 4), `44ea71d37` (launcher pprof), `887a8de33` (step 5), all on `main`, local, not pushed. Rivulet unchanged (still `a7d8994`). The user said "do everything, don't wait for my ok", so steps 3–6 ran without a stop between them.

## Step 3: one health rule, one quality order, one picker (episode page)
- **`health.ts`**: need = bytes still missing past the start point ÷ seconds until playback reaches the end. Healthy = rate ≥ **1.25×** need. A file all on disk needs 0 (always healthy).
- **`candidates.ts`**: the order every flow follows: exact label → the other labels at the same resolution → each lower resolution. Each step holds its top 3 by live seeders; a copy on disk is always among them. A remembered label that isn't offered starts at its resolution.
- **`picker.ts`** (plus `pickDecision.ts`, pure):
  - Tests a step's sources start-only as **temporary records** (POST `/downloads`, renewed every 30 s; the lease is 2 min), sampling `/stats`.
  - Decides once all of them have a live rate, or at 20 s. The healthiest healthy one wins; a copy on disk wins at once; losers are deleted as soon as the step is decided.
  - None healthy: a toast, then the next step. No step healthy: the healthiest seen plays, with a toast saying so.
- **Episode page:**
  - The pick for the likely quality starts in the background 2 s after the ranking holds still; Play uses its result, or shows the loader while it runs.
  - **Loader:** a step timeline with match tags, the candidates' size, seeders and ×-need, plus "Play leader now" and Cancel.
  - Leaving the page deletes its tests. A step down doesn't overwrite the remembered quality.
- **Removed:** the race (`race.ts`), the preload and its dwell meter, the downloaded-copy check, and the board warm-up with its setting. **Kept:** the start hold (`startBuffer`), per the decisions file.
- **Board ▶** goes the Resume way (`requestAutoplay` + the episode's streams page).
- **Live** (HotD S2E6, nothing on disk, player navigation intercepted):
  - The background pick tested three 4K DV/HDR candidates as temporary records.
  - On click, the loader showed "4k DV | HDR: best 1.2×" (just under the 1.25 margin) → 4k "best 0.6×" → 1080p, whose leader was at 11.2×. The two step-down toasts were shown.
  - Peaky S06E02 from the board ▶ played the RAZORS copy from disk with no test.
- **Jest:** the repo had no TypeScript transform, so `tests/tsTransform.js` (the repo's own `typescript`) and `jest.config.js` were added. 16 tests for health, the order and the decision.

## Step 4: the Next slot and the watch (replaces `ahead.ts`)
- **`next.ts`:**
  - After 60 s of steady playback, the next episode is picked by the same picker. The time it has = what's left of this episode + its runtime.
  - The winner is kept with origin `next`.
  - A file of the torrent being played is **taken on trust**: the server holds a playing torrent's other files back, so a test can't measure it. The watch checks its real progress instead.
- **`watch.ts`** (pure, Jest-tested):
  - The rate comes from a record's bytes over a sliding 30 s window, needing at least 15 s of samples. A stall reads 0 (RateMeter only counts intervals that moved, so a stalled source would keep its old rate).
  - Unhealthy 30 s in a row → re-pick. A healthy check resets the count; an unknown one leaves it as it is.
- **Replacement:**
  - A new pick that skips the slow torrent. When nothing keeps up, a replacement goes in only if it's faster.
  - The new record is kept, the old one deleted, and a toast says so.
- **`notify.ts`:** smart play's toasts in one place.
- Trace: `sessionStorage smartPlay.nextTrace`.
- **Bug found live:** `useNotify()` returned a new object on every render, so the 10 s interval was re-created by every player time update and never fired. The trace stayed empty for 40 s of playback. Fixed: a memoized return, and the interval reads `fill` through a ref.
- **Live** (HotD S2E5 4K, muted):
  - About 75 s in, S2E6 was queued as kept/`next` from the same season pack (trusted). The watch then read 35 MB/s against 1.6 MB/s needed.
  - Forced replacement: the page's `/downloads` was stubbed to freeze that record's bytes. The watch went unhealthy at 02:46:58 and replaced the record at 02:47:28 (exactly 30 s later) with 0a02c4fe (2.1 GB 4K DV/HDR) at 5.4×. The old record was deleted and the toast shown.
- Not re-run: "closing the window finishes kept entries and the server exits", which is server behaviour unchanged since `findings/28`.

## Step 5: switching sources mid-episode
- **Measured first** (`lab/player/mpvplay.py switch=AT:IH:IDX`, libmpv with no audio, `remove=0`), on two releases of HotD S2E6 4K DV/HDR:

  | Run | From → to | At | Gap (command → playback-restart) | Position before → after | Durations | Stalls |
  |---|---|---|---|---|---|---|
  | A | 0a02c4fe (2.1 GB) → fe521cc3 (12.4 GB MAX WEB-DL) | 10:30 | **0.37 s** | 630.088 → 630.129 | 4033.6 / 4034.6 s | 0 |
  | B | fe521cc3 → 0a02c4fe | 30:30 | **0.26 s** | 1830.078 → 1830.120 | 4034.6 / 4033.6 s | 0 |

  Well under the 3 s target. The durations differ by 1.0 s, so the two timelines can be up to about 1 s apart; content alignment wasn't checked frame by frame (`vo=null`).
- **`nowWatch.ts`** (player, shell only):
  - Every 10 s it computes the health of the file playing, from its record and the position.
  - Unhealthy 30 s → the picker runs from the current position, skipping that torrent.
  - The winner is loaded with `mpv-command loadfile <url> replace -1 start=+<s>` over the shell transport, the same command ShellVideo loads with. The audio and subtitle tracks are then chosen again by language.
  - ShellVideo doesn't treat the old file's `end-file` as the end of the episode: its early-EOF guard sees the position far from the end.
  - Not in the first 60 s of playback (the start hold covers that) nor in the last 3 min.
  - The new file becomes kept when it opens. The old record is deleted 5 s later, only if smart play alone made it (`picker`/`play`/`next`) and it isn't complete.
  - Trace: `sessionStorage smartPlay.nowTrace`.
- **Live** (HotD S2E5 4K, muted; the page's `/downloads` stubbed to show the playing record stalled at 10%):
  - Unhealthy from 02:58:57; switched at 02:59:27 with the toast "Switched to 1.7 GB 4k DV | HDR at the same point … (0.0× … the new one 34.8×)".
  - The clock ran on without a visible gap (12:43 → 12:55 in 12 s). The new file (0a02c4fe:5) was recorded as kept/`play`.
  - That run deleted the old record, a complete 11.5 GB fe521cc3 copy downloaded by this session's own tests: the stub hid that it was complete. This led to the guard above (no delete of complete or user-chosen records).
  - Track re-selection by language wasn't checked live.
- **Fallback outside the shell** (reopen the player at the position) isn't built: the app only runs in the shell. In a browser, nowWatch does nothing.

## Torrentio limit 3 → 10 (account change)
- **Backup:** the 7 add-ons were exported to a local file first.
- **Change:** installed `https://torrentio.strem.fun/limit=10/manifest.json` through core, then uninstalled the `limit=3` entry. Core keys add-ons by URL, so the install added a second copy rather than replacing it.
- **Measured:**

  | | Streams (HotD S2E6) | 4K DV/HDR | Response time |
  |---|---|---|---|
  | limit=3 | 24 | 3 | 0.28 s |
  | limit=10 | 61 | 8 (plus a "4k HDR" tier) | 0.29 s |

  Rivulet's `/scrape`: limit 3 gave 25 torrents (median update 0.8 s), limit 10 gave 61 torrents (median update 0.4 s). Both streams end at 10.0 s. The page's live counts settle in **9.9 s**, unchanged.

## Other findings
- **`/downloads` and `/stats.json` hung once** (> 20 s, while `/settings` answered) during the first step 4 play, after the picker's deletes and a promotion. No goroutine dump was possible (pprof off), and it didn't recur in 10 min of the same flow with pprof on. The launcher now always sets `RIVULET_PPROF=127.0.0.1:6060`, so the next one can be dumped (`/debug/pprof/goroutine?debug=2`).
- **Upstream: stale streams on the details page.** When a details page that's already open is moved to another episode by a bare URL change, it keeps the previous episode's streams. `Details.tsx` snapshots them while `directVideo` is set; v5.2.0 still does this. Smart play then correctly shows no quality rows, since every source names another episode.
  - The app's own flows don't hit it: episode clicks pass `fromVideos`, and the board ▶ and next-episode autoplay mount the page fresh.
  - Seen only through test navigation, so not changed.
