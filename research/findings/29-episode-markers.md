# 29. Episode-list markers (step 8e)

2026-11-18. stremio-web `90392412d`. The user picked "Chip in the meta line" from three variants.

- **Chip:** each episode downloaded ahead shows its state after the date: `⤓ On disk`, `⤓ 42% · 3.1 MB/s · 12 min`, `⤓ Queued` or `⤓ Paused`.
- **Data:** polled from `GET /downloads` every 5 s while the list is shown (`src/smartPlay/episodeDownloads.ts`). The episode-to-source mapping is the web's `smartPlay.ahead` memory.
- **Menu:** the row's right-click menu gets "Remove download" (`DELETE`).
- **Live (HotD S2):** S02E04 and S02E05 showed "On disk". A download added for S02E06 showed "2% · 44.6 MB/s · 4 min". "Remove download" removed it (entry and file) and left the pack's other episodes on disk.
- Screenshot: scratchpad `markers.png`.
