# 45. Steady-state speed: qBittorrent's lead was one seedbox

2026-11-23. The README bench (bench-5, three rounds on a healthy 4K HDR swarm) had qBittorrent at 49 MB/s over 30–60 s and Turbo at 24. This finding asks why and tests the fixes that looked likely. None of them shipped. With more rounds, the gap turned out to be the luck of one seedbox.

## The single-connection ceiling, in a lab
`research/lab/bench/ceiling.py` takes the swarm out:
- A libtorrent seeder serves a 4 GiB file (16 MiB pieces) on 127.0.0.1.
- The seeder dials Rivulet's BT port. Rivulet ignores loopback trackers, an SSRF guard (`filterRequestTrackers`).
- Optionally, a relay holds each direction's bytes to add a round trip.

One connection, steady MB/s:

| Round trip | Turbo | libtorrent 2.1 |
|---|---:|---:|
| 0 ms | 578 | 128 |
| 210 ms | 37 | 127 |

At 0 ms the receive path is far from a limit. At 210 ms Turbo's queue tops out at **963 requests**: anacrolix's `maxLocalToRemoteRequests`, which is (32 KiB write buffer − 16 KiB low water − 5) / 17. With refills at half the queue, about 490 are in flight: 490 × 16 KiB / 0.21 s = 38 MB/s. libtorrent instead takes the peer's advertised `reqq` (`bt_peer_connection.cpp:2091`), 2000 for a libtorrent seeder, with a 3 s queue time. Raising the write buffer to upstream's intended 64 KiB (1927 requests; WebRTC kept at 32 KiB) took Turbo's lab connection to 88 MB/s.

## In the swarm, the receiver wasn't the limit
The trace now records each TCP connection's kernel state (`peerSamples[].tcp`, from Windows `SIO_TCP_INFO`: receive window and buffer, RTT, reordering, retransmits). On the fastest connections at 30–60 s:
- The receive window was fully open: 8–16 MB, equal to the buffer, so data was read as fast as it arrived. There was no reordering and no retransmission.
- Each held 255–700 requests, well over the 125–190 that its 10–15 MB/s needs in flight at about 200 ms.
- Many fast seedboxes advertise `reqq` 500 (`maxRequests` 500 in the trace), below either cap.

So the sender set the pace: the seedbox's per-peer upload. That shifts with its load from one minute to the next, which matters because the bench's runs follow each other, not run at once.

## What was tried
Healthy swarm (`a3bdec6c…`, file 0), same rounds, cold, rotating order. MB/s medians:

| Variant | Rounds | 0–30 s | 30–60 s | First frame | Waits behind older requests |
|---|---:|---:|---:|---:|---:|
| Current (4 RTT queue, about 0.85 s) | 4 | 21.6 | 32.1 | 4.0 s | 8.1 s |
| 8 RTT queue (about 1.6 s) | 4 | 16.9 | 31.9 | 6.8 s | 10.1 s |
| 3 s queue (libtorrent's `request_queue_time`) | 4 | 21.0 | 37.3 | 5.8 s | 17.8 s |
| Current build | 10 | 23.2 | 36.3 | 3.6 s | — |
| 1927-request cap (64 KiB write buffer) | 10 | 20.9 | 30.7 | 4.2 s | — |

- **Deeper queues:** urgent chunks wait behind more requests (finding 15's trade), so the first frame is 1.75–2.8 s later. The steady-state gain is within noise.
- **Higher cap:** it helps only a fast `reqq`-2000 peer far away, and none showed up in 10 rounds. First frame was over 4 s in 7 of 10 runs (2 of 10 for the current build), because slow start keeps growing past 963 before it exits.

Neither change passes the plan's guard (first frame no more than 0.3 s worse). Both stay as bench knobs only: `RIVULET_REQUEST_QUEUE_RTTS` (100 reaches the 3 s ceiling), and compare.py's `@EXE=` for comparing builds.

## Same rounds against qBittorrent
Pooled from bench-6 and bench-7 (8 rounds; the current build in each):

| | 0–30 s | 30–60 s | First frame |
|---|---:|---:|---:|
| Turbo | 21.1 | **38.9** | 3.4 s |
| qBittorrent | 12.5 | 36.0 | 20 s (first and last piece on disk) |
| libtorrent 2.1 (qBittorrent defaults, 4 rounds) | 19.5 | 33.0 | 19.5 s |

These rounds ran with finding 44's change: starts from the beginning stayed at 3.4–3.6 s (3.3 s in bench-5).

qBittorrent's 30–60 s rounds split in two: 51–58 MB/s in the three rounds where one qBittorrent 5.2.2 seedbox gave it 45–51 MB/s on a single plaintext TCP connection found through the DHT, and 14–40 MB/s otherwise. Turbo got the same seedbox in some rounds (23–32 MB/s from it) and missed it in others. The same split explains bench-5: qBittorrent's three rounds there were 14.8, 49.2 and 51.5.

## Next
- A bench needs at least 6–8 rounds per row before its steady-state numbers mean anything. Three rounds measure the luck of a seedbox.
- Landing the fast seedbox more often is a supply question (findings 42–43): which peers Turbo dials and keeps, not how fast it reads them.
