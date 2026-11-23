# anacrolix/torrent: how Rivulet's engine library requests, dials and reads

Source: the fork (then its own repository, now `deps/torrent/`) (`main` = upstream `master` 753d7120, 2026-11-16, plus our uncommitted `request-health.go`). Line numbers are from that tree.

## 1. Which pieces are wanted, and in what order
**Priority of a piece** (`piece.go:381`, `purePriority`) = the highest of:
- its files' priority (`File.Download()` = Normal, `SetPriority`);
- reader-derived priority: the piece under a reader's position is **Now**, the rest of that reader's readahead window is **Readahead** (`torrent.go:279–283`, `reader.go:148`);
- an explicit `Piece.SetPriority` (Rivulet's head/tail priming uses Now).
Values: None 0, Normal 1, High 2, Readahead 3, Next 4, Now 5.

**Order across pieces** (`internal/request-strategy/order.go:24`, `pieceOrderLess`): priority (high first) → partially downloaded first → **availability, rarest first** → piece index.
**Order of requests for one peer** (`requesting.go:120`, `lessByValue`): choked-and-not-allowed-fast last → priority → requests it already holds → unrequested before stealable → (for stealable) biggest queue, most recent → **availability, rarest first** → for Readahead priority the piece index, **for every other priority a per-torrent random order** (`t.pieceRequestOrder`) → request index.

Consequences for streaming:
1. **Among equal-priority pieces, the rarest goes first.** In a swarm of streamers, partial peers hold the *start* of files, so head pieces have higher availability than tail pieces. Rivulet primes the file's head and tail both at Now → **the tail piece is requested before the head piece**. Measured (data/ab-1116-033153): for the first 60–70 s every peer's requests were on piece 398 (tail) and none on piece 0; the first byte only flowed after the tail piece completed.
2. **The rest of the file (Normal) downloads rarest-first, then randomly**, not sequentially. Seen in status dumps as scattered partial pieces (5, 6, 19, 20…) while the piece after the playhead waits.
3. Only pieces inside a reader's readahead window are fetched in index order, and Rivulet's window is 8–64 MiB (2 s of throughput): less than one 16 MiB piece at 4K speeds. So in practice only the piece under the playhead is ordered.

**Requestable set** (`order.go:50`, `GetRequestablePieces`): walks the order and stops once the pieces offered add up to `MaxUnverifiedBytes` (anacrolix default 64 MiB, counting each piece's full length). With disk storage the order is **per torrent** (`torrent-piece-request-order.go:36`: per-torrent key unless the storage has a shared capacity), so the default allowed 4 requestable 16 MiB pieces **per torrent**. (Correction: earlier in the session I said "client-wide"; that is only true for the in-RAM cache, whose capacity is shared.) Upstream 131d9597 also stops the walk at the first unwanted piece.

## 2. How requests are issued: batch, drain, double
- All requests assigned to a peer go on the wire immediately; there is no local unsent queue (`applyRequestState`, `requesting.go:403`).
- Queue cap (`nominalMaxRequests`, `peerconn.go:1776`): `min(peer's reqq, 2 × peakRequests, 963)`, at least 1. `peakRequests` = how many requests the **last update added** (`requesting.go:486`), decremented on cancel/reject.
- **A request update for a peer runs only when its queue is completely empty** (`isLowOnRequests`, `peerconn.go:2104`): after a received chunk (`:2132`), on priority changes (`torrent.go:1672`, only for peers that are low), on unchoke/have/bitfield, after a cancel or reject.
- So the pattern is: send N, wait for all N, then send up to 2N. Effects:
  - a slow peer is stuck at a queue of 1–2 (it never drains more than it was given);
  - a fast peer's queue doubles until it holds 963 blocks (15 MiB): one peer takes almost a whole 16 MiB piece, for ~30 s at 500 KiB/s, and nothing re-plans that queue in the meantime (a seek or a new urgent piece only reaches peers whose queues are empty);
  - each batch ends with one idle round trip.
- The periodic update timer exists but is compiled out (`enableUpdateRequestsTimer = false`, `requesting.go:508`; disabled 2022-05-11 in 669c69fa with no stated reason, in a series of request-performance commits).

## 3. Stealing: balances queue *counts*
`applyRequestState` (`requesting.go:440–468`): a peer may take a request another peer holds only if afterwards it would not have more requests than the holder (ties go to whoever received a chunk more recently), and after `StealRequestGrace`. A reject blocks further adds until the next unchoke (`:436`).
- It never considers speed or deadlines. A stalled peer keeps its **last** request indefinitely (holder has 1, stealer would have ≥ 1). A 16 MiB piece needs all 1024 blocks, so one stalled block holds the piece.
- There is no timeout, no snubbing, no duplicate request upstream.

## 4. Reading: verified pieces by default, **"responsive" mode exists**
- `reader.available` (`reader.go:125–135`): by default a read waits until the **whole piece is complete and hash-verified**; with `Reader.SetResponsive()` it returns each chunk as soon as it is downloaded and written (`haveChunk`, `torrent.go:1517`).
- **Rivulet never calls `SetResponsive()`.** With 16 MiB pieces the player's first byte therefore needs a full verified piece, and every block of it, including the slowest peer's.
- Measured side by side (data/ab-1116-033153, same moment, same swarm): verified reads → first byte at 115 s; responsive reads → 73 s, 5 MB at 80 s, 16 MB at 95 s. (Both were held back ~70 s by the tail-before-head order of §1.)
- Risk of responsive mode: a chunk is handed to the player before its piece is hashed. If the piece later fails, anacrolix re-downloads it and smart-bans the peer whose block differed (`torrent.go:1356–1465`), but the player has already consumed the bad bytes (a visible glitch, not a crash). Peers sending bad data are rare on public swarms of scene releases; the trade is worth it for start-up, and can be limited to large pieces.

## 5. Dialling: slow and in random order
- Limits (`config.go:328–356`): 25 half-open per torrent, **100 half-open client-wide**, **10 dials per second client-wide** (`DialRateLimiter`, burst 10, applied in `dialAndCompleteHandshake`, `client.go:910`), 50 established per torrent.
- Dial timeout (`reducedDialTimeout`, `client.go:793`): `20 s ÷ ((pending + 25) / 25)`, floor 3 s: ~4 s with 120 addresses waiting, 10 s with 26, 20 s once fewer than 25 wait. Then a 4 s handshake timeout.
- Order (`prioritized-peers.go`): `Trusted` peers first, then the BEP 40 priority (a hash of both addresses, effectively random), then an address hash. Source (tracker/DHT/PEX), past success and scrape results play no part.
- Measured: 143 known addresses → pending 123 / 80 / 26 / 0 at 10 / 20 / 30 / 40 s, half-open pinned at 25 until ~60 s. A productive peer can sit behind dead DHT addresses for 30–40 s. libtorrent tries all of them in ~5 s (30/s, no half-open cap, 3 s uTP timeout).
- In a race or preload of several torrents the 10/s and 100 half-open limits are **shared**: 5 torrents × 140 addresses = 70 s of dial queue.
- Each address is dialled on TCP and uTP at once; first to connect wins (`DialFirst`).

## 6. Uploading while leeching
`uploadAllowed` (`peerconn.go:1334`): while the torrent is incomplete we unchoke a peer only if it has pieces we want, and stop once we have sent it 100 KiB more than it sent us. So we never pay first. libtorrent leechers rank peers by bytes received in the last 15 s round (findings/01 §8), so we only ever get their spare and optimistic slots.

## 7. What our uncommitted `request-health.go` changes, and what it does not
- Does: caps the queue at `rate × 3 s` (min 2), snubs after 20 s without a useful chunk (cancels that peer's requests), re-runs the update for idle unchoked peers every second.
- Does not: refill before the queue is empty (still batch-and-drain); order by deadline; use an adaptive timeout (libtorrent's is 2–60 s from the peer's own response times); avoid Cancel (libtorrent just frees the block); let a fast peer duplicate a late block; fix the ordering or dialling above.
- The rate it uses is a lifetime average (`downloadRate`, `peer.go:187`: useful bytes ÷ total time spent expecting chunks), which reacts slowly.
- Verdict: a patch on a model that is wrong for deadlines. See `design/30-streaming-scheduler.md`.

## 8. Things anacrolix does well (keep)
- Pure Go, one static binary, clean storage interface (Rivulet's RAM cache builds on it).
- Readers as first-class objects that drive priorities; responsive reads; per-file priorities.
- Fast-extension support (allowed-fast requests while choked), holepunching (BEP 55), webseeds, WebTorrent, DHT with BEP 33 scrape (used by our `/scrape`).
- An active upstream (62 commits in the 11 days before this read), receptive to performance work.
