# 41. Plan 13 Part F: plan 9 closed

2026-11-21. Rivulet `a363c93` (giving back).

## Giving back: upload capped, only while downloading
- **Before:** Rivulet seeded with no cap (`cc.Seed = true`, upload limiter at ∞), for as long as a torrent stayed loaded, finished or not.
- **Already measured (findings/30):**
  - Uploading doesn't buy speed on these swarms: the deliverers were seeds, which don't trade.
  - It costs this line nothing: 11 MB/s up with no queueing.
- **Your rule:** a small cap, only while downloading, no UI.
- **Built:**
  - With seeding enabled, upload is capped at 1 MiB/s, client-wide. findings/30's uncapped median was 1.5 MB/s.
  - A torrent uploads only while it still wants data: metadata pending, or a downloading file (priority above None) incomplete.
  - This goes through the existing seed-ratio gate (`applySeedRatio`, one transition per change, logged), so the two rules can't fight.
  - A finished torrent stops uploading.
- **Tests:** the gate's transitions, including "nothing to download → no upload" and back.
- **Not measured:** whether the cap changes unchoke time. findings/30 says it can't make downloads slower here, and the cap only lowers what we give.

## Merged swarms: closed, not built
- **Measured** (`data/merged-swarms.py`, Torrentio limit 10, HotD S2 E1–E8 and Peaky S6 E1–E6):
  - HotD: one 4K file per episode is in 2–4 torrents (the same AMZN/NOW WEB-DL release in packs), with 0–2 seeders per copy.
  - Peaky: no shared 4K file; only 400p/480p copies.
- **Why not built:** merged, those copies would reach at most about 4 seeders. The 4K sources the picker leads with have 20–90, so a merged swarm would never change a pick.

## Deadline scheduler: closed
The overdue steal (findings/40) leaves 1.1–1.3 s of stuck-at-head time per 2-minute play on a well-supplied source, against 4–22 s without it, and no stalls. The late critical chunks that reopened this item are handled.

## Also closed
- **Source choice by supply:** done by plan 11's picker (rate ÷ need).
- **Peer memory:** skipped (useful peers connect in, findings/30).
