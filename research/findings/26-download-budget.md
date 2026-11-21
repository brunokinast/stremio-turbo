# 26. Download budget and eviction (step 8b)

2026-11-18. Rivulet `a2919ab` (local on `main`).

## What was built
- **Setting:** `downloadBudget` (bytes; null = unlimited; default 60 GB), in the OpenAPI settings schema.
- **On add:**
  - a new download waits up to 30 s for its torrent's metadata (503 otherwise), since the file's size is needed;
  - when it doesn't fit, completed downloads are deleted: **watched first, then unwatched, oldest first** within each;
  - incomplete downloads and torrents being read are never deleted;
  - if it still doesn't fit, the add is refused with 409 and the numbers.
- **One episode of a season pack:** removing it deletes just that file:
  - the torrent is unloaded, the data file deleted, and the pieces wholly inside it marked incomplete in the piece-completion database (now exposed by `newFileStorage`);
  - boundary pieces shared with a neighbour stay;
  - the torrent reloads for its other downloads;
  - the registry keeps each file's path and inner piece range, so this works while the torrent is unloaded.

## Tests
`downloads_budget_test.go`: the eviction order, incomplete downloads kept, the refusal, an unlimited budget, and deleting one file of a pack (its file and its pieces' completion gone, the neighbour intact). The pack test had to use full-size sparse files: a wrong size made the storage move the neighbour back to `.part` on reload.

## Live
Removing S02E06 of the HotD pack from the episode list (8e) deleted that file only. S02E04 of the same pack stayed complete on disk.
