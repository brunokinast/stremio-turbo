# 09 — Re-reading the recorded runs against the code

Fifth pass, 2026-11-16. No new runs. I re-read `data/diag2.txt`, `data/qbt-peers-*.json` and `data/rivulet-status-150s.txt` with what `06`–`08` established, fetched Torrentio's public list for the episode once, and checked three code points. Marks: **[data]** read from a recorded file, **[code]** read in source, **[calc]** arithmetic, **[hyp]** inference.

## 1. What the reference swarm really is
- Torrent `9247c52e…` has **2711 pieces of 16 MiB ≈ 43 GB: it is a season pack**. Our episode is 406 of them (`diag2.txt`: `wanted 406`; status dump: `514/2711 completed`). **[data]**
- qBittorrent at 150 s lists 22 peers, but only **5 are connected** (the rest have no client string, no flags, 0 %). Rivulet's dump shows the same 5 clients. **Connection count is not the gap.** **[data]**
- The two seeds are nearly useless: 27 and 24 KB/s to qBittorrent; to Rivulet one seed gave 3–16 KiB/s over TCP while unchoked with requests queued, the other choked us for the whole run. **[data]**
- All real supply is two or three **leechers that already hold our episode**: `peer-2` (525 pieces; it had pieces 0, 1, 2 and 398) and `216.183…` (761 pieces). They connected **to us** (incoming uTP) at about 28 s and 65 s. **[data]**
- The fast peer geolocates to the US in qBittorrent's list, so my 250 ms RTT in `07` is probably high. `07`'s formula goes with 1/RTT², so its numbers need the measured RTT before they mean anything. **[data]** for the country, **[hyp]** for RTT.
- Torrentio lists six 4K files for this episode, with 9, 8, 7, 1, 0, 0 seeds, and no 4K file appears in two torrents. Merged swarms (`06` §8 item 15) would not help 4K here; it would help 1080p (26 files appear in more than one torrent). **[data]**

Consequence: for this title the engine is competing for two or three peers. Start time is dominated by when those peers show up and what we ask them for first.

## 2. Timeline of the instrumented run (`diag2.txt`, first byte at 72 s)

| Time | What happened | Cost |
|---|---|---|
| 0–28 s | Only the throttled seed delivers (3–10 KiB/s) | 28 s with no usable peer |
| 28–50 s | Fast peer connected. Request limit grows 8 → 256 → 963; rate 39 → 213 → 576 KiB/s | ~20 s of ramp |
| 50–60 s | Fast peer's queue: **947 requests on the tail piece 398, 2 on piece 1** | The best peer spends ~40 s (55–95 s) on the tail piece |
| until 72 s | Piece 0 waits for up to 26 chunks assigned to the 13 KiB/s seed | Piece 0 completes long after its bytes could have arrived |
| 70–90 s | 258–320 requests on the fast peer are in the **cancelled** state; last useful chunk 2.8–3.8 s ago | Queue churn when the second fast peer appears |
| 100–110 s | Second fast peer: unchoked, wanted, **0 requests for 10 s** | Idle supply (known, E5) |

## 3. New findings from this

**3a. Our request ramp throttles the sender's own ramp.** **[code]+[data]**
- libtorrent only grows its uTP window when it has enough queued data to fill it (`cwnd_saturated`, `utp_stream.cpp:3259, 3288`). A short request queue leaves it application-limited, so its window does not grow at all.
- Our limit doubled about once per 2 s (8 → 256 in 10 s), because it grows only when the queue drains. libtorrent as a downloader adds one request per block received, which doubles per round trip.
- Result: about 20 s to reach a deep queue instead of 1–2 s. Fix belongs to the request model: start a newly unchoking peer with a deep queue (on non-urgent pieces if it is unproven) and grow per received block.

**3b. The rate grew linearly, so the sender was out of slow start almost from the first second.** **[calc]+[hyp]**
- 39 → 576 KiB/s in 20 s is about 27 KiB/s per second. LEDBAT's linear growth is `3000 / RTT²` bytes/s per second, which gives that slope at RTT ≈ 0.33 s. An exponential ramp would have reached 576 KiB/s in 1–2 s.
- libtorrent leaves slow start on loss, on `cwnd > ssthres`, or when the delay it computes from **our** timestamp replies reaches 100 ms (`utp_stream.cpp:3266–3274`). So one of these happened at a tiny window: a spurious loss (`07` §2), a late timestamp from our reader (`07` §4), or 3a keeping it application-limited (then it is not slow start that ended, the window just never moved). The lab (`plans/done/01-transport-lab-design.md`, results in `10`) separates them.

**3c. The tail piece takes the best peer.** **[data]**
- Head and tail are both at priority Now; the fast peer's whole queue (15 MiB) went to piece 398 while the next piece the player needs had 2 requests. With whole-piece reads and ~500 KiB/s, the tail costs ~30 s of the only good peer before the player has anything.
- This confirms `06` §8 items 1–2 as the largest start-up lever on this swarm, with numbers: head + tail as whole pieces ≈ 64 s at 500 KiB/s; as the bytes actually read (a few MB of head, tens of KiB of tail) ≈ 10 s.

**3d. Chunks of the urgent piece sit on the slowest peer.** **[data]**
- Up to 26 chunks of piece 0 were assigned to a 13 KiB/s seed (416 KiB ≈ 32 s) while a 500 KiB/s peer was connected. A piece is as late as its slowest chunk.
- Rule for the request model: a chunk of an urgent piece may only go to a peer whose expected delivery time (queue ahead ÷ measured rate) beats the piece's deadline; otherwise that peer gets non-urgent pieces.

**3e. Cancel storms between similar peers.** **[data]+[hyp]**
- When the second fast peer arrived, a third of the first peer's queue was cancelled. With ~0.3 s RTT most cancelled chunks are already in the sender's pipeline, so cancelling gives up our place without saving time. Stealing should require a clear margin (the thief's expected delivery well before the victim's) and never apply to chunks likely already in flight.

**3f. The uTP reader waits on the client lock.** **[code]**
- Every incoming uTP SYN calls `Client.firewallCallback`, which takes the client read lock (`anacrolix-torrent\client.go:539–542`), from inside the single uTP reader goroutine while it holds the socket mutex (`go-utp\purego\socket.go:425`). A long hold of the client lock therefore freezes every uTP connection and the DHT on that socket. Fix: answer from an atomic flag.

**3g. Useful peers find us; we do not find them.** **[data]+[hyp]**
- Both productive peers were incoming. Trackers only returned the two seeds. So time-to-supply depends on how fast our address spreads (DHT announce, tracker announce, PEX) and on being reachable (UPnP worked).
- Test instances start a fresh process with an empty DHT table, while qBittorrent's profile keeps its DHT state. Part of every "time to first byte" comparison so far measured a cold DHT. Check whether Rivulet persists DHT nodes between runs; if not, add it, and measure discovery cold vs warm.
- The strongest lever here is already built and untested: smart-play **preload**. Every second the torrent is open before Play is a second of discovery and ramp not paid on screen. For series, the pack torrent should stay open between episodes: the peers that hold episode N+1 are the ones already connected.

## 4. Corrections
- `07` §1: RTT assumption (250 ms) is unsupported; peer is in the US. Treat the 700 KiB/s ↔ "one loss per 10 s" match as unverified.
- `06` §1 said one fast peer; there are two or three, all leechers holding our episode, all incoming.
- The earlier target "first 16 MiB within 1.5× of qBittorrent" compared a warm-DHT client with a cold one.
- The "player" in `diag2.txt` was a sequential HTTP reader, not mpv. mpv is expected to need the tail (Cues and Tags) right after the first header bytes, so in the real app the first frame of that run would have come when the tail piece completed, near 97 s, not at 72 s. The tail is therefore needed early, but as a few hundred KiB, not as a 16 MiB piece ahead of the head's next piece (`plans/05` A1 and B1).

## 5. The reference file itself (parsed from the cached `.part`, 2026-11-16)
Read from `%LOCALAPPDATA%\rivulet-engine\9247c52e…\…S06E01….mkv.part` with a 40-line EBML parser. **[data]**

| Element | Position | Note |
|---|---|---|
| SeekHead | byte 52 | lists everything below |
| Info, Tracks | bytes 4151–7118 | all headers end at byte 9552 |
| First Cluster | byte 9552 | clusters are ~477 KiB each |
| Cues | byte 6 686 667 960 | ~607 KiB long |
| Tags | byte 6 687 274 720 | directly after the Cues, at the very end |

What follows:
- The player needs **under 10 KiB of head** before it can look for the trailing elements, then **about 0.6–0.7 MiB at the very end** (Cues and Tags), then clusters from the start. A few MiB in total before the first frame, against the two whole 16 MiB pieces it gets today.
- Both Cues and Tags trail the clusters. mpv defers the end-of-file read only when the Cues are the sole trailing element, so expect a tail read at every fresh start of this kind of file (to be confirmed by the access log).
- **The SeekHead in the first 120 bytes gives the exact tail range.** The server can read it from the first chunk and prime exactly `[Cues position, end of file)` instead of a guessed 8 MiB window.
- The tail piece (398) holds 9.3 MiB of ordinary video, then the 0.6 MiB the player waits for, then the first 6 MiB of the next file in the torrent. Only ~4 % of that piece is needed. The cached `.part` shows this cost for real: it ends exactly 250 chunks into piece 398 (ascending order from chunk 0), 5.2 MB short of the Cues. Whoever fetched that got none of what the player needed from 4 MiB of download.
- **`prefetchNext` warmed the wrong episode.** The next file by torrent index is E05, not E02, and an `…S06E05….mkv.part` of 7.28 GB exists in the cache: its tail was written, which on a non-sparse file means the whole 7 GB was zero-filled for an episode nobody asked for.
- The `.part` files are as long as the highest byte written, not sparse: this confirms `06` §3 on this machine.
- Checked in mpv's current source (`demux/demux_mkv.c`, the `only_cue` loop in `demux_mkv_open`): trailing elements are read during open, in ascending position, unless the Cues are the only one. With Cues and Tags both trailing, open blocks on the tail. There is no demuxer option that skips this for a seekable stream (`index=recreate` skips parsing the Cues but still seeks there, and Tags are still read). So the tail read cannot be configured away in mpv; it has to be made cheap on the server (`plans/05` A1, B1). The shell's bundled mpv may be older than master; confirmed with the shell's own libmpv in `10` §A.

