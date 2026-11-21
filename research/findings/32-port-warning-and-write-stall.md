# 32. Port warning (R1 / 9e), and a download stalled by a closed write handle

2026-11-19.
- Rivulet `ac04e10` plus the rebuild with anacrolix `6a443443` (installed in Tools; `.prev.exe` = the build before it).
- stremio-web `92851b0be`.
- All local commits.

## Port warning
- **Rivulet:** `GET /network-info` gains `incoming {count, lastAt, listenPort, activeSince}`.
  - The count is bumped for every accepted incoming connection (`dialLog.noteIncoming`).
  - `activeSince` is when the longest-loaded torrent was added.
  - Tests: `TestDialLogIncomingAndActiveSince`, `TestHandlerNetworkInfoIncoming`.
- **Web:** `src/smartPlay/PortCheck.tsx`, mounted once in `App.js`, polls every minute. If torrents have been loaded ≥ 5 min with 0 incoming, it shows one toast per session: "No peer could connect to you (port N). Small swarms will be slow: forward this port on your router."
- **Live:**
  - **Port 49881 (forwarded):** 38 incoming in the first 90 s.
  - **Port 49885 (not forwarded):** still 4 incoming within 3 min, most likely over IPv6, which has no NAT. So an unforwarded IPv4 port isn't "unreachable" when IPv6 works, and the warning correctly stays quiet.
  - **Idle purge:** preloaded torrents are purged after 5 min, which resets `activeSince`. Only playing or downloading torrents can trigger the warning, which is the intent.
- **Web logic** checked with a stubbed reply (0 incoming, active 6 min): the toast came at the next poll (43 s), once.

## Bug found on the way: a download ahead stopped at 9.92 of 11.56 GB
- **Symptom:** S02E05 (HotD pack, queued in 9c run G) sat at 0 MB/s for 35 min, with 50 seeds connected and an empty piece request order.
- **Log at 15:16:**
  - 5 pieces failed their hash (22–29 peers contributed to each) and one peer was smart-banned;
  - then "writing received chunk …S02E05…mkv.part: **file already closed**";
  - then "default chunk write error handler: **disabling data download**" for the torrent.
- **Cause (our fork, `8a8532ad`, which caches write handles):**
  1. A failed piece is marked not complete, and `onFileNotComplete` renames each of its files from the final name back to `.part`.
  2. For a file still downloading there is no final file, so the rename is a no-op.
  3. But `classicFileIo.rename` first closed the cached writers of both paths, including the `.part` being written.
  4. In-flight writes on that handle failed, and anacrolix's default handler stops the torrent's data download for good.
- **Fix (anacrolix `6a443443`):** `rename` returns early, closing nothing, when the source doesn't exist. Applied to the classic IO and to upstream's mmap IO, which has the same pattern.
  - **Test:** `TestClassicFileIoRenameMissingSourceKeepsWriter`. It fails without the fix with the same "file already closed" error, and passes with it.
- **Live:** after installing the fixed build, S02E05 finished within 90 s of restart (11.56 GB, promoted, unloaded).
- **Open, not built:** any other write error (e.g. disk full) still disables a torrent's download until restart. Rivulet's 30 s download tick could re-enable it, but that's only worth doing if such errors show up.
