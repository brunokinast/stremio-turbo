# 12 — Transport and storage fixes (step 3)

Date: 2026-11-17. Builds on `findings/10` (lab) and `findings/11` (baseline plays). Everything is on the local `wip/streaming-research` branches; nothing is pushed.

| Repo | Commit | Item |
|---|---|---|
| go-utp (local only) | `4857f90` | B2: selective ack covers the whole reorder buffer |
| go-utp | `1989d0f` | B4: reads that reopen the window arm the ack timer |
| go-utp | `d66c5d3` | B6 counter: data packets dropped ahead of the reorder buffer are counted (`ReorderTooFar` could never fire before) |
| anacrolix-torrent | `8a8532ad` | S1: opens, syncs and stats off the chunk write path (with opt-in `TestFileWriteLatency`) |
| rivulet-engine | `c1e1a0d` | B3: 4 MiB OS receive buffer on the uTP/DHT socket (`ClientConfig.ListenPacket`) |

Lab tools: `lab/transport/utplab-fork/` (utplab against the fork, stats from `Conn.Stats()`, 4 MiB buffer), `run-step3-lab.sh`; `run-lab.sh` takes `UTPLAB=`. Data: `data/transport-lab/F-*.txt`, `data/play-20261117-042100-hotd-step3-cold`.

## Lab (relay, 150 ms each way, test qBittorrent sender)

| Run | Before (step 1) | After (fork) |
|---|---|---|
| S10: 10 single losses 50 apart at 20 s | 20.5–23.5 s at 128–288 KiB/s (one hole per round trip); 68.1 MiB in 45 s | holes repaired within 0.5 s (4000 KiB/s at 21.0 s); 72.3 MiB in 45 s |
| L40: 40-packet burst at 20 s | ~5 s at zero, then ~300 KiB/s for 5 s more; 55.1 MiB in 60 s | one 0.5 s dip, back to full rate at 21.0 s; **107.5 MiB** in 60 s |
| Plain 60 s | B runs: loss clumps in 9 of 13 | no holes (0 reorder inserts), 0 OS receive errors; 134.9 MiB |

`ReorderTooFar` stayed 0 in the lab, as expected on a single paced sender.

## Storage: the cause of the slow writes, found and fixed

`TestFileWriteLatency` (anacrolix `storage/`, opt-in) reproduces the real play: 16 writers of 16 KiB chunks at 30 MiB/s, bolt completion, a player (64 KiB × 48/s) and uploads (16 KiB × 200/s) reading completed pieces.

| Load: writers + player + uploads | Before | After |
|---|---|---|
| Chunk write mean | 1.42 ms | 0.19 ms |
| Time in writes / wall | 2.65 | 0.36 |
| Read mean | 4.2 ms | 0.18 ms |
| MarkComplete max | 150 ms | 1.6 ms |

Without readers, writes were already 0.08–0.23 ms: the real play's 1.3–2.0 ms came from reads. Three causes:
1. `classicFileIo` held its client-wide mutex across `os.Open` (~5 ms on this machine) for **every read**, and every chunk write takes that mutex. Now the mutex guards only the handle map; reads reuse the cached write handle, reference counted so closing waits for them.
2. The buffered checkpoint ran `f.Sync()` of the whole file inline in MarkComplete once a second. Now it runs on a background goroutine.
3. MarkComplete stat-ed the `.part` file before checking completeness, and on Windows a stat waits for a sync of the same file (up to **230 ms** measured; the outliers in `findings/11`). Completeness is now checked first; a missing part file is cached so rechecks stay O(N).

A sync still slows concurrent writes at the OS level (≥ 50 ms writes: ~105 with syncs vs ~60 without, out of 65,536). Fewer syncs would need a durability decision; left as is.

## Real play: House of the Dragon S02E01, cold, same driver and settings as `findings/11`

| | Step 2 cold | Step 3 cold |
|---|---|---|
| Metadata | 6.9 s | 4.7 s |
| First piece verified | 22.0 s | 20.7 s |
| First frame | 24.0 s | 23.7 s |
| **Stalls after first frame (mpv)** | **2, 13.1 s** | **0** |
| Useful MiB from uTP peers | 184 (10 %) | **446 (20 %)** |
| Best uTP peer | 50 MiB | 178 MiB |
| uTP reorder peaks | 1023, 982, 655 | 705, 449, 361 |
| `ReorderTooFar` (drops ahead of the buffer) | not counted | 1 packet in total |
| Chunk writes: total time / wall | 178 s / 120 s (1.48) | 52 s / 120 s (0.44) |
| Chunk write mean | 1.43 ms | 0.37 ms |
| MarkComplete max | 366 ms | 0.5 ms |
| Client lock holds ≥ 50 ms | 0 | 0 |

The mpv cache still had 57–67 s of read-ahead at the end, and the server rate ran 17–27 MiB/s from 30 s on. Reader waits still occur on the server (one of 9 s at piece 765 at 28.6 s), but mpv's cache covered them. That is a single run on a live swarm, not proof by itself, but every per-peer counter moved the way the lab predicts.

**B6:** one packet dropped ahead of the buffer in 120 s. The window cap or bigger reorder buffer is not needed; not built.

**C1 (prefer TCP):** the top TCP peers delivered 300–411 MiB against 178 MiB for the best uTP peer, but the TCP peers' RTT is not measured, so it is not a same-peer comparison. No evidence either way; C1 stays unbuilt.

## New: the first write to the tail zero-fills the file, and that is the start-up "limit stuck at 2"

The step 3 play kept every slow write (102 of them; step 2's 1000-slot ring had already overwritten the first minute). The first write to piece 1395, the last piece, about 10 GB into a fresh file, took **7.55 s** (starting at 5.6 s). The head writes to piece 760, the piece the player is waiting for, started at 6.7, 8.8 and 9.4 s and took 8.2, 6.0 and 5.5 s. Writes stayed slow until about 15 s.

Cause: anacrolix creates cache files as ordinary (non-sparse) NTFS files. The first write past the valid data length makes Windows zero-fill everything before it, and other writes to the file wait. A scratch test on this machine: on a new plain file, the first write at 2 GiB takes **836 ms**; on a sparse file (`FSCTL_SET_SPARSE`), 0 ms. At 10 GB under load that is the 7.5 s seen. It also writes ~10 GB of zeros to the SSD for every file the player opens.

Every chunk write blocks its peer's read loop. Peer samples from 5 to 14 s: every peer at `maxRequests` 2, having delivered 0.0 MiB. From 15 s, when the writes return, all limits double together (4 → 11 → 23 → 46 → 72 → 111 → 146 → 257). Step 2's cold play shows the same 7–16 s window. So the "request limit sits at 2 for ~10 s" in `findings/11` is mostly this stall. The request-health loop only keeps the limit low while peers can deliver nothing. **About 9 s of the ~15 s first-piece time is the zero-fill.**

`findings/11` called the zero-fill question answered ("no multi-second zero-fill stall"). That was wrong: the ring had evicted the evidence, and the tail's MarkComplete is not where the cost lands. Plan 03 A1 (sparse files on Windows) has its evidence now; it was skipped in this step on the old reading.

## What changes next
- **A1, sparse cache files on Windows**, is the largest start-up item measured so far: ~9 s of a 21–24 s first frame, plus 10 GB of avoidable SSD writes per file. It belongs in the anacrolix storage (`storage/file-io-common.go` / `sys-lame.go`, as plan 03 describes).
- Plan 04's R0/R1 (short-window rate, deep first queue) should be re-measured after A1. The 10 s at limit 2 may mostly disappear with it.
- The slow-write ring should keep the first N entries too, or a per-second histogram, so the start of a play is never evicted.

## 3b — Sparse cache files on Windows (2026-11-17)

Commits:
- **anacrolix `d6a9b4cc`:** `openFileExtra` marks files sparse with `FSCTL_SET_SPARSE`; a no-op off Windows; errors ignored.
- **Rivulet `5a8c4bf`:** the cache budget charges allocated bytes on Windows (`GetCompressedFileSize`). Its sparse-file tests used to skip on Windows; they now run.

| Check | Before | After |
|---|---|---|
| First 16 KiB write at the end of a new 4 GiB file (`TestFirstTailWriteLatency`) | 1.63 s (plain file) | 0.5 ms (file storage) |
| Allocated after one write at 1 GiB (`TestOpenFileExtraCreatesSparseFiles`) | ≥ 1 GiB | < 1 MiB |
| Real play: slow writes at start | 7.5 s (piece 1395), 8.2 / 6.0 / 5.5 s (piece 760) | **none**; slowest write in the whole play 17 ms |
| Real play: request limit after the first chunk | 2 for ~10 s on every peer | the first peer went 2 → 10 → 16 within 6 s of its first chunk |

**The play** (`data/play-20261117-191508-hotd-step3b-cold`) confirms the storage effect, but its headline numbers can't be compared:
- The swarm gave far less this time: metadata at 18.4 s (4.7 s in the step 3 play), and for the first 30 s a single μTorrent peer delivering ~0.1 MiB/s.
- Only 377 MiB of useful data in 120 s, against 2192 MiB.
- First frame 85.9 s and 3 stalls (13.3 s) follow from that supply, not from storage: there were no slow writes, and every limit rose as soon as data arrived.

The rule against repeat runs applies, so the play wasn't repeated. The next cold play (step 4's baseline) will show the start-up gain on a normal swarm.

**Consequence for plan 04:** the "stuck at 2 for 10 s" in `findings/11` was the zero-fill stall. With storage fixed, a delivering peer leaves 2 at once and grows by doubling (2 → 10 → 16 → 128 → 331 over ~25 s here). R0/R1 (a deep first queue instead of doubling from 2) still matter for how fast that growth goes, but the ~10 s floor case is gone. Plan 04 should be re-baselined with one normal cold play first.
