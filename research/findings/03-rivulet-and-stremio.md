# Rivulet and Stremio: what happens between the click and the frame

Covers the Rivulet fork (`internal/engine/engine.go`, `internal/api/api.go`), `@stremio/stremio-video` 0.0.103 (in stremio-web's node_modules) and `Stremio/stremio-shell-ng` (public source, `main`, read 2026-11-16). Less deep than findings/01–02: the reading budget went to the engines.

## The pipeline for a torrent stream in the Windows shell
1. **stremio-web** picks a stream (our smart play: quality rows → race → winner) and hands it to `stremio-video`.
2. **`withStreamingServer`** (`withStreamingServer.js`, `convertStream.js`, `createTorrent.js`):
   - with a known `fileIdx` and no tracker list (Torrentio's case) it does **not** call `/create`; the media URL is simply `http://127.0.0.1:11470/{infoHash}/{fileIdx}`;
   - it then **probes** the URL through the server (`/hlsv2/probe`, ffprobe run by Rivulet) to decide direct play vs HLS transcode. The probe reads the file's head through the same torrent reader, so it too waits for the first piece.
3. **`ShellVideo`** (`ShellVideo.js:506–565`) tells the shell's embedded **mpv** to `loadfile <url> replace -1 start=+<seconds>` (resume position), then un-pauses.
4. **mpv** (libmpv in `stremio-shell-ng`) reads the URL with plain HTTP range requests. For Matroska it reads the header, and a start offset or seek needs the Cues, which sit at the **end** of the file → a range request to the tail, then one to the target position.
5. **Rivulet** (`handleStream`, `api.go:~900`) waits for metadata (`CreateMetadataWait`), opens `engine.NewReader(idx)` and copies bytes to the response.

## Rivulet's reader policy (`engine.go`)
- `NewReader` (`:1432`): marks the file for download (`ensureDownloading` → `File.Download()`, Normal priority for the whole file on disk storage), primes the **head 4 MiB and tail 8 MiB at Now** (`primeBoundary`, `:2420`; each rounds up to one whole piece, so 2 × 16 MiB on 4K packs), starts a **tail pre-read** (`warmMoov`, `:1492`, a second reader parked on the tail), and prefetches the next file's boundaries at Normal (`prefetchNext`).
- Readahead (`readaheadFor`, `:1671`): 2 s of the measured download speed, clamped to **8–64 MiB**, re-evaluated every 3 s. At 2 MB/s that is 8 MiB: half a piece.
- Reads are **not responsive** (findings/02 §4).
- First read is bounded by `startupReadTimeout = 90 s`; then the HTTP response ends with an error (the "content length mismatch" seen in the browser race).
- When the **last reader of a file closes**, the file's priority drops to None and its boundary pieces are released (`pinnedReader.Close`, `:1823`): downloading stops when the player's connection closes. The torrent stays open until the janitor's idle timeout (300 s).
- Tracker list: top 25 by a liveness ranking (`trackers.go`); peers from `/scrape` are kept as hints for 10 min (`peerhints.go`).
- Client budget: 50 established / 25 half-open per torrent (`peerBudget`, `:2586`), scaled by `RIVULET_PEERS_PER_TORRENT`. The client-wide dial limits (10/s, 100 half-open) are anacrolix defaults Rivulet does not set.
- A soft download speed limit of 4 MB/s appears in the server settings (`btDownloadSpeedSoftLimit`); not traced. **Check whether it throttles**: 4K needs ~2 MB/s, so a 4 MB/s ceiling halves how fast a buffer can build.

## The player's own buffer (mpv)
- The shell creates mpv with a fixed option set (`player.rs:410–460`): `hwdec=auto`, `vo=gpu-next,gpu`, HDR/tone-mapping options, `stream-lavf-o=reconnect=1,…`. **No cache options**, so mpv's defaults apply: `demuxer-max-bytes=150MiB` forward (≈ 75 s of this 4K stream), `demuxer-max-back-bytes=50MiB`, `cache-pause=yes`, **`cache-pause-wait=1`** (after an underrun it resumes with 1 s buffered), `cache-pause-initial=no`.
- With supply ≈ bitrate, `cache-pause-wait=1` produces the worst experience: play 1 s, stall, play 1 s.
- The web UI cannot change this: the shell only accepts an allowlist of properties (`communication.rs:165–227`: pause, time-pos, volume, speed, aid/sid, sub-*, hwdec, vo, …) and `loadfile` only with a validated `start=+N` (`is_safe_start_option`). Cache options need either **a fork of stremio-shell-ng** (Rust; adding 3–4 init options is a small change, but it is another fork to build and keep) or a buffer policy enforced from the web UI with `pause` (allowed) driven by server-side buffer data.
- Observable from the UI: `paused-for-cache`, `cache-buffering-state`, `demuxer-cache-time` (already wired as `buffering` / `buffered`).

## Gaps this exposes (inputs to the gap table)
1. Tail before head, whole-piece verified reads, sub-piece readahead (all measured in findings/02 and 05).
2. No notion of **bitrate or deadline** anywhere: readahead is sized from download speed, not from what playback needs.
3. No **download intent** independent of an open HTTP reader: nothing can say "keep fetching this episode" or "fetch the next one".
4. Probe and player both pay the first-piece cost; a preloaded torrent with the head already on disk makes both instant.
5. The buffering the user sees is governed by mpv defaults nobody chose.
