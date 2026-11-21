# 38. Plan 13 Parts A–C: review fixes, code cleanup, design pass

2026-11-21. stremio-web `2972a49d5` (A), `5c2177e00` (C), `013017e78` (B); Rivulet `d18249c` (A9), `ca99530` (C2); go-utp `7e7fae9` (C3). All on `main`, local, not pushed.

## A. The correctness review's findings
- **A1 (critical): smart play could delete a manual download.**
  - **Cause:** the streams page's background pick tested every source not fully on disk, including a kept file still downloading, and deleted the losers.
  - **Fix:** the picker removes only the tests it added. A source that already had a kept record is measured as it is and never removed.
  - **Where the rule is applied:** losers, abort, unmount and renew (`PickResult.owned`), and in the Next slot and the mid-episode switch.
- **A2: no undo, no cancel** (your decision). The toast only informs ("… · Manage in Downloads"); downloads are managed on the Downloads screen. This also removes findings 2, 3, 5 and 8, which were in the undo and cancel code.
- **A4:** a season's race tests every episode's top source, in waves of 12 torrents. Before, it stopped at 12 in total.
- **A6:** a later pack episode waiting behind the first shows "Waiting for E1" instead of Stalled.
- **A7:**
  - In-flight test adds are awaited before cleanup.
  - Cleanup removes only what the job added.
- **A9 (Rivulet):** a per-engine lock is held across the check and the priority set. Before, a pause could leave a file at High priority. A test pauses the file between the check and the set.
- **The chooser** is always a dialog (your choice). A row that doesn't fit the free space says why under it.

## C. Code quality and upstream standards
- **One source pipeline** (`addonStreams.ts`) instead of four copies.
- **Shared hooks:**
  - `useSteadyPlayback`;
  - `usePlayerSmartPlay`, so `Player.js` has one touch point.
- **One formatter** (`format.ts`, "–" for unknown everywhere).
- **Upstream helpers reused:** `getEpisodeLabel` ("S2 E5") and `routesRegexp`.
- **The Downloads screen is split up:** `DownloadRow`, `DownloadGroup` and shared pieces; the screen went from 488 to 262 lines.
- **Removed:** dead server helpers.
- **Comments and headers:** comments give the reason instead of pointing to these notes, and new files carry the copyright header.
- **New tests** for the pure modules: Jest 118 at the end of C, 121 now.
- **Rivulet:**
  - one atomic file write (`writeFileAtomic`);
  - declared capability interfaces instead of type assertions;
  - the internal "leftover" kind is private.
- **Skipped, with reasons:**
  - moving the diagnostics package would expose engine internals, and the traces are in daily use;
  - splitting `New()` would be churn;
  - a generic store helper adds little (KISS);
  - `Button` on Downloads rows would break the rows' Enter and blur behaviour;
  - the remaining exhaustive-deps disables are deliberate and commented;
  - the two resolution parsers take different inputs.
- **Kept for PR time:** history shaping, `go.mod` replaces, the translations route and fork-only files.

## B. Design pass
- Colours come from theme variables (`colors.less`).
- Visible keyboard focus on the Downloads screen.
- "Loading downloads…" instead of a flash of "unreachable".
- Storage bar with an inline legend.
- One ghost ⬇ on both screens.
- The racing panel shows the leading step only, with too-slow steps folded into one line.
- Toasts: the decision in the title, the reason under it; no toast for a plain pick.
- One vocabulary ("needed speed", "Fast enough" / "Too slow", "Play fastest now", "Delete", "min left").
