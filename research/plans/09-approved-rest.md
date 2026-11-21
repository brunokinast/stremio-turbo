# Plan 09, the rest: 9c (season-pack rate) and 9e (port warning)

## Context
- **9a** found that the reachable listening port decides thin swarms (findings/30): qBittorrent got 0.03 MB/s on an unforwarded port and 7.7 MB/s on 49881.
- **9b** closed with no code: the line uploads 11 MB/s with no added delay.
- **9d** was dropped: the useful peers connect to us, and we can't reach them.

Two steps are left, one at a time, each ending with a report and your OK.

## 9c. Season packs: measure the next episode correctly
**The problem:**
- In run E (findings/27), the probe of the pack's S02E05 measured 0.7 MB/s, so the planner chose the 1.7 GB file. The pack later downloaded at 44.6 MB/s.
- `measure()` in `src/smartPlay/ahead.ts:217` samples `stats.downloaded` for the **whole torrent**. When the candidate is in the torrent that's playing, that number mixes the player's readahead with the probe. The readahead is limited by what mpv asks for, so the number says nothing about what the swarm could deliver.
- Rivulet also demotes a torrent's other files while one plays (`ensureDownloading`, engine.go:158), so the probe of the other file may get little of the swarm.

**First, measure (no product code):**
- One real-app run on the HotD pack: play S02E05 and let the planner probe S02E06.
- Read Rivulet's trace `fileBytes` (useful bytes per file per second, already recorded while a reader is open). Compare them with the torrent-wide number the planner used.
- **What it settles:** whether the probe's file got bytes at all, and how much went to the playing file. The fix depends on the answer.

**Fix (expected; confirmed by the measurement):**
- **Skip the probe when the candidate is in the playing torrent.** Queue it straight away as a download (Normal priority, below playback). Then judge it by its own progress, `bytesCompleted` per file from `GET /downloads`, which is supply-limited because a download wants every byte.
  - The first check comes after **60 s** for this case, instead of 4 min, since its rate is clean.
  - If it can't finish in time, it is replaced through the existing path: `slow` list, step down a resolution, toast.
- **Other sources keep the 30 s probe.** They are separate torrents, so `stats.downloaded` is theirs alone.
- **Files:** `src/smartPlay/ahead.ts` (same-torrent branch in `plan()`, the check delay). Reuse `listDownloads`/`addDownload` (`downloads.ts`), `fitsWindow`, `rememberAhead`, and the `trace()` fields (add `sameTorrent: true`).
- **Emulator:** `lab/player/smartplay_emul.py`, a case where the same-pack candidate is queued without a probe and replaced when its measured rate fails the window.

**Verify:**
- One real-app run (HotD, muted). The trace shows S02E06 queued from the pack with no probe, and a 60 s check that passes, given the 44 MB/s seen before.
- Lint, build, and a local commit.

## 9e. Port warning (your choice: a toast only)
**Rivulet:**
- **Counter:** a manager-level count of accepted incoming connections since start, plus the time of the last one. Bumped in `dialLog.onPeerConnAdded` (`internal/engine/dials.go:108`), which already sees every incoming connection.
- **Exposed** in `GET /network-info` (`handleNetworkInfo`, api.go:845), next to the existing fields:
  - `incoming: { count, lastAt, listenPort, activeSince }`;
  - `activeSince` is when a torrent last became active, so the web knows how long peers have had to find us.
- **Docs and tests:** OpenAPI updated; a test that an incoming connection bumps the counter.

**Web:**
- **When:** while a torrent is active (playing or downloading), poll `/network-info` once a minute.
- **Rule:** if a torrent has been active for ≥ 5 min and `count` is still 0, show **one toast per session**: "No peer could connect to you (port 49881). Thin swarms will be slow: forward this port on your router."
  - The port shown is the one Rivulet reports.
  - The text goes through i18n (`src/smartPlay/i18n.en.json`).
- **Where:** a small hook, `src/smartPlay/usePortCheck.ts`, mounted where the other smart-play toasts live (`Player.js`, plus the downloads path so a background-only session is covered). It reuses the toast pattern of `onAheadPlanned`.
- **No setting:** it shows once, then stays quiet for the session.

**Verify:**
- **Rivulet:** unit test plus a live run; after a few minutes on a public swarm, `count > 0`.
- **Web, negative case:** temporarily start a test Rivulet on an unforwarded port (e.g. 49885, which the router doesn't forward) with an allowed test binary path. After 5 min the toast appears, once. Then restore normal settings.
- Lint, build, real app, and a local commit.

## Close-out
- `findings/31` (9c) and `findings/32` (9e).
- Update `plans/09-later.md`, TODO and START-HERE.
- The plan-08 next-day check stays open (it happens naturally on your next episode).

## Not covered
- Manual downloads (yours to design).
- The deadline scheduler and merged swarms (parked, reasons in plan 09).
