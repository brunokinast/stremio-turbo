# Benchmark

Every run behind the README's "How fast" numbers, and how fair the test is. The tables are written by `node scripts/readme-images.mjs bench`.

## How it ran

One PC and one home connection; one client at a time, each started fresh, given 10 s to warm up (DHT, trackers), then 60 s from the click; the order rotates each round so no client always goes first. Every client gets the same torrent and the same tracker list, and its download is deleted after each run.

- **Stremio** 5.0.28 (server 4.22.6) runs with this PC's own settings, which are its fastest torrent profile: 200 connections, and its built-in throttle that stops looking for peers above 4 MiB/s.
- **Turbo** 1.0.2's engine runs exactly as the app starts it.
- **qBittorrent** 5.1.2 runs with stock settings, plus sequential download and first and last pieces first, as streaming needs.

## What to keep in mind

- qBittorrent's "first frame" is a stand-in (the file's first and last pieces on disk), not a played frame.
- qBittorrent's speed swings with whether it reaches one fast seedbox. Over 12 other rounds the same day its median from 30 s on was 25 MB/s, against Turbo's 34 ([finding 45](research/findings/45-steady-state-speed.md)). Turbo's lead is real but smaller than this run's.
- The official app always ran first in round 1, and both times that was its slowest run. Without it, its medians move by only 0.1 MB/s.
- Two torrents on one connection. Turbo's latest speed fixes were measured on the healthy one; the thin one wasn't used to tune anything.

## Every run

Anonymised: no torrent, addresses or names. The same runs with each second's download: [docs/readme/bench-runs.json](docs/readme/bench-runs.json). The script: [research/lab/bench/compare.py](research/lab/bench/compare.py).

<!-- bench-runs:start -->

**Healthy swarm** (179–292 peers known)

| Round · order | Client | First frame | Frozen | MB/s | 0–30 s | 30–60 s | Peak | Peers |
|---|---|--:|--:|--:|--:|--:|--:|--:|
| 1 · 1 | Stremio | 14.2 s | 5 × 28.4 s | 1.1 | 1.2 | 1.0 | 1.9 | 12 |
| 1 · 2 | Turbo | 4.3 s | 1 × 4.2 s | 21.0 | 16.3 | 25.5 | 35.1 | 39 |
| 1 · 3 | qBittorrent | not ready | – | 16.3 | 12.0 | 20.0 | 26.1 | 37 |
| 2 · 1 | Turbo | 4.7 s | 2 × 4.5 s | 27.8 | 21.1 | 34.4 | 41.5 | 39 |
| 2 · 2 | qBittorrent | 21.0 s* | – | 16.1 | 13.8 | 17.8 | 21.8 | 34 |
| 2 · 3 | Stremio | 4.3 s | 3 × 4.1 s | 4.2 | 3.5 | 4.8 | 6.0 | 11 |
| 3 · 1 | qBittorrent | 15.0 s* | – | 16.3 | 13.4 | 18.5 | 26.4 | 27 |
| 3 · 2 | Stremio | 4.6 s | 3 × 4.2 s | 3.4 | 2.8 | 4.1 | 5.2 | 12 |
| 3 · 3 | Turbo | 4.0 s | 1 × 2.6 s | 24.4 | 20.3 | 28.4 | 45.1 | 35 |
| 4 · 1 | Stremio | 4.6 s | 4 × 11.8 s | 3.0 | 2.9 | 3.1 | 4.0 | 15 |
| 4 · 2 | Turbo | 4.0 s | 1 × 1.6 s | 31.9 | 24.7 | 39.0 | 41.7 | 36 |
| 4 · 3 | qBittorrent | not ready | – | 15.2 | 12.6 | 17.4 | 40.2 | 29 |
| 5 · 1 | Turbo | 4.5 s | 1 × 2.0 s | 26.5 | 18.1 | 34.9 | 39.6 | 36 |
| 5 · 2 | qBittorrent | not ready | – | 15.5 | 14.3 | 16.2 | 18.4 | 29 |
| 5 · 3 | Stremio | 4.7 s | 1 × 2.1 s | 4.0 | 3.6 | 4.3 | 4.7 | 14 |
| 6 · 1 | qBittorrent | not ready | – | 12.3 | 13.2 | 11.0 | 17.2 | 25 |
| 6 · 2 | Stremio | 4.8 s | 2 × 3.6 s | 3.2 | 3.2 | 3.3 | 4.6 | 12 |
| 6 · 3 | Turbo | 3.8 s | 1 × 1.1 s | 30.2 | 22.8 | 37.6 | 53.0 | 33 |

**Thin swarm** (37–43 peers known)

| Round · order | Client | First frame | Frozen | MB/s | 0–30 s | 30–60 s | Peak | Peers |
|---|---|--:|--:|--:|--:|--:|--:|--:|
| 1 · 1 | Stremio | 5.0 s | 0 | 1.7 | 2.2 | 1.2 | 2.7 | 5 |
| 1 · 2 | Turbo | 4.0 s | 0 | 13.4 | 13.5 | 13.3 | 19.7 | 8 |
| 1 · 3 | qBittorrent | 30.0 s* | – | 8.7 | 7.9 | 9.2 | 11.8 | 11 |
| 2 · 1 | Turbo | 4.6 s | 0 | 9.9 | 9.0 | 10.7 | 14.9 | 10 |
| 2 · 2 | qBittorrent | 38.0 s* | – | 6.9 | 6.3 | 7.3 | 8.9 | 11 |
| 2 · 3 | Stremio | 5.8 s | 0 | 4.2 | 4.0 | 4.5 | 4.9 | 6 |
| 3 · 1 | qBittorrent | 38.0 s* | – | 6.4 | 7.1 | 5.6 | 9.6 | 10 |
| 3 · 2 | Stremio | 5.8 s | 0 | 3.4 | 2.6 | 4.2 | 4.4 | 6 |
| 3 · 3 | Turbo | 4.9 s | 0 | 11.3 | 9.6 | 13.0 | 15.9 | 10 |
| 4 · 1 | Stremio | 8.4 s | 0 | 3.0 | 2.6 | 3.3 | 3.9 | 6 |
| 4 · 2 | Turbo | 3.9 s | 0 | 16.4 | 15.2 | 17.7 | 22.5 | 10 |
| 4 · 3 | qBittorrent | 21.0 s* | – | 8.6 | 9.5 | 7.4 | 12.2 | 11 |
| 5 · 1 | Turbo | 4.0 s | 0 | 12.2 | 10.6 | 13.8 | 18.8 | 12 |
| 5 · 2 | qBittorrent | not ready | – | 5.8 | 5.4 | 6.0 | 9.0 | 10 |
| 5 · 3 | Stremio | 5.5 s | 0 | 2.5 | 2.6 | 2.3 | 3.1 | 5 |
| 6 · 1 | qBittorrent | 15.0 s* | – | 8.1 | 7.0 | 8.9 | 12.9 | 11 |
| 6 · 2 | Stremio | 5.7 s | 0 | 3.1 | 2.7 | 3.4 | 4.4 | 6 |
| 6 · 3 | Turbo | 3.9 s | 0 | 15.2 | 13.7 | 16.8 | 20.9 | 11 |

<sub>Order: when the client ran within its round. Frozen: the player's pauses for data after the first frame, count × total. MB/s: the average over the 60 s; Peak: the best second. Peers: most connected at once. *qBittorrent has no player: its first frame is when the first and last pieces are on disk (the file's headers and index), "not ready" if not within 60 s.</sub>

<!-- bench-runs:end -->
