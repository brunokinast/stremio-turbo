# 39. Plan 13 Part D: live checks of plans 12 and 11

2026-11-21. Real app, HotD S2, muted, player left after each play. Fixes in stremio-web `0a810d37a`.

| Check | How | Result |
|---|---|---|
| A1: the streams page's pick leaves a manual download alone | Added a kept `manual` record for S2E3 (0a02c4fe:3), opened its streams page, waited for the pick, left | ✅ The pick tested only the other source (e411516b:3), removed only its own test, and the record survived. With the `smartPlay.debug` trace: `likelyReady`, tier "4k dv \| hdr". |
| A2: toast and chooser | Season dialog → 405p ⬇ | ✅ "Downloading E7 of season 2 in 405p (0.5 GB) · Manage in Downloads". No ✕ or undo; the chooser is a dialog with ghost ⬇ buttons and step-down lines. |
| First-file priority in a pack | Kept E5 and E6 of the 0a02c4fe pack | ⚠️ Partly. E5 became `first` in 13 s. E6 downloaded alongside it and finished first (01:22:55), and E5 cleared `first` on completion. Priority moving between two incomplete files wasn't observed. |
| "Waiting for E1" | Same run | **Changed:** E6 moved while E5 was first, so "Waiting for E5" was wrong. It now shows only while the file gets nothing (`rate === 0`). |
| Stalled + Try another source | `/downloads` stubbed in the page (E8 720p at 14 %, rate 0) and the clock moved 11 min ahead | ✅ "14% · Stalled". The retry showed "Testing other sources…", kept 9d5cc012:7 (`picker`, `manual`) and removed the stuck one. |
| Next slot leaves your own download alone | A manual E8 record downloading (a380d54e:7) during an E7 play | ✅ The trace shows `skip: "user download"`; the record was untouched. **Note:** the first runs looked like the slot never ran. They had under 60 s of steady playback, because start-up and buffering took most of the run. The steady-play counter itself was correct (99 of 100 updates counted). |
| Plan 11: tracks after a mid-episode switch | Health stubbed (the playing file at 10 %, rate 0) → switch to another 4K source | ❌ then ✅. Wanted audio "en" and subtitles "en-US"; the new file tags English subtitles "en", so subtitles were turned off. **Fix:** match the same tag, else the same base language (`tracks.ts`, with a test). Rerun: subtitles "en" were chosen. |

## Side effects, cleaned up
- Every record the tests created was deleted: 19 records, with unkept leftovers removed by path (`DELETE /downloads/<ih>?path=`); 34 GB freed.
- The E7 test plays moved the show's Resume from S2E5 (50 min left) to S2E7. Restored, at your request, by playing E5 muted and seeking to 20.5 % (13:01 of 62:53). Resume now reads "S2 E5 · 50 min left".
- `smartPlay.debug` was set during the checks and removed afterwards.

## Driver notes (also in memory)
- From Git Bash, `appplay.mjs` needs `MSYS_NO_PATHCONV=1`; otherwise the `#/detail/...` route becomes a Windows path.
- To wait for a rebuild, grep the bundle for code, not for comments (it's minified).
