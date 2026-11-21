# Plan 07 — Player buffer (step 7; Part A can be done any time with the user's OK, it is independent)

Goal: a short dip in supply does not become a visible pause, and a pause, when it happens, ends with enough buffer not to repeat at once.

## Part A — mpv config file (no code)

> **Done 2026-11-18 (`findings/24`):** shell 5.0.27 had no mpv config support, so the shell was updated to 5.0.28 (adds it; backup of 5.0.27 kept). `%APPDATA%\Stremio\mpv.conf`: cache-pause-wait=10, 1 GiB / 256 MiB buffers, network-timeout=180; no cache-pause-initial (6f handles starts). Verified loaded (forward buffer 159 s ahead vs about 52 s default); seeks unaffected.
Facts: the installed shell (5.0.27) loads a user mpv config: `%APPDATA%\Stremio\mpv-config.txt` holds the path of the config file (`ref\stremio-shell-ng\src\stremio_app\mpv_config.rs`, loaded at `stremio_player\player.rs:462`). The web fork already has a picker for it (Settings → Device, `src\routes\Settings\Device\useMpvConfig.ts`). No such file exists on this machine, so mpv runs on defaults: it resumes after about one second of buffer.

1. Read `player.rs` around the load to see which options the shell sets **after** loading the file (those would override ours) and note them.
2. With the user's OK, create `%APPDATA%\Stremio\mpv.conf` and point `mpv-config.txt` at it (or let the user pick it in Settings). Starting values:

```
cache=yes
cache-pause-initial=yes
cache-pause-wait=10
demuxer-max-bytes=1GiB
demuxer-max-back-bytes=256MiB
network-timeout=180
```

   Why these: a 16 MiB piece is about 8 s of this stream, and until plan 05 Part B lands data arrives a piece at a time, so resuming with less than one piece of buffer pauses again at once; `cache-pause-wait=10` covers one piece. 1 GiB forward buffer is about 8 minutes at ~2 MB/s and lets mpv keep reading ahead while the swarm is good. The long network timeout stops mpv from dropping the stream during a thin patch (each drop closes the reader, and closing the last reader currently sets the file to priority None, `engine.go:1846–1868`).
   Measured offline with the shell's own libmpv (`findings/10` §A): supply at 0.7 of the bitrate for 60 s gave **7 pauses** on defaults and **2** with `cache-pause-initial=yes`, `cache-pause-wait=10`. Total waiting is the same (`duration × (bitrate/supply − 1)`): the config groups pauses, it does not remove them. Only supply (plans 03–06) or a disk buffer (plan 08) removes them.
3. One play with plan 02's capture. Report the count and length of cache pauses (`paused-for-cache` is observable through the shell, `communication.rs:169–174`) against a play without the file.

## Part B — Reader-close grace in Rivulet (moved to `05-startup-rules.md` A5; skip here if done)
`pinnedReader.Close` drops the file to priority None when the last reader closes, so every mpv reconnect or range change flaps the priorities and cancels work. Keep the file's priorities for a grace period (suggest 30 s) after the last reader closes; cancel the timer when a new reader opens. This was item 11 in `findings/06` §8.

## Part C — Buffer behaviour in the web UI (design with the user first)
The shell lets the web UI observe `paused-for-cache`, `cache-buffering-state` and `demuxer-cache-time`, but the runtime property allowlist does not include the cache options (`communication.rs:158–226`), so adapting them at run time needs either a shell fork or a restart of the stream. Options to show the user, with previews as their rules ask:
1. Static config only (Part A).
2. A "buffer first" start: the web UI keeps playback paused until `demuxer-cache-time` reaches a target computed from the measured download rate versus the stream bitrate (if rate ≥ bitrate, a few seconds; if below, enough to finish without a second pause, or an honest "this source cannot keep up" with a switch offer). Uses only allowed properties.
3. Shell fork adding the cache options to the allowlist.

Recommendation: 1 now, 2 after plan 05, 3 only if 2 proves insufficient.

> **Option 2 done early for smart-play starts** (step 6f, `findings/21`): hold on the first frame until 4 s is buffered when the source is under 2x its bitrate. Part A (mid-play pauses) is still open and needs the user's OK.
