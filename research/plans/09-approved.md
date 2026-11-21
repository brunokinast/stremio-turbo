# Plan 09: thin swarms (what's left of `plans/09-later.md`)

## Context
Plan 08 handles a swarm that is slow but steady. What's left is a swarm that barely delivers at all. Last night's Peaky Blinders S06 showed the problem:
- **On the same small swarm, Rivulet got 0.01–0.1 MB/s on both 4K packs, while qBittorrent got 1–5 MB/s.** The runs were at different times, so this isn't proven yet, but the gap is 10–50×.
- **Sources:** none of the other 4K S06E02 sources beat the pack (findings: XEBEC 0.55, FLUX 0.08, GLHF 0 MB/s).

**Status of the plan 09 items:**
| # | Item | Status |
|---|---|---|
| 1 | Download ahead | Done (plan 08) |
| 2 | Source choice by measured supply | Mostly done: 6e (rate ÷ bitrate), 6h (rate memory), 8c (probe + 4-min check). One gap is left: season packs (9c) |
| 3 | Deadline scheduler | Not justified: no late urgent chunks after 5c (findings/15). Stays parked until a stall trace shows them |
| 4 | Peer memory | Open. Design waits on 9a (are the useful peers dialable?) |
| 5 | Merged swarms | Parked: no shared 4K files |
| 6 | Giving back | Your choice: **seed only while downloading** |

**Rules:** every step starts by measuring, and later steps are designed from 9a's answer. One step at a time, each ending with a report and your OK. Tests run 4K HDR, muted, stopped after; local commits only.

## 9a. Why does qBittorrent get 10–50× more on a thin swarm? (diagnostic only, no product code)
- **Setup:** reuse your live Peaky S06 download in qBittorrent. Pause it, then run each client alone, alternating 10 min each for two rounds (A B A B), on file E02 of 9247c52e. Rivulet runs as a download (`POST /downloads`) with the same trackers as qBittorrent.
- **What is recorded:**
  - **qBittorrent:** `/api/v2/sync/torrentPeers` every 10 s: address, client, flags (I = incoming, U/D interest and choke, E = encrypted, P = uTP), download and upload rates, and how much each peer holds.
  - **Rivulet:** `/debug/trace/{ih}`: dial log (source, outgoing, result), `peerSamples` (rate, choked, pieces), and upload bytes.
  - **Both:** total bytes per 10 min.
- **Questions to answer, each with a number:**
  1. Do both clients see the same peers? Compare addresses, and how each was found: tracker, DHT, PEX or incoming.
  2. Which peers deliver to qBittorrent? Are they incoming-only (not dialable by us)? Do they unchoke Rivulet at all?
  3. Upload: how much does qBittorrent send to the peers that unchoke it, and how much does Rivulet send? This tests tit-for-tat.
  4. Protocol differences: encryption required by peers, uTP vs TCP, our listen port reachable (incoming count), connection cap.
- **Output:** `findings/30`, with the cause ranked by evidence. That ranking picks which of 9b–9d gets built, and in what order.
- **Cleanup:** restore qBittorrent exactly (resume the torrent, file priorities unchanged), and remove the Rivulet test download and its data.

## 9b. Giving back while downloading (if 9a shows unchokes follow uploads)
- **Today:** `seedingEnabled` is on, so a torrent that's downloading already uploads the pieces it has. 9a measures whether that happens at a useful level.
- **Change only what 9a shows is wrong.** Likely candidates:
  - the upload limit;
  - upload slots / optimistic unchoke in the anacrolix config;
  - in a season pack, completed episodes staying offered while another episode of the pack is downloading. This already works if the torrent stays loaded; verify it.
- **Unchanged:** `unloadIfDone` still unloads a torrent once its downloads are all complete (your choice: no seeding after).
- **Verify:** compare upload bytes and time to first unchoke per peer, before and after, on the same swarm.

## 9c. Season packs: use the playing torrent's own rate (gap left in item 2)
- **The gap:** when the next episode is in the same pack, `ahead.ts` still probes that file for 30 s. The torrent's live rate is already known (findings/27 "Open"). The 0.7 MB/s probe vs 44.6 MB/s actual was this case.
- **Change:** if the candidate shares the playing infoHash, take the torrent's rate over the last 60 s from stats as its rate, and skip the probe. The 4-min check stays.
- **Files:** `src/smartPlay/ahead.ts`; the rate source is the existing stats polling (`useSmartPlay` / `RateMeter`).
- **Test:** an emulator case in `lab/player/smartplay_emul.py`, then one real-app run on the HotD pack (S02E06), checking the trace shows no probe and the right choice.

## 9d. Peer memory (only if 9a shows the delivering peers are dialable and stable)
- **Remember:** per infoHash, peers that delivered ≥ 1 MiB, with address, network, rate and last seen, kept 7 days. Stored in `<APP_PATH>/peers.json`, from the trace's `peerSamples` and dial log.
- **Use:** on add, give the remembered addresses to the torrent first (`AddPeers`), before trackers and DHT answer. Pattern: `dhtnodes.go` (`keepDHTNodes`: load on start, save periodically).
- **Skip this step** if 9a shows the delivering peers are incoming-only: remembering them can't help us dial them.
- **Verify:** time to the first useful byte on a re-add, cold process, with vs without the memory (Rivulet trace).

## Verification (end of plan 09)
- 9a explains the gap with per-peer numbers.
- After the built steps, Rivulet alone on the Peaky swarm reaches the same order of rate as qBittorrent at a comparable time, measured A/B as in 9a.
- Findings are `findings/30` onward; update `plans/09-later.md`, TODO and START-HERE.

## Not covered
- Manual downloads (yours to design after reviewing plans 1–9).
- Merged swarms and the deadline scheduler (parked, with reasons above).
