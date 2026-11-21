# 08 — Probe correction and the piece-completion path

Fourth pass, 2026-11-16, read-only. Two areas: what the web client asks the server at start (corrects `06` §2), and what happens between "last chunk arrived" and "player can read the piece".

`V` = `stremio-web\node_modules\.pnpm\@stremio+stremio-video@0.0.103\node_modules\@stremio\stremio-video\src`, `A` = `anacrolix-torrent`.

## 1. Correction: in the shell, `/hlsv2/probe` does not run before mpv loads

`06` §2 said the probe blocks the mpv load. That is true for the browser player only.

- `getPlayability` probes only when `supportsTranscoding()` is true (`V\withStreamingServer\withStreamingServer.js:81–104`).
- `supportsTranscoding()` is false when `window.qt` exists (`V\supportsTranscoding.js:4`).
- stremio-shell-ng defines `window.qt` in every page (`ref\stremio-shell-ng\src\stremio_app\stremio_wevbiew\wevbiew.rs:257`).
- So in the shell it goes straight to `ShellVideo.canPlayStream`, which is always true (`V\ShellVideo\ShellVideo.js:663`), and mpv gets the direct URL at once.

What still touches the server around start, in the shell:
- After the player reports loaded: `fetchVideoParams` (`withStreamingServer.js:415–417`) → `/opensubHash`, unless the add-on already gave `behaviorHints.videoHash` and `videoSize` (`fetchVideoParams.js:3–8`), and `/subtitleSignature`.
- mpv's own reads (head, then tail or position; still to be captured by the access log).
- Rivulet's own priming of head and tail (`warmMoov`, `primeBoundary`).

Consequences:
- `06` §8 item 3 (cheaper or earlier probe) loses most of its value for our setup. The 30 s probe timeout and probe sizes only matter if `/subtitleSignature` runs ffprobe; check that before spending time there.
- The tail demand at a fresh start is now: `/opensubHash` (last 64 KiB), possibly `/subtitleSignature`, possibly mpv, plus Rivulet's tail priming. With 16 MiB pieces and whole-piece reads, a 64 KiB tail read costs one full piece: about 8 s of the one fast peer at 2 MB/s, spent while the head is still loading.
- New improvement: give server-side helper reads (`/opensubHash`, `/subtitleSignature`) a background class. They should not raise piece priority to Now while a player reader is starving; the hash is only used to match subtitles and can arrive seconds after the first frame. This fits the "download intent" idea (`06` §8 item 11) and removes the tail competition without touching the web client.

## 2. From last chunk to readable piece

Facts **[code]**:
- Chunk writes are synchronous in the peer read loop, batched per run of buffered Piece messages (`A\peerconn.go:1000–1023`). A storage stall therefore stalls the transport read, which is the trigger in `07` §2.
- The client lock is released around `MarkComplete` (`A\torrent.go:2810–2816`), so a slow fsync does not hold the whole client.
- It does hold the storage: `classicFileIo.flush` keeps the single storage mutex across `f.Sync()` (`A\storage\file-io-classic.go:51–59`), and every write open and read open takes the same mutex (`:72`, `:85`). During an fsync, chunk writes on all connections, hashing reads and the player's reads all wait.
- The flush covers the whole file (`A\storage\file-torrent-checkpoint.go:107`), at most once per second or per 128 MiB of completed pieces (`:12–15`).
- A piece is marked complete in memory only after `MarkComplete` returns (`A\torrent.go:2812–2822`). So the completion-store write and any file flush sit between "hash passed" and "reader unblocked".
- Completion is persisted (`rivulet-engine\internal\engine\filestorage.go:33`), so resuming a partly cached episode does not re-hash it. No issue there.

Improvements:
- Unblock readers when the hash passes; persist completion and flush afterwards. Readers need correctness, which the hash gives; durability only matters for the next start.
- Run `f.Sync()` outside the storage mutex (hold a reference to the handle instead). One slow flush should not stop every connection.
- Time two calls on the real disk, on a fresh 6.7 GB file: the first tail `WriteAt` and the first `Sync` after it. One of them pays for the NTFS zero-fill (`06` §3), and it does so under a lock that stops all chunk writes. Whatever that stall is, `07` §1 says each libtorrent uTP peer then needs about 20 s to recover from it, so its cost is larger than its duration.

## 3. Where this leaves the reading
The remaining open questions are empirical (access log, uTP counters, the two timings above). Further code reading has low yield until those exist.
