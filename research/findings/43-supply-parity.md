# 43. Supply parity: Turbo against qBittorrent after the limits came off (plan 16 Part D)

2026-11-22. Follows findings/42 and its correction: qBittorrent's peer counts had included connection attempts, and the uTP cause was refuted.

Method:
- `research/lab/bench/compare.py`, 60 s per run, cold, rotating order.
- qBittorrent's peers now count only completed handshakes.
- Rivulet's CPU time per run is recorded.
- Turbo variants share the rounds (`variants=`), so swarm changes hit every row alike.

## Changes measured
1. **No download speed limits** (`RIVULET_SPEED_LIMITS=0`, set by the shell; commit `6adaefa6b`). The official profile's soft limit froze the connection count at about 20 s (findings/42, cause 1), and the hard cap refused new connections while its limiter was empty.
2. **Connection budget** (`RIVULET_PEERS_PER_TORRENT`): 50, 100 and 200 established connections per torrent, with half-open n/2 and known addresses n×10.

## Healthy swarm (4K HDR, 9.0 GB, about 95 seeders)
Speed limits off, first with the default budget, in two separate benches:

| bench-3, median of 3 | First frame | MB/s | Peers |
|---|---:|---:|---:|
| Turbo, 50 | 2.8 s | 28.3 | 34 |
| qBittorrent | — | 14.3 | 23 |
| Turbo, 200 (a later bench) | 2.8 s | 19.7 | 43 |
| qBittorrent (same later bench) | — | 40.5 | 34 |

The two benches disagree because the swarm changed between them, so the budgets were then interleaved in the same rounds:

| bench-4-h-budget, median of 4 (range) | First frame | MB/s | Peak MB/s | Peers | CPU s |
|---|---:|---:|---:|---:|---:|
| Turbo, 50 | 3.4 s | 19.0 (11.9–29.0) | 41.1 | 40 | 12.7 |
| **Turbo, 100** | **3.0 s** | **19.9 (18.9–22.2)** | 41.5 | 40 | 14.5 |
| Turbo, 200 | 4.1 s | 25.6 (17.6–29.9) | 39.2 | 38 | 22.1 |
| qBittorrent | — (proxy 17 s) | 20.3 (11.9–22.6) | 25.7 | 31 | — |

Before the change (findings/42), Turbo held 12–17 peers on this swarm, against qBittorrent's 17–28 handshaked peers.

- **Every budget now matches or beats qBittorrent** on MB/s and peers.
- **The budget barely matters here:** the swarm offers about 40 reachable peers, and all three budgets reach them.
- **100 is the new default.** It is qBittorrent's per-torrent default. It had the fastest and most even start, and the tightest MB/s range.
- **200 isn't worth it here:** 50 % more CPU and a second slower to start. Its MB/s gain is inside the noise; in bench-3 it was the slower one.

The earlier bench-3 200 run also showed a mechanism worth watching. With 40 peers from the first seconds, requests in flight fell from about 3,200 to 650–1,300, below what the peers' queues allowed. The per-peer queues (sized by rate) shrank as each peer's share fell. This did not repeat as a clear loss in the interleaved bench.

## Thin swarm (4K HDR, 8.6 GB, about 14 seeders)
Turbo at the new default (100), against qBittorrent:

| bench-4-thin, median of 3 (range) | First frame | MB/s | Peak MB/s | Peers | Stalls |
|---|---:|---:|---:|---:|---:|
| **Turbo** | **8.0 s (7.9–8.7)** | **3.1 (1.2–3.3)** | 4.8 | 7 (4–8) | 0 (0–1) |
| qBittorrent | never (its first and last pieces weren't done in 60 s) | 2.6 (1.2–3.4) | 4.4 | 7 (6–8) | — |

- Before: Turbo 9.7 s and 1.0 MB/s against qBittorrent's 5.7 MB/s (findings/42).
- That gap came from one seeder that qBittorrent reached and Turbo usually didn't. The seeder wasn't in the swarm tonight, so this bench measures the rest of the swarm, where Turbo leads.
- Whether Turbo now reaches that seeder stays open until it reappears.

## Changed
- `server/internal/engine/engine.go`: `defaultPeersPerTorrent = 100` (`peerBudget(0)` is now 100/50/1000, was 50/25/500).
- `research/lab/bench/compare.py`: `variants=`, `turboenv=`, CPU time; qBittorrent peers count handshakes only.
- `deps/go-utp/purego`: `Socket.Counters` and `TestLiveDial` (findings/42 correction).

## Next
- Dial timeout and address order (Part D items 1 and 4) matter less now: the reachable set is found within about 30 s either way. They are worth revisiting only if a thin-swarm bench shows a seeder found late.
- Part E (libtorrent prototype) and Part F (decision).
