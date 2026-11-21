# 32. Port warning (R1 / 9e), and a write-handle bug that stopped a download

2026-11-19. Rivulet `ac04e10`; stremio-web `92851b0be`; anacrolix fork `6a443443` (all local on `wip/streaming-research`). Rivulet rebuilt and installed (`.prev.exe` = the previous build).

## Port warning
- **Rivulet:** `GET /network-info` gains `incoming {count, lastAt, listenPort, activeSince}`. Incoming connections are counted in `dialLog.onPeerConnAdded`; `activeSince` is the longest-loaded torrent's add time. Tests: the dial-log counter and the handler.
- **Web:** `src/smartPlay/PortCheck.tsx`, mounted once in `App.js`, polls every minute while the app is open. With torrents loaded for ≥ 5 min and 0 incoming, it shows one toast per session: "No peer could connect to you (port N). Small swarms will be slow: forward this port on your router."
- **Live, port 49881:** 38 incoming connections in the first 90 s, and no toast.
- **Live, port 49885 (not forwarded):** still 4 incoming in 5 min, probably IPv6, which has no NAT, so that port isn't fully unreachable and no toast is correct.
  - Idle torrents are purged after 5 min, which resets `activeSince`. So only playing or downloading sessions can trigger the warning, which is the intent.
- **Web logic, with a stubbed `/network-info` reply** (0 incoming, active 6 min): the toast appeared at the next poll, once.

## Bug: "file already closed" disabled a torrent's downloading (found while checking 9c's download)
- **Symptom:** the S02E05 download ahead stopped at 9.92 of 11.56 GB. 50 seeds were connected, but the torrent's request order was empty.
- **Log, 15:16:**
  - 5 pieces failed their hash check, with 22–29 peers contributing to each, and one peer was smart-banned;
  - then "writing received chunk …S02E05….mkv.part: file already closed";
  - then "default chunk write error handler: disabling data download".
- **Cause, our fork commit `8a8532ad`, which caches write handles:**
  - When a piece fails its hash, `onFileNotComplete` renames every file of the piece back to its `.part`. For a file still downloading that's a no-op.
  - But `classicFileIo.rename` first closed the cached writer of both paths, including the `.part` being written.
  - Chunk writes holding that handle failed, and anacrolix's default handler disabled data download for the torrent for good.
- **Fix (`6a443443`):** `rename` leaves the handles alone when the source doesn't exist. Upstream's mmap IO has the same pattern and gets the same guard.
  - **Test:** `TestClassicFileIoRenameMissingSourceKeepsWriter` fails without the fix with exactly the live error, and passes with it.
- **After installing:** the stuck download resumed and completed within 90 s (S02E05 promoted, 11.56 GB).
- **Open:** any other chunk write error, such as a full disk, still disables a torrent's downloading until a restart. Rivulet could re-enable it on the janitor tick. Not built.
