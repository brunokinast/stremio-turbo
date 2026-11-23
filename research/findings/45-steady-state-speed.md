# 45. Steady-state speed: dropped seedboxes and a narrow uTP window

2026-11-23. In the README bench (bench-5, three rounds on a healthy 4K HDR swarm), qBittorrent downloaded 49 MB/s over 30–60 s and Turbo 24. More rounds kept the gap: over 14 same-round runs (benches 6–9) the medians were qBittorrent 42.7 MB/s and Turbo 27.3. qBittorrent's numbers are bimodal: 45–59 MB/s when one fast seedbox serves it, 14–40 otherwise.

## Ruled out first

### The receive path, in a lab
`research/lab/bench/ceiling.py` takes the swarm out:
- A libtorrent seeder serves a 4 GiB file (16 MiB pieces) on 127.0.0.1.
- The seeder dials Rivulet's BT port. Rivulet ignores loopback trackers, an SSRF guard.
- An optional relay holds each direction's bytes to add a round trip.

One TCP connection, steady MB/s:

| Round trip | Turbo | libtorrent 2.1 |
|---|---:|---:|
| 0 ms | 578 | 128 |
| 210 ms | 37 | 127 |

At 210 ms Turbo tops out at anacrolix's 963-request cap, `maxLocalToRemoteRequests` (32 KiB write buffer). libtorrent takes the peer's advertised `reqq`, 2000 for a libtorrent seeder.

### In the swarm, the TCP receive window was open
The trace now records each TCP connection's kernel state (`peerSamples[].tcp`, from Windows `SIO_TCP_INFO`). On the fast connections:
- The receive window was fully open (8–16 MB, equal to the buffer).
- There was no reordering or retransmission.
- Each had more requests outstanding than its rate needs in flight.

Many fast seedboxes advertise `reqq` 500, below the cap.

### What didn't help
Same rounds, 30–60 s MB/s median (first frame):

| Change | Rounds | Change | Before |
|---|---:|---:|---:|
| 3 s per-peer queue (libtorrent's `request_queue_time`) | 4 | 37.3 (5.8 s) | 32.1 (4.0 s) |
| 8 round-trip queue | 4 | 31.9 (6.8 s) | 32.1 (4.0 s) |
| 1927-request cap (64 KiB write buffer) | 10 | 30.7 (4.2 s) | 36.3 (3.6 s) |
| 200 half-open dials per torrent (from 50) | 6 | 31.8 (4.3 s) | 38.0 (4.2 s) |

- **Deeper queues:** urgent chunks wait behind more requests (finding 15's trade).
- **Higher cap:** slow start keeps growing past 963, and no fast peer in reach advertised a `reqq` above 963 anyway.
- **More half-open dials:** worse, with more dial churn and no earlier reach to the peers that matter.

They remain as bench knobs: `RIVULET_REQUEST_QUEUE_RTTS` (100 reaches the 3 s ceiling), and compare.py's `@EXE=` to compare builds from the firewall-allowed path.

## What was wrong

### 1. libtorrent's holepunch messages ended our connections
`supply.closes` showed why productive connections ended: 13 of 26 that had delivered at least 20 MB closed with `unmarshalling ut_holepunch message: buffer too small to be valid` (or `not enough bytes`). Two of them were seedboxes at 339 and 395 MB.

- libtorrent writes the error code only in `failed` messages (`bt_peer_connection.cpp`, `write_holepunch_msg`). Its rendezvous and connect messages are 8 bytes for IPv4 and 20 for IPv6.
- anacrolix's decoder required 12 and 24. So every time a libtorrent peer relayed a holepunch connect to us, we dropped it.
- Well-connected seedboxes relay the most.

**Fix** (`peer_protocol/ut-holepunch`, `peerconn.go`):
- The error code is optional when decoding.
- A holepunch message that still can't be read is ignored instead of ending the connection.
- We still always send the code, since unfixed anacrolix peers need it and libtorrent ignores trailing bytes. Upstream has the same bug.

### 2. uTP connections ran at window / round trip
The fastest uTP peers ran at exactly our advertised window divided by the RTT: 6.1 MB/s against 6.0 allowed at 175 ms, and 4.9 against 5.0 at 208 ms. go-utp's default receive buffer is 1 MiB, and anacrolix never set it.

**Fix:** a new `ClientConfig.UtpReceiveBuffer`, set to 4 MiB by Rivulet. Most swarm peers send at libtorrent's MTU floor (about 530-byte payloads in two thirds of samples). For them the window stays bounded by the reorder buffer, 2016 packets, the most a selective ack can describe: about 1 MB. Peers sending full-size packets get about 2.9 MB.

### 3. The fast seedbox is reached late (not fixed)
In the runs where qBittorrent got 41–52 MB/s from one seedbox, it had connected by 5–6 s, through the DHT.

- Turbo knew of that peer within the first 1.5 s: DHT and trackers delivered 250 or more addresses by then.
- It dialled it only at 20–27 s. Half-open dials were pinned at 50 while about 250 addresses waited, and dead addresses hold a slot for 4–10 s across their four legs.
- By then the seedbox was full: both TCP legs connected and it closed the handshake (EOF).
- When Turbo dialled it in the first second (two runs), it connected.

More half-open slots didn't help (above). The order of dialling decides it.

## Results
Same rounds, healthy swarm, 30–60 s MB/s:

| Bench | Turbo before | Turbo with 1–2 | qBittorrent |
|---|---:|---:|---:|
| bench-10, 6 rounds | 26.6 (22–48) | 31.2 (26–36) | 33.7 (19–59) |
| bench-11, 6 rounds | — | 38.0 (33–50) | 25.2 (15–51) |
| bench-13, 6 rounds | 28.6 (11–33) | 29.9 (20–46) | — |
| Pooled with qBittorrent, 12 rounds | — | **34.4** | 25.2 |

### Start-up held
- **First frame:** 4.2–4.7 s, against 4.5 s before.
- **The freeze right after it** (mpv's first pause) swings with the hour's swarm: 1.3–4.4 s medians for the same build in different benches.
- **Same rounds:** over 12 paired rounds (bench-10 and bench-13), the medians were 2.35 s with the fixes and 2.13 s without, which is within the noise. 30–60 s was 31.3 against 27.9 MB/s.

### The README bench (bench-12, this build, 6 rounds each)

| | First frame | MB/s, first minute | MB/s, 30–60 s | Frozen in the first minute |
|---|---:|---:|---:|---:|
| **Healthy swarm** | | | | |
| Turbo | 4.2 s | **27.2** | **34.2** (25–39) | 2.3 s |
| qBittorrent | 18 s (ready in 2 of 6) | 15.8 | 19.2 (12–21) | — |
| Official Stremio | 4.7 s | 3.3 | 3.6 | 4.2 s |
| **Thin swarm** | | | | |
| Turbo | 4.0 s | **12.8** | **13.3** (11–18) | 0 s |
| qBittorrent | 30 s | 7.5 | 8.0 | — |
| Official Stremio | 5.8 s | 3.0 | 3.3 | 0 s |

On the healthy swarm Turbo was ahead at 30–60 s in every round. qBittorrent didn't get the fast seedbox in any of them.

## Next
- **Dial order.** Reach the peers that deliver before their slots fill. libtorrent ranks connect candidates by how many sources reported them (tracker, DHT, PEX) before BEP 40. PEX's seed flag marks the seeds.
- **Benches need 6 or more rounds per row.** With fewer, a steady-state number measures which client got one seedbox.
- **Peer resets.** 10 of the 26 productive connections were reset by the peer (`WSAECONNRESET`). The trace can't say why yet.
