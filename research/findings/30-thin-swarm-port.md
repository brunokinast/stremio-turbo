# 30. Thin swarm: the reachable listening port decides it (step 9a)

2026-11-19, 03:43–04:56 (UTC-3). Peaky Blinders S06 RAZORS pack `9247c52e`, file 1 (S06E02). Data: `data/thin9a-20261119-034334` (contains peer addresses; not to be published). Scripts: session scratchpad `thin9a.py`, `thin9a_report.py`.

## Question
Last night Rivulet got 0.01–0.1 MB/s on this swarm, while qBittorrent got 1–5 MB/s. Which client is better on a thin swarm, and why?

## Run: each client alone, 10 min windows (A B A B), the same 20 trackers
| Window | Down | Avg | Up | Distinct peers | Incoming |
|---|---|---|---|---|---|
| A1 qBittorrent (port 31687) | 21.3 MB | 0.04 MB/s | 0.5 MB | 39 | 0 |
| B1 Rivulet (port 49881) | 197 MB | 0.33 MB/s | 554 MB | 20 | 16 |
| A2 qBittorrent (31687) | 11.3 MB | 0.02 MB/s | 0.6 MB | 36 | 1 |
| B2 Rivulet (49881) | 204 MB | 0.35 MB/s | 543 MB | 20 | 17 |
| C1 qBittorrent on **49885** | 19.0 MB | 0.03 MB/s | n/a | 31 | 1 |
| C2 qBittorrent on **49881** | **4627 MB** | **7.7 MB/s** | n/a | 41 | 6 |

- **Where qBittorrent's data came from:** in A1 and A2, nearly all of it came from one Deluge seed it had dialled.
- **Where Rivulet's data came from:** in B1 and B2, it came mostly from **incoming** connections by seeds (they hold all 2711 pieces). The largest single peer sent 132 MB in B2.
- **The seeds can't be dialled:** Rivulet's own attempts to reach the peers that delivered to qBittorrent were 9 timeouts and 4 connections (B1).
- **Firewall:** Windows Firewall allows both programs inbound (Private profile, the active one).
- **UPnP fails for both:** qBittorrent got "UPnP error 900"; Rivulet's AddPortMapping got "500".
- **Only 49881 is reachable from outside:** the router has a forward for it alone, which is why moving qBittorrent to 49885 changed nothing.

## Cause
**On this swarm the supply comes from seeds that can only connect in, so only a client whose listening port is reachable gets them.**
- Neither client's code makes the difference. qBittorrent on 49881 reached 7.7 MB/s, about 200× its own rate on 31687.
- **Last night's gap** was very likely the same thing. Rivulet's runs then were short probes (30–60 s), too short for seeds to find it and connect. qBittorrent had been running for hours and kept outgoing connections to the few dialable peers.

## Consequences for plan 09
- **9d (peer memory): skip.** The peers that matter connect in, and we can't dial them, so remembering their addresses doesn't help.
- **9b (giving back): upload doesn't buy speed here.** Rivulet uploaded 2.7× what it downloaded, uncapped (~0.9 MB/s), and the deliverers were seeds, which don't trade.
  - **What's left to decide:** whether to cap uploads while downloading. Upstream saturation can slow downloads (ACK delay); this run doesn't show whether it did.
  - **Needs your call** on the cap value, if any.
- **9c (season-pack rate): unaffected,** still worth building.
- **New, a product point:** Rivulet already listens on a fixed port (6c). With UPnP failing on this router, that port's reachability is the single largest factor on thin swarms, so worth surfacing:
  - **The signal:** count incoming connections in the first minutes. If none arrive, a notice says the port is unreachable.
  - **Status:** not built; a candidate step.

## Machine state changed
- **qBittorrent's listening port:** set to 49881 temporarily so Peaky S06 finishes (35% → done in about 40 min at 13.7 MB/s).
- **Automatic restore:** a background job sets it back to 31687 when the torrent completes.
- **While it's on 49881,** Rivulet can't bind that port, so don't start Stremio until it's restored.

## 9b measurement: does uploading fill the line? (07:24)
- **Setup:** the round trip to 1.1.1.1 every second for 60 s with the line idle, then for 4 min with Rivulet uncapped on the Peaky swarm (qBittorrent paused).
- **Idle:** p50 3 ms, max 7 ms.
- **While Rivulet ran:**
  - upload median 1.5 MB/s, peak **11.0 MB/s**; download peak 6.9 MB/s;
  - round trip at ≥ 0.9 MB/s up: p50 3 ms, max 18 ms, no losses.
- **Result:** the upstream has well over 11 MB/s (~90 Mbit/s) and doesn't queue, so uploading costs this machine's downloads nothing. **No upload cap is needed on this line;** 9b is closed without code. A per-user cap would only matter on a slow-upstream connection, and that's upstream Rivulet's concern.
- Data: `data/upcap-20261119-072440`.
