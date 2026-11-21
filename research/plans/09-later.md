> **2026-11-19:** approved as `09-approved.md`. 9a done (`findings/30`): on thin swarms the reachable listening port decides it; 9d (peer memory) skipped; 9b reduced to an upload-cap question.

# Plan 09 — Later work (not ready to build; each needs its own plan after steps 1–7)

Ordered by expected effect for this user. Each entry says what must be known first.

## 1. Time-shifted supply: download before watching
**Now has its own plan: `08-download-ahead.md` (waiting on the user's choices).** The notes below are the reasoning.
The reference swarm delivers roughly the stream's bitrate at best (0.5–2 MB/s against ~2 MB/s). No scheduler removes buffering when supply is below demand; only time does. The user's stated end goal ("an option to download ahead so no buffering is possible") is this.

- **Next episode in the background.** When an episode ends or playback stops, keep downloading the next one or two episodes of that series at Normal priority. At 0.5 MB/s a 6.7 GB episode takes under 4 hours, so "watched one last night" means "the next is on disk today".
- **Needs:** Rivulet running when the Stremio window is closed (today `start-stremio.ps1` stops it with the shell), a disk budget with eviction (watched episodes first), a visible list of what is on disk, and a setting. For season packs the torrent is the same; for single-episode torrents it needs the next episode's source (the binge group the web client already tracks).
- **First step when planned:** measure with plan 02 how many bytes a background download gets per hour on the reference swarm when nothing is playing.

## 2. Choose the source by measured supply, not by seed count
Torrentio lists six 4K files for the reference episode with 9, 8, 7, 1, 0, 0 seeds, and the one we used had two useless seeds; its real supply was leechers. Seed counts say little.

- After plans 05 and 06, a preloaded source has a measured fact within seconds: how many peers hold the head of our file, and their rate.
- Rule to design: prefer the source whose measured rate over the first N seconds, divided by the file's bitrate (size ÷ runtime), is highest; show that ratio in the smart-play panel ("can keep up" / "will buffer").
- Remember per title which source worked (`qualityMemory.ts` exists) and how its peers behaved.

## 3. Deadline scheduler (replaces the priority tiers for the playing file)
`design/00-synthesis-and-roadmap.md` describes it. Plan 04 (rate, queue) and plan 05 Part C (delivery-time stealing, slow-peer exclusion) are its first slices. Build the rest only if plan 05's acceptance run still shows urgent chunks arriving late.

## 4. Peer memory
Remember, per torrent, the addresses that delivered data, with transport and rate, and dial them first next time. On the reference swarm two or three peers are the whole supply, and they stay the same people for days. Needs plan 06 B1's report to confirm they are dialable at all (both were incoming).

## 5. Merged swarms
The same file in several torrents (single episode and season pack) shares bytes; each torrent verifies with its own hashes over a common file on disk. For the reference 4K episode no file is shared between torrents, so this helps 1080p only (26 shared files in Torrentio's list). Low priority for a 4K user; do not start without a title where it would matter.

## 6. Giving back
All useful peers were leechers. They unchoke by what they receive. We hold only what they already have, so there is little to trade today; with item 1 (whole episodes on disk, kept for a while) we would become a useful peer for the rest of the pack's leechers, which is what earns unchokes on small swarms. Measure upload and unchoke time per peer before designing anything.

## 7. Audio normalizer (asked 2026-11-21)
Even out loud and quiet passages and level differences between episodes during playback. To discuss with the user before any plan: what bothers them (dialogue too quiet vs. effects, or level jumps between sources), and where it would run (the shell's mpv filters, e.g. `dynaudnorm`/`loudnorm`, set through `mpv.conf` or a player option).

## Ideas parked with reasons
- **Smaller probe / persisted probe cache:** the probe does not gate mpv in the shell (`findings/08` §1).
- **More connections as such:** qBittorrent and Rivulet had the same five live peers (`findings/09` §1). Connect fast and widely for discovery (plan 06 B5), not for raw count.

> **Started 2026-11-18 (6h, `findings/23`):** a narrow per-source memory, each source's measured rate ÷ bitrate kept for 24 h in the web client, lets the race skip waiting for sources known to be too slow. Peer memory is still open.
