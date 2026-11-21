# 31. Season packs: the next episode is queued on trial, not probed (step 9c)

2026-11-19. stremio-web `0002f574c` (local, `wip/streaming-research`). Data: `data/F9c*.json`, `data/G9c*.json`.

## Measured first (run F: S02E04 playing its pack copy, the planner choosing S02E05)
- **No 4K source qualified.** The planner dropped to 1080p and queued a 3.3 GB file (1.3 MB/s) "ready in about 47 min".
- **The pack's own S02E05 (fe521cc idx 4) got 29 MB during the probe.** Its trace showed 20–27 peers and 150–170 "interested" flags, but **0 requests outstanding** for most of 40–90 s.
- **Why:** while a file of a torrent plays, Rivulet holds the torrent's other files back (`ensureDownloading`). The probe's preload primes a few pieces and asks for nothing more, so the probe measures almost nothing, whatever the swarm could deliver. Registered downloads are exempt from that, which is why the same file downloaded at 40+ MB/s once queued.
- **0a02's S02E05, complete on disk, never qualified either:** probing it downloads nothing, so it has no rate.

## Rule (`src/smartPlay/ahead.ts`)
- **A source in the torrent being played** counts as fitting **on trial**. It is queued without a probe, and the next plan runs 65 s after queueing to check its own average rate since then. If too slow, it's replaced through the usual path (slow list, toast). The toast says it's on trial.
- **A copy complete on disk** counts as fitting.
- **Other sources:** unchanged (30 s probe; only rates measured now qualify).
- **Trace:** each source carries `basis`: measured / trial / disk / slow.
- **Emulator:** `ahead_rate` in `lab/player/smartplay_emul.py`, with run F's cases in `ahead_checks`.

## Verified (run G, same setup)
- S02E05 was queued from the pack on trial at 98 s, after a 30 s probe of the remux.
- The check at 164 s measured **38.7 MB/s** average (needed: 8.9 GB in 5802 s × 0.8 ≈ 1.9 MB/s), so the download was kept. It was at 4.0 of 11.6 GB when the run ended.
- **Old probe vs actual:** 0.7 MB/s vs 38.7 MB/s.
