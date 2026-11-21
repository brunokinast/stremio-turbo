# Plan 11, step 2c: every file is complete, moving or stopped

Follows 2b (the Downloads screen with quality, duplicates, seasons; uncommitted).

## Why
- The screen shows "Partial" rows with no % (and, in the demo, "Partial · 100%"). The cause: Rivulet
  forgets a torrent's file list when it unloads it (finished, idle, restart), so for that data it
  only sees bytes on disk, never the file's size. The data is real; its file list was thrown away.
- "Left over" folders are the same thing one step further: data with no record at all.
- Your model: a file is **complete** (nothing shown), **moving** (purple bar + %), or **stopped**
  (grey bar + %, resumable). No "partial", no "left over".

## A. Rivulet keeps every torrent's file list
- When a torrent's metadata is known, Rivulet saves it as `<cache>/<infoHash>.torrent` (~100 KB),
  beside the data folder (not inside it, so it never counts as data or a file row).
- Loading a torrent uses the saved file: metadata at once, no swarm lookup (resume and replay start
  faster too).
- **Old data without one** (your S02E03 partial, the leftover folders): a background job fetches the
  metadata from the swarm (records' trackers + DHT), one torrent at a time, 2 min each, saves it, and
  unloads the torrent unless something wants it. Until then its rows say "Reading file info…".
  A torrent nobody answers for keeps its rows as "No file info" (delete only); the job retries at
  each start.
- Deleting a torrent's last data deletes its saved file list too.

## B. One kind of row for data nobody asked for
- Leftover folders and partials merge into one: **unkept** files (data on disk, no record), listed
  per file from the saved file list, with their real size and % (bytes on disk, via the piece
  completion store when it has them).
- A folder whose file list isn't known yet is listed as one row per data file, "Reading file info…".
- `kind` values: `kept`, `temporary`, `unkept`. `leftover` and `partial` go; both forks change
  together, so no compatibility layer.
- Resume of an unkept file = record it as kept (origin `resume`); it finishes in the background.

## C. The screen
- **States:**
  - complete: nothing in the state column;
  - moving (purple bar): downloading ("46% · 18 MB/s · 6 min · 31 peers"), playing ("Playing · …"),
    testing ("Testing · …"), queued ("Connecting…", 0% or what it has);
  - stopped (grey bar): paused by you, or unkept ("31%");
  - unknown size: "Reading file info…" (grey, no bar) or "No file info".
- **Buttons:** moving → Pause + Delete; stopped → Resume + Delete; complete → Delete; tests →
  Delete. Delete on a moving file asks "Stop and delete?". A playing file can't be deleted (hover
  says why). The ✕ cancel button goes; delete is the bin everywhere.
- **Groups:** leftovers join their shows (matched by name, then Cinemeta like the rest). Files that
  match no show go to an "Unknown" group, last. The "Left over" group, its filter and the
  "Delete left over" button go.
- **Filters:** All / Downloading / Stopped.
- The low-disk and "old data found" toasts: the second one goes (no leftovers any more); unkept data
  is just stopped files on the screen.

## Verification
- Rivulet tests: saved file list written and reused (no metadata wait on reload); an unkept file
  listed with its real size and %; resume records it kept; deleting the last data removes the file
  list; the fetch job saves a list for a folder that has none (test torrent with a local seeder).
- Live: S02E03 (fe521cc3) shows 10.9 GB of its real size with a %, grey, resumable; the Peaky 1080p
  folder shows its episodes under Peaky Blinders after the fetch, each with %.
- Web: lint, types, build; the demo with every state; real app.
- Close-out: commits (2 + 2b + 2c) on both `main`s, findings/35.
