# 13 — Request queue (step 4)

Date: 2026-11-17.

Commits (local `wip/streaming-research`, nothing pushed):
- **anacrolix `60e7f385`:** `ClientConfig.InitialRequestQueue`, `RequestLowWater`; `request-queue.go`.
- **rivulet-engine `01229fa`:** sets 32 and 0.5.

Lab tool: `utplab-fork -iqueue/-lowwater`, `run-step4-lab.sh`. Data: `data/transport-lab/Q-*.txt`, `data/play-20261117-194144-hotd-step4-cold`.

## What changed
- **Before:** a peer's limit was twice the requests added at its last update, and an update happened only when its queue was empty. So the queue doubled once per drained batch, starting from 2. The rate cap used the connection's lifetime average.
- **Now:**
  - a new or newly unchoked peer gets 32 requests;
  - the queue grows by one per useful block (doubling per round trip) until two flat seconds;
  - after that it covers `RequestQueueTime` (3 s) of the peer's 5-second rate, at least 4 while the peer delivers;
  - it is refilled when half empty, at most every 100 ms per peer while requests remain.

## Lab (one uTP peer, 150 ms each way, 1 MiB window: ceiling ~3.3 MiB/s)
| Run | First data → 80 % of rate | Rate after first 5 s | Queue at ≤ 1 outstanding (after 1 s) | Total |
|---|---|---|---|---|
| F-plain (step 3, old queue) | 9.5 s | 2.60 MiB/s | 11 of 116 samples | 134.9 MiB / 60 s |
| Q-plain1 | 2.5 s | 2.48 MiB/s (sender-limited, below) | 0 | 134.5 MiB |
| Q-plain2 | 3.0 s | **3.28 MiB/s** | 0 | 179.8 MiB |
| Q-plain3 | 3.0 s | **3.30 MiB/s** | 0 | 180.5 MiB |
| F-S10 → Q-S10 (10 single losses) | 6.0 → 3.0 s | 1.94 → 2.46 MiB/s | 6 → 0 | 72.3 → **100.6 MiB** / 45 s |

Q-plain1 was the first run after the test qBittorrent started, with its 384 MiB seed file read cold. Our side wasn't the limit: 200–436 requests were outstanding throughout, with no loss and no holes, while the sender's rate climbed from 1.9 to 3.5 MiB/s. F-plain was also a first run after a restart, so the like-for-like comparison is runs 2 and 3: **acceptance met** (≤ 3 s ramp, ≥ 3.0 MiB/s, never empty).

## Real play: House of the Dragon S02E01, cold, same driver
| | Step 3 | **Step 4** |
|---|---|---|
| Swarm supply (useful MiB in 120 s) | 2192 | 2831 (comparable) |
| Metadata | 4.6 s | 7.0 s |
| First chunk → first piece verified (16 MiB) | 15.4 s (5.3 → 20.7) | **5.5 s** (7.3 → 12.8) |
| **First frame** | 23.7 s | **14.6 s** |
| Stalls after first frame | 0 | 1, 2.75 s |
| Slowest chunk write | 7.5 s (zero-fill, before 3b) | 144 ms |

- **First peer's limit after its first chunk at 7.3 s:** 57 → 200 → 532 → 810 within 3 s (the plan asked for `rate × 3 s` within 3 s). Several peers sat at 963, the local write-buffer cap.
- **Gains:** with 3b and step 4 the first frame is 9 s earlier than step 3's, and the first 16 MiB piece takes 5.5 s instead of 15 s. Metadata was 2.4 s slower this time, which is discovery, not this step.

## New: deeper queues overflow the uTP reorder buffer
One udp6 qBittorrent peer delivered 218 MiB with `ReorderTooFar = 793`: packets dropped beyond the 1024-packet reorder buffer, peak 993 (step 3: 1 drop). Deeper queues let fast uTP peers send bigger bursts, and our advertised window counts only the read buffer, not what sits in the reorder buffer (`findings/12`, B6). That is the evidence B6 was waiting for: cap the advertised window, or grow the reorder buffer to 2048 (the mask still fits a packet). Each drop costs a resend; 793 packets is ~0.5 % of that peer's data.

## Still slow at start (plan 05's territory)
- The tail request waited 1.6 s after the head (piece 1395 not ready).
- The next head pieces waited 6.4 s and 4.6 s (pieces 761, 762) while the queues were full of other pieces.
- Only the player's bytes should be urgent: offset-first chunks, exact tail from the SeekHead. That is plan 05.

## 4b: reorder bytes count against the uTP window (partial)
Commits: go-utp `58556c9` (`rcvWindow = buf − read − reorderBytes`, `Stats.ReorderBytes`), rivulet `d8a083f` (trace field). Lab script: `run-step4b-lab.sh`; data `W-*.txt`, `data/play-20261117-200202-hotd-step4b-cold`.

**Lab (no harm):** plain 180.3 MiB in 60 s (step 4: 179.8–180.5); S10 102.1 MiB (100.6); L40 140.1 MiB (step 3: 107.5, the gain is step 4's queue). No stalls, no drops beyond the buffer.

**Real play: the drops did not go away.**
| | Step 4 | 4b |
|---|---|---|
| Swarm, useful MiB in 120 s | 2831 | 2651 |
| uTP share | 508 MiB (17 %) | 191 MiB (7 %) |
| Packets dropped beyond reorder buffer | 793 (one udp6 peer) | **2163** (one udp4 qBittorrent 5.2.1, 16 MiB delivered) |
| First frame / stalls (context only) | 14.6 s / 1 × 2.75 s | 17.8 s / 1 × 4.59 s |

**Cause:** that peer sends **~528-byte packets** (24 packets = 12 672 B; 1011 packets = 533 KB). The reorder buffer is limited to 1024 *packets*, so it is full at ~530 KB, while the byte window still has ~515 KB open (`lastRcvWindow 514915` at the same sample). Counting bytes is right, but it can't bound the packet count when packets are small. The step 4 peer (udp6) was probably the same kind of sender; its packet size wasn't recorded then.

**Next fix (4c):** bound the window by packets as well. Advertise at most `(1024 − packets from ackNr to the highest buffered) × recent payload size`, tracked as a moving minimum or average of received payloads. Alternatively grow the buffer: the selective-ack extension length byte allows up to 2016 packets.

## 4c: reorder buffer sized for small-packet senders (done)
Commits: go-utp `2b04c51`, rivulet `35d5077`. Lab: `udprelay -mtu`, `run-step4c-base.sh`, `run-step4c-lab.sh`; data `M0-*`, `M1-*`, `X-*`; plays `play-20261118-035721-hotd-step4c-cold`, `play-20261118-040118-hotd-step4c-utpdiag`.

**Cause of the 528-byte packets.** libtorrent (qBittorrent) sends everything but MTU probes at `m_mtu_floor`, which is 576 − 20 − 8 = 548 bytes, or 528 of payload (`utp_stream.cpp:1532`, `utp_stream.hpp:977`). The floor rises only when a probe is acked (`:2216`); a lost probe lowers the ceiling (`:1982`, `:3421`). At 528 bytes our 1 MiB window is ~1990 packets against 1024 reorder slots. After any loss the sender legitimately ran past the buffer, and the probes dropped along with it kept libtorrent at the floor.

**Change.**
- The reorder buffer grows 1024 → 2016 packets, the most a selective ack can describe (a 252-byte mask).
- The window is also bounded by `free slots × payload estimate`; the estimate counts only after 16 payloads, so handshake messages don't shrink new connections.
- The selective-ack loop stops at the span.
- Stats gain `ReorderSpan` and `PayloadEstimate`; the trace now samples every uTP peer.

**Lab.** `udprelay -mtu 548` models a 576-byte path: the first oversize copy of each sequence number is dropped (a DF probe), and its resend gets through (fragmented). The first version dropped every oversize datagram, which black-holed libtorrent's same-size resends.

| Run | Before | After |
|---|---|---|
| 576-byte path, no loss | 123.4 MiB / 45 s (527 B/packet) | 121.6 |
| 576-byte path + 10 losses | 42.1 MiB, **1935 dropped**, 1447 resends | **92.7 MiB, 0 dropped**, reorder peak 1976 |
| Full-size S10 / L40 | 102.1 / 140.1 MiB | 99.1 / 140.9 |

One after-change run (the 576-byte S10 case) transferred nothing. Our window was open (957 600) and 32 requests went out, but the seeder sent no data. The rerun with the same build gave 92.7 MiB. The cause wasn't identified; it is noted here, not hidden.

**Real plays.**
- **First play:** almost no uTP data (3 sampled peers, all late), because the trace then skipped peers that choked us.
- **Diagnostic play, every uTP peer sampled:**
  - 13 uTP peers; 10 delivered 925 MiB of 2609 (**35 %**; earlier plays 7–17 %);
  - **0 dropped** beyond the buffer (step 4: 793; 4b: 2163), reorder peak **1985**;
  - the three largest uTP peers (qBittorrent 5.0–5.1, 224–277 MiB each) sent **521-byte payloads**: exactly the senders the old buffer dropped;
  - only one peer never got past its handshake (1 packet), against 3 that never unchoked us at all; so the receive path is healthy, and the first play's lack of uTP was peers choking us.
- First frame 15.5 s with one 3.35 s stall (the first play: 47.2 s, 0 stalls). Swarm-dependent, single runs.
