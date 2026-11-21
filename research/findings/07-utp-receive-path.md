# 07 — Why a far uTP peer gives us a third of what it gives qBittorrent

Third pass, 2026-11-16, read-only. Scope: the open question from `06` §4 (peer `peer-2` gave qBittorrent ~2 MB/s and Rivulet ≤ 700 KiB/s over uTP). I read our receiver (`go-utp/purego`, the pure-Go port of libutp that `CGO_ENABLED=0` builds use) against the sender most peers run (libtorrent 2.0 `utp_stream.cpp`, i.e. qBittorrent).

Status of each claim is marked: **[code]** = read in both code bases, **[calc]** = arithmetic from code constants, **[hyp]** = not measured.

Paths: `G` = `%USERPROFILE%\go\pkg\mod\github.com\anacrolix\go-utp@v0.0.0-20260908033909-9f1664acf866\purego`, `L` = `ref\libtorrent\src\utp_stream.cpp` (RC_2_0).

## 1. The governing number: one loss event costs ~20 s on a far peer

- libtorrent's sender halves its window on a loss event (`utp_loss_multiplier` 50, `L:2124`), leaves slow start, and then grows by at most `utp_gain_factor` = 3000 bytes per round trip (`settings_pack.cpp:340`). **[code]**
- At 250 ms RTT, 2 MB/s needs a 500 KB window. After one halving it needs 250 000 / 3000 = 83 round trips ≈ **21 s** to get back. **[calc]**
- With a loss event every `T` seconds the window saw-tooths, and the average rate is `1.5 × 3000 × T / RTT²`. At 250 ms: `T` = 10 s gives **720 KB/s**; 2 MB/s needs `T` ≥ 28 s. **[calc]**
- So the rate from a far uTP peer is set almost entirely by how often the sender *believes* a packet was lost, and it falls with RTT². The observed 700 KiB/s is what one loss event per ~10 s produces. The RTT to that peer was not measured (250 ms is my estimate for Brazil → `peer-2`). **[hyp]**

This reframes the gap: the question is not "what limits our throughput" but "what on our side makes a libtorrent sender see loss more often than qBittorrent does".

## 2. Our window-update acks are counted as duplicate acks by libtorrent (new)

**Receiver (ours).** Every `Conn.Read` that frees space calls `readDrained` (`G\conn.go:1351`). If the window is now larger than the last one advertised, it schedules a pure ack (`ST_STATE`) to announce it. The ack carries the unchanged ack number. Inherited from libutp's `utp_read_drained`. **[code]**

**Sender (libtorrent).** Any `ST_STATE` whose ack number equals the last acked one, while packets are outstanding, increments `m_duplicate_acks`; the advertised window is not compared (`L:2724–2729`). At 3 it declares the next packet lost, resends it and calls `experienced_loss` → window halved (`L:2814–2835`). TCP excludes window updates from duplicate-ack counting for exactly this reason (RFC 5681); libtorrent does not. libutp senders are immune (they resend only from selective acks), so this only bites with libtorrent peers, which are most of any swarm. **[code]**

**When three line up.** The counter resets whenever the acked sequence advances, so it needs three window updates with no new in-order packet between them, while the sender has newer packets in flight. That happens when our application has fallen behind and then drains a backlog in three or more reads during a gap in arrivals:
- The peer read loop reads through a 128 KiB `bufio` (`peerconn.go:992`) and handles every chunk (client lock, storage write) between reads, so a backlog over ~256 KiB drains in ≥ 3 reads more than 1 ms apart.
- A backlog forms whenever the read loop stalls: `MarkComplete` → `fsync` under the storage mutex (`06` §3), the NTFS zero-fill, client-lock contention, or the download limiter's `time.Sleep` inside the read loop (`ratelimitreader.go:40`).
- Worst case is a stall long enough to close the 1 MiB window: the first update is sent at once (`lastRcvWin == 0`), the sender answers with a burst, and our next updates arrive while that burst is still in flight → spurious loss on every such stall.
- Scheduled window updates are only flushed 1 ms after the next uTP packet on the shared socket, from any connection (`G\socket.go:282`, `readDrained` does not arm the timer), so traffic from slow peers turns them into stand-alone duplicates. **[code]**

**Prediction.** If each piece completion stalls the read loop once, `T` = piece size / rate, and the rate settles at `sqrt(1.5 × 3000 × piece) / RTT`: for 16 MiB pieces **≈ 1.1 MB/s at 250 ms**, 2.7 MB/s at 100 ms, independent of what the peer could send. Smaller pieces make it worse (4 MiB → half). This is in the range we measured, but how often the pattern really occurs is **not measured**. **[hyp]**

## 3. Our selective ack covers 30 packets; libtorrent's covers its whole reorder buffer

- We send a 4-byte mask, 30 packets past the hole (`G\conn.go:429`), though we buffer up to 1024 out-of-order packets. libtorrent as receiver sends up to an MTU of mask (`L:1536–1547`). **[code]**
- libtorrent as sender only removes selectively-acked packets from its in-flight count (`L:1253–1260`) and resends at most 5 holes per ack (`L:1228`). With our short mask, the ~300 packets we received beyond bit 30 stay "in flight", so the sender is window-blocked until the hole is filled, and each further hole in the same window is discovered one round trip later. **[code]**
- Result: a burst that drops `k` packets (what a 64 KiB OS socket buffer overflow produces; 64 KiB is 45 packets) costs about `k` round trips of near-zero goodput on top of the halving, against ~1 round trip between two libtorrent ends. **[calc]**

## 4. Smaller receive-path points
- The delay we report back is stamped when the reader goroutine processes the packet, after it has waited in the OS buffer and for the socket mutex (`G\conn.go:949, 1058`). Any lag of ours over 100 ms reads as congestion to the sender. Only matters during stalls. **[code]**
- One reader goroutine, one mutex and one OS buffer serve every uTP connection and the DHT (`06` §4). **[code]**
- The limiter sleeping in the read loop means a hard limit close to the real rate would itself trigger §2 on every libtorrent uTP peer. A throttle should shrink request queues, not delay reads. **[code]** for the sleep, **[hyp]** for the effect.

## 5. How to prove or kill this cheaply (no swarm needed)

1. **Loopback test, deterministic.** qBittorrent test profile seeds a local file; Rivulet downloads it from `127.0.0.1` with TCP disabled. On loopback nothing is lost, so **every retransmitted packet we receive is spurious by construction**. Count in a go-utp fork, per connection:
   - packets received whose sequence was already delivered (the "ancient" branch, `G\conn.go:982–988`);
   - pure acks sent with an unchanged ack number and no selective ack (window updates);
   - reorder-buffer inserts (real holes);
   - longest gap between `Conn.Read` calls and peak `readBuf` size.
   Inject a 200 ms stall in the read loop every few seconds to see the dependence. If spurious retransmits appear with stalls and vanish without, §2 is confirmed.
2. **One live run** with the same counters plus `UDPv4\Datagrams Received Errors` separates the three causes: spurious (§2), socket-buffer loss (§3), path loss (neither counter moves, yet retransmits arrive).
3. **Transport A/B on one peer**, sequentially: uTP then TCP to the same far libtorrent peer (already planned in `06`).

## 6. Fixes, in order of cost

| Fix | Where | Why it is proper |
|---|---|---|
| Send a window update only when the last advertised window was small (below ~¼ of the buffer or 2 packets) and has grown by a large step; otherwise let the next data ack carry it | go-utp fork, `readDrained` | Receiver silly-window avoidance, as TCP does (RFC 1122 §4.2.3.3). Removes §2 at the source and is upstreamable |
| Selective-ack mask sized to the reorder span, in multiples of 4 bytes, capped by MTU | go-utp fork, `sendAck` | Matches libtorrent; both libtorrent and libutp parse any length |
| OS receive buffer 4 MiB via `ClientConfig.ListenPacket` | Rivulet | Config only (`06` §4) |
| Keep the peer read loop from stalling: `fsync` outside the storage mutex, sparse files, no limiter sleep in the read path | anacrolix fork | Was item 14 in `06` §8; now it protects every libtorrent uTP peer, so it moves up |
| Prefer TCP for outgoing connections (dial TCP first, uTP after a short head start or on failure; still accept incoming uTP) | anacrolix fork dial path | A TCP sender recovers from loss in a few round trips instead of 80. libtorrent prefers uTP, so this is a place to beat qBittorrent on far swarms. Needs the A/B first |

## 7. What this changes in `06`
- §4's four hypotheses are replaced by §1–§3 here: the rate is set by loss-event frequency; two concrete receiver behaviours raise it, one of them (window updates) previously unknown.
- `06` §8 item 7 (uTP receive path) and item 14 (read-loop stalls) move up next to item 5: on a swarm whose supply is one far peer, they decide whether that peer delivers 0.7 or 2 MB/s, which is the difference between buffering and not for a ~2 MB/s stream.
- `06` §9 step 0 gains the loopback test and the four counters; step 5 (uTP) moves before step 4 (request model), because request-model gains cannot show while the transport saw-tooths.
- Lab swarm design: add delay to the "single fast far peer" scenario (the effect scales with RTT²; a zero-RTT lab hides it).
