# Plan 03 — Transport fixes and read-loop stalls (step 3)

> **Status 2026-11-17** (`findings/12`):
> - **Built:** B2, B3, B4, the B6 counter (new: drops ahead of the reorder buffer were uncounted), and S1 (new, replaces A2/A3). S1 moves file opens for reads, syncs and the part-file stat off the storage mutex and out of MarkComplete.
> - **Skipped on evidence:** A4 (no lock holds), A5 (no limit set), B5, and C1 (no same-peer comparison).
> - **B6 window fix done later (step 4b/4c, `findings/13`):** deeper queues made it matter. The window counts reorder bytes, and the reorder buffer holds 2016 packets for libtorrent's 528-byte floor packets.
> - **A1 done in step 3b** (2026-11-17): cache files are sparse on Windows, and the 7.5 s first tail write is gone.

Goal: get the full rate out of each of the few peers that feed us, and stop multi-packet loss from freezing a stream for seconds. Inputs: step 1 (lab verdicts per hypothesis, real-swarm RTT) and step 2 (storage timings, lock holds, the go-utp fork with counters). Each item names the evidence that must exist before it is built; skip an item whose evidence came back negative and say so in the report.

Why this comes before the request-queue and start-up work: the lab showed a loss burst of `k` packets costs ~`k` round trips of near-zero goodput with our receiver (`findings/10`, S10: 3 s for 10 losses; L1/L5: 12 s and 28 s). That is buffering the player sees, and it also hides any gain from plans 04–05.

## Part A — Read-loop stalls (anacrolix fork, `storage/`)
A stall in a peer's read loop stops that peer's transport reads, because chunk writes are synchronous in the loop (`peerconn.go:1000–1023`).

| # | Change | Evidence needed | Where |
|---|---|---|---|
| A1 | Create cache files sparse on Windows | Plan 02: first tail write or first `Sync` takes more than ~200 ms on a fresh multi-GB file | `storage/file-io-common.go:13` opens with plain `os.OpenFile`; `sys-lame.go` covers Windows. Add a Windows file that issues `FSCTL_SET_SPARSE` right after creation (`golang.org/x/sys/windows.DeviceIoControl`), behind the same interface the Unix build uses. Also fixes the cache budget seeing a full-size file |
| A2 | `Sync` outside the storage mutex | Plan 02: any `Sync` over ~50 ms | `classicFileIo.flush` (`file-io-classic.go:51–59`) holds `me.mu` across `f.Sync()`, and every write open and read open takes that mutex. Take the handle under the lock, sync after releasing it; make sure a concurrent close cannot invalidate the handle (reference count or a per-file lock) |
| A3 | Mark the piece complete for readers before persisting completion | Plan 02: gap between "hash passed" and "reader unblocked" over ~20 ms | `Torrent.pieceHashed` (`torrent.go:2804–2822`) waits for `MarkComplete`. Order: set completion in memory and wake readers, then persist. On a persist error, log and leave the in-memory state (the data is verified) |
| A4 | uTP accept check without the client lock | Plan 02: client-lock holds over 50 ms exist | `Client.firewallCallback` (`client.go:539–549`) takes the client read lock inside the uTP reader goroutine. Keep an atomic boolean updated wherever `wantConns()` or `AcceptPeerConnections` change |
| A5 | No sleeping in the read loop for the download limit | Only if the user's hard limit is ever near the real rate | `ratelimitreader.go:40`. Apply the limit by shrinking request queues instead. Low priority |

## Part B — uTP receiver (go-utp fork at `C:\Users\bruno\Documents\git\go-utp`, created in step 2)
Create `brunokinast/go-utp` as the remote only with the user's OK; until then the anacrolix fork uses a local `replace`. The lab copy (`lab\transport\go-utp`) has working versions of B2 (`sendAck` with a 256-byte mask buffer) and B3 (`SetReadBuffer`); port the idea, not the env switches.

| # | Change | Evidence | Where |
|---|---|---|---|
| B2 | **Selective ack sized to the reorder buffer** | **Proven** (`findings/10`): S10 stalled ~3 s at 130–380 KiB/s; S10-bigsack had no stall, every hole repaired in one round trip | `sendAck` (`purego/conn.go:~412–448`). Mask length = reorder span rounded up to 4 bytes, capped by what fits in the packet. Both libtorrent and libutp parse any length. Unit test: holes beyond bit 30 appear in the mask |
| B3 | **4 MiB OS receive buffer** | **Proven** (`findings/10` §C): loss clumps after the relay in 9 of 13 default-buffer runs, 0 of 8 with 4 MiB; Windows does not count them as receive errors | Rivulet: `ClientConfig.ListenPacket` (`config.go:109`) creating the UDP socket with `SetReadBuffer(4 << 20)`. Config only, no fork |
| B4 | Arm the ack timer for updates scheduled from `Read` | Code: `readDrained` → `scheduleAck` does not arm the timer (`socket.go:282`), so the update waits for the next packet from any connection | Small correctness fix |
| ~~B1~~ | ~~Window updates only when they matter~~ | **Dropped**: H1 not shown, with or without stalls (`findings/10` §C) | `readDrained` (`:1351`): immediate update when the last advertised window was zero (already so); otherwise only if it was under a quarter of the buffer and has grown by a quarter |
| B5 | Denser acks | Not shown harmful yet: ~1 ack per 50 packets makes the sender bursty, which is what overflows a small socket buffer. Revisit only if bursts still drop packets after B3 | Ack batching in `socket.go` |

Real-swarm evidence (`findings/10` §E): with B2+B3, uTP peers delivered ~3.6× more on a live 4K HDR swarm (RTT median 263 ms). Bursts above ~800 packets still recovered slowly because they overflow the 1024-packet reorder buffer: while doing B2, check the reorder buffer covers the advertised window (1 MiB ≈ 750 packets, so overflow means the sender exceeded our window or the buffer counts differently), and measure again after B3/B5.

Acceptance: re-run S10 and step 1's `-burst 40` run with the fork and no env switches: no multi-second stall after the losses; plain runs show no spontaneous hole bursts. Then one real play with step 2's capture. Offer B2 and B4 upstream to anacrolix/go-utp **only if the user asks**; never push there.

## Part C — Transport choice
| # | Change | Evidence needed |
|---|---|---|
| C1 | Prefer TCP for outgoing connections: dial TCP first, start uTP after a short head start or on failure; keep accepting incoming uTP | A same-peer comparison (uTP then TCP to one far libtorrent peer, sequentially, with step 2's per-peer samples) shows TCP at least 1.5× faster after B2–B3. Both productive peers in `findings/09` were incoming uTP, so this only helps peers we dial |
| C2 | Leave as is | If B2–B3 close the gap, C1 is not needed |

Where: `client.go:921–937` dials both and takes the first. Make the head start a `ClientConfig` duration, default 0 (today's behaviour).

## Order inside this plan
1. B3 and A4 (tiny, safe).
2. B2, then B4 (the proven stall fix).
3. A1–A3, each only with its step 2 timing.
4. C1 last, only with its evidence (B1 dropped).

## Done when
Lab re-run and one real play are recorded in `findings/12-transport-fixes.md`, with before and after per item, and the forks' tests pass per their `CLAUDE.md`. Report to the user and stop.
