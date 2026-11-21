# 11. Shared path and downloads screen: decisions (walkthrough, 2026-11-19)

Collected step by step with the user; the plan is written after the walkthrough ends.

## Launch / close
- Single launcher / shortcut: deferred to the user's monorepo.
- Closing the window: every "whole" download finishes, then the server exits (PC kept awake, as today).

## One shared path
- The server keeps one record of every file on disk: origin(s), show/episode, size, bytes on disk, state, last played, watched.
- Unknown cache folders found at startup are registered as "Left over" (e.g. 15.6 GB of Peaky 1080p partials from 2026-11-15).
- Every flow asks the same thing: "want this file, for this episode, because X, start-only or whole".
- **Whole** (kept until the user deletes it on the Downloads screen): the episode played or stopped, and the **next 1** episode (not 2).
- **Start-only** (the start, the end and the resume window, done in seconds) is temporary. Candidates are deleted when they lose, or when the page is left without Play. On Play, the winner is promoted to whole, but only once it actually begins to play.
- The server never deletes whole files. Gone: the 5-min idle deletion, the 150 GB cache limit, the 60 GB download budget, the warm-up 10-min timer.
- **Low disk:** a warning when Stremio opens, with a button to the Downloads screen.

## Removed mechanisms
- **Board warm-up** is removed, with its setting. The episode page's preload is the single "get ready" behaviour for any episode not on disk (first time, or a Continue Watching item from another device). While on the page, the leader stays connected and start-only.
- **Rivulet's own next-file prefetch** is removed. Download ahead is the only next-episode mechanism. When nothing can finish in time, it asks for the chosen next source start-only instead of skipping.

## Playing
- Every play first asks the server "is a copy of this episode on disk?" (any origin), and plays it if so.
- The board poster's ▶ goes through the same path as Resume.

## Quality
- **Fallback order, shared by every flow:** the exact remembered label → the other labels at the same resolution → each lower resolution (all its labels).
- **A tag shows the match level:** "Exact match" / "Same resolution" / "Highest available" (short wording, the hover explains more).
- **Play when nothing in the current step keeps up:** a toast, drop one step in the order, race again, and repeat. Whole only once a source keeps up and begins playing.

## Downloads screen
- **List grouped by show**, episodes in order. Each row shows an origin chip, size on disk / total, state or progress, last played, and delete. Left over has its own group.
- **Sortable** (biggest first), **multi-select delete**, and a delete action per group.
- No manual download yet.

## Candidates and winner
- **Torrentio:** limit=3 → **10 per quality**. Back up the account add-on config first.
- **Candidates in every flow:** ranked by live seeders, top 3 opened (today download ahead tests the 3 largest instead).
- **Winner in every flow:** the **healthiest**, i.e. the most speed to spare relative to its file's needs, among those that keep up with the margin.
- **One margin for every flow:** keeps up = speed ≥ **1.25×** the file's need (size ÷ runtime).
- **Play measures** the top 3 for **up to 20 s**, deciding as soon as all 3 have a speed. Candidates with no data by then don't count.
- **The loader UI needs a redesign** (previews first).
- **The start hold stays:** under 2× the need, the first frame waits for 4 s of buffer (at most 20 s).
- **Loader:** a step timeline (each quality step with its result, the live one with its candidates, then Buffer and Play), with "Play leader now" and Cancel. It stays in place of the quality rows, as today.

## Made while building steps 3–6 (2026-11-20, user: "do everything, don't wait for my ok")
- **The start hold stays** (this file, above), although plan 11 Step 3 listed it as removed.
- **Torrentio limit last**, after a backup of the account add-ons; done (`findings/36`).
- **A file of the torrent being played is taken on trust** by the Next slot (the server holds it back while it plays, so a test can't measure it); the watch checks its real progress.
- **A replacement or switch when nothing keeps up** goes in only if it's faster than what it replaces.
- **A mid-episode switch deletes the old record** only when smart play alone made it and it isn't complete.
- **Mid-episode switching is shell only**; the browser fallback (reopen at the position) isn't built.
