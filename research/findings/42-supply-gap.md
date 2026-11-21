# 42. Why Turbo gets fewer peers than qBittorrent (plan 16 Part C)

2026-11-21. Rivulet and anacrolix with the supply diagnostics (`54da87be2`), installed Turbo 1.0.0 build.

Benches:
- `research/lab/bench/compare.py`, 3 rounds, 60 s per run, 10 s settle, rotating order, cold.
- Data: `data/bench-2-healthy` (a 4K HDR single episode, 9.0 GB, about 95 seeders) and `data/bench-2-thin` (a 4K HDR single episode, 8.6 GB, about 14 seeders). The smoke run with the first live trace is `data/bench-smoke4`.
- Analysis: `research/lab/bench/funnel.py`.

The libtorrent reference row is missing: Python 3.13 has no firewall rule yet.

## Results

| Median of 3 (range) | Official Stremio | Turbo | qBittorrent |
|---|---:|---:|---:|
| **Healthy:** first frame | 4.3 s (3.6–11.8) | 4.2 s (3.1–4.4) | — * |
| **Healthy:** MB/s, first 60 s | 3.3 | 10.2 (9.9–17.5) | 10.5 (7.0–14.9) |
| **Healthy:** freezes / frozen s | 2 / 5.2 s | 1 / 2.0 s | — |
| **Healthy:** peers held (max) | 15 | 14 | 100 |
| **Thin:** first frame | 37 s; 2 of 3 never | 9.7 s (4.3–14.6) | — * |
| **Thin:** MB/s, first 60 s | 0.07 | 1.0 (0.08–9.4) | 5.7 (0.5–5.8) |
| **Thin:** peers held (max) | 4–5 | 6–9 | 35–37 |

\* qBittorrent's stand-in (first and last pieces on disk) took 21–51 s or never came. It downloads in order without favouring the player, so it isn't a first frame.

Swarm noise is large. qBittorrent's healthy-swarm speed was 23–44 MB/s in bench-1 and 7–15 MB/s here.

## Causes, ranked by measured effect

### 1. Rivulet's soft limit freezes the connection count at about 20 s
`engine.enforceSoftLimit` runs every 5 s and copies the official server's behaviour. When the download speed is above the profile's `btDownloadSpeedSoftLimit` (the owner's profile: 4 MiB/s) and at least `btMinPeersForStable` (10) peers are connected, it calls `SetMaxEstablishedConns(connectedPeers)`.

In the trace (`bench-smoke4`):
- At 19 s: `maxEstablished` 50, 11 established.
- At 21 s: `maxEstablished` 11; half-open falls from 25 to 0, and 98 addresses are left undialled.

Every healthy run held 12–17 peers, as the official server does (12–15).

### 2. The ramp before that is slow: 25 half-open slots, 3 s dial timeout
- **Addresses aren't scarce.** 347 were offered in the first 5 s (DHT and trackers); the first DHT batch came at 0.1–0.3 s.
- **Attempts are few.** 36 started by 5 s and 50 by 10 s, with half-open pinned at 25.
- **The timeout is short.** anacrolix's `reducedDialTimeout` gives 20 s ÷ ((pending + 25) / 25), which is 3 s while 200 addresses wait.
- **Connects that worked took** 1.5–1.9 s at the median, 2.7–3.0 s at the 90th percentile, up to 7.8 s. The 90th percentile sits on the cut-off.
- **Result:** 8–10 connections at 10 s, against qBittorrent's 76–88.

### 3. Most of Turbo's dials to peers qBittorrent reaches over uTP get no answer at all
qBittorrent's data comes almost entirely over uTP from DHT-found peers:
- healthy: 410–846 MB uTP against up to 125 MB TCP;
- thin: 290–333 MB uTP.

Overlap on the same swarm:
- Turbo dialled 126 (healthy) and 34 (thin) of the addresses qBittorrent connected to.
- qBittorrent connected to all of them except one over uTP (125 of 126 on the healthy swarm).

Turbo's attempts to those peers (each tries TCP and uTP at once):

| | Thin (timeout 20 s) | Healthy (timeout 3–10 s) |
|---|---:|---:|
| Both TCP and uTP timed out | 55 | 194 |
| TCP connected | 29 | 28 |
| uTP connected | 12 | 32 |
| TCP refused, uTP timed out | 3 | 21 |

On the thin swarm the uTP dials waited the full 20 s (p10–p90 of 19,999–20,000 ms) without a reply. qBittorrent, on the same machine and the same port (49881), connects to these peers over uTP. So the cause is in our uTP connect, not the swarm or the network. Candidates, all unmeasured:
- the SYN isn't sent, or is sent from a socket other than the listening one;
- the replies aren't matched to the connection;
- the remote resets the connection and we drop the reset.

This is the largest unexplained gap: it hides most of the swarm's best peers.

### 4. Minor
- The profile's hard limit (37.5 MiB/s) makes anacrolix refuse new connections while the limiter is empty (`newConnsAllowed`). Turbo peaked at 43–47 MB/s.
- About 2–4 dials per torrent hit our own public address (refused, or a self-connection, which `PeerConnClosedReason` now names).
- Incoming connections delivered 99–181 MB per healthy run for Turbo. They were a large share for qBittorrent in bench-1 but not here.

## Next (Part D, in this order)
1. **uTP connect:** go-utp counters per dial (SYNs sent, packets received from the address, resets), plus a lab reproduction against the test qBittorrent. Then fix.
2. **Soft limit:** off for Turbo, or measured against what the stream needs rather than a fixed 4 MiB/s. It needs the owner's decision, since it's a Stremio setting.
3. **Dial pipeline:** a libtorrent-like timeout (`peer_connect_timeout` 15 s) and more half-open slots, measured together with 1, because fixing uTP changes how many dials time out.
4. **Re-bench** on both torrents, with the libtorrent row once its firewall rule exists.
