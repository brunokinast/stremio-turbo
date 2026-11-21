# libtorrent (RC_2_0): how the reference engine gets data out of a swarm

Source: `arvidn/libtorrent` branch `RC_2_0` (qBittorrent 5.x builds on 2.0.x). Line numbers are from the clone made on 2026-11-16.
This is what qBittorrent ran when it got 16 MiB playable in 18 s and ~1.9 MB/s on the reference torrent.

## 1. Two request queues per peer, sent just in time
- Each peer has `m_request_queue` (picked, **not yet sent**) and `m_download_queue` (sent, awaiting data). `add_request` (`peer_connection.cpp:3598`) appends to the local queue; `send_block_requests` moves at most `m_desired_queue_size` onto the wire.
- Consequence: blocks that are only queued locally can be reordered, dropped or given to another peer for free. Urgent blocks jump the local queue (`make_time_critical`, `:3575`; `add_request` with `time_critical` inserts at `m_queued_time_critical`).
- anacrolix has no local stage: every request it assigns goes on the wire at once (up to its cap), so reassigning costs a Cancel and a round trip.

## 2. Request queue depth = 3 seconds of the peer's measured rate
- `update_desired_queue_size` (`:4792`): `desired = request_queue_time(3 s) × download_rate / block_size`, clamped to `[min_request_queue = 2, max_out_request_queue = 500]`. Snubbed peers get 1.
- Slow start (`m_slow_start`, initial desired size 4, `peer_connection.hpp:1087`): +1 to the queue **per block received** (`:2829`), until the rate stops growing by more than 5 kB/s in a second (`second_tick`, `:5086`), a snub, or a choke.
- So a fresh 1 MB/s peer reaches a ~190-block queue within a couple of seconds, and never holds more than ~3 s of work.

## 3. Timeouts: adaptive, and they release the block without cancelling
- `request_timeout()` (`:4563`): mean block round-trip + 4 × deviation, rounded up, clamped to `[2 s, request_timeout = 60 s]`. A peer that normally answers in 0.5 s is snubbed after ~2 s of silence. Before any sample: 60 s.
- `piece_timeout = 20 s` (`settings_pack.cpp:255`): no block at all for 20 s with requests outstanding → snub.
- `snub_peer` (`:5140`):
  - drops the **unsent** local queue back to the picker;
  - marks the last outstanding block `timed_out` and frees it in the picker **only if the piece has no other free blocks** (otherwise it isn't holding the piece up; see the linked blog post "block request time-outs");
  - does **not** send Cancel: if the snubbed peer delivers later, the data is still used;
  - queue size becomes 1; before releasing the timed-out block it picks one new block so the same peer doesn't re-pick the same one;
  - snubbed peers are excluded from time-critical requests (`can_request_time_critical`, `:3557`) and pick the *most common* pieces (`piece_picker::reverse`) so they stay out of the way.

## 4. Busy blocks (duplicate requests), tightly rationed
- Normal mode (`request_blocks.cpp`, `request_a_block`): a peer with **nothing else to pick and no outstanding requests** may request one block that another peer already has outstanding ("busy"), and only one busy block per peer at a time. With `strict_end_game_mode = true` this only starts when every wanted block is already requested.
- Time-critical mode lifts the limit: see §5.

## 5. Streaming mode: `set_piece_deadline` → `request_time_critical_pieces` (`torrent.cpp:11193`)
Called once per second while any piece has a deadline. This is the part qBittorrent does **not** use (it uses plain sequential download); streaming front-ends built on libtorrent (Elementum, Popcorn-Time-style apps) do.
- Candidates: peers that are unchoked, interesting, not snubbed, not on parole, with queue < 2 × desired (`can_request_time_critical`).
- Sorted by `download_queue_time()` (`:1084`) = (outstanding bytes + queued time-critical bytes) ÷ rate. Rate is the current rate; the **peak** rate if nothing arrived for 30 s; the torrent's **average per-peer rate** for a peer unchoked < 5 s ago (so new peers aren't assumed slow). Floor 50 B/s.
- The slowest 10% of candidates are dropped.
- For each deadline piece, in deadline order, `pick_time_critical_block` (`:10999`) gives each free block to the peer that would **deliver it soonest**, re-sorting after each assignment, and stops once the best peer's queue exceeds 2 s.
- Horizon: pieces whose deadline is further away than `average_piece_time + 4 × deviation + 1 s` are left to the normal picker, so it doesn't request the whole file urgently.
- Stalls: if all blocks of a deadline piece are requested and nothing was requested for more than `average_piece_time + deviation/2`, each elapsed multiple allows **one more duplicate request per block** (`pick_busy_blocks`, `:10942`), fewest-duplicated blocks first, from a different peer.
- While any deadline piece exists, the normal picker runs with `time_critical_mode`: no whole-piece affinity, and ordinary requests don't crowd the queues.

## 6. Piece picking that matters here
- `picker_options` (`:915`): sequential if the torrent is sequential; otherwise random + finish partials until 4 pieces are complete (`initial_picker_threshold`), then rarest first.
- `whole_pieces_threshold = 20 s`: a peer fast enough to fetch a whole piece in 20 s prefers whole pieces (window capped at 8 MiB, so it never applies to 16 MiB pieces).
- `auto_sequential = true`: switches to sequential automatically when the swarm is seed-heavy (rarest-first has nothing to optimise).
- Fast extension: when choked, picks only from the allowed-fast set (`request_blocks.cpp`, `fast_mask`).

## 7. Connecting to peers
- **No half-open cap.** `connection_speed = 30` new attempts per second (`settings_pack.cpp:269`), plus `torrent_connect_boost = 30` attempts fired immediately when a torrent gets its first peers (`torrent.cpp:3840`).
- Timeouts: `peer_connect_timeout = 15 s` (TCP), `utp_connect_timeout = 3 s`, `handshake_timeout = 10 s`.
- `connections_limit = 200`; `peer_turnover = 4%` of peers are disconnected periodically (worst first) to try new ones; `min_reconnect_time = 60 s`, `max_failcount = 3`.
- A swarm list of 150 mostly dead DHT addresses is therefore fully tried in ~5 s.
- Resume data stores the peer list, so a restarted torrent reconnects to known peers first.

## 8. Upload side: who gets unchoked (what the *other* peers do to us)
Most of the swarm runs libtorrent (qBittorrent, Deluge), so this is the behaviour we face.
- `unchoke_interval = 15 s`; `unchoke_slots_limit = 8` in libtorrent (qBittorrent's own default is 4 upload slots per torrent); one optimistic unchoke rotates every `optimistic_unchoke_interval = 30 s`.
- Ordering (`choker.cpp`, `compare_peers` then `unchoke_compare_rr`):
  1. peers that **sent us more bytes in the last round** come first (tit-for-tat; for a seed everyone is 0);
  2. round-robin: an unchoked peer **keeps its slot** until it has received `seeding_piece_quota = 20` pieces **and** been unchoked ≥ 1 minute. With 16 MiB pieces the quota is 320 MiB;
  3. then the peer it is **uploading to fastest**;
  4. then whoever has waited longest.
- What this means for a streaming client:
  - a seed's slot is worth up to 320 MiB, and keeping it depends on **pulling fast**: a peer with a tiny request queue ranks last among the unchoked and is the first to lose the slot;
  - leechers (the peer that gave qBittorrent most of its data was at 19%) prefer whoever **uploads to them**; a client that uploads nothing only gets their spare and optimistic slots;
  - after a choke the wait is a rotation (15 s rounds, longest-waiting first), so staying connected and interested is what gets the next slot.

## 9. What libtorrent has that anacrolix lacks (inputs for the gap table)
1. Local (unsent) request queue → free reassignment and urgent-block reordering.
2. Queue depth from rate × 3 s with per-block slow start.
3. Adaptive per-peer block timeout (2–60 s), snub without cancel, release only when it blocks a piece.
4. Deadline pieces with earliest-delivery assignment and rationed duplicate requests.
5. Dial pacing by rate (30/s + boost 30) with 3 s uTP / 15 s TCP timeouts, instead of a small half-open cap.
6. Peer turnover and persistent peer lists.
7. A documented choker we can reason about (and that the swarm runs).

Not better, just different: disk cache, hashing threads, uTP implementation, DHT. Not examined.
