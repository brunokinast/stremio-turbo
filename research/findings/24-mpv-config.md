# 24. mpv config file (step 7A): done after updating the shell to 5.0.28

2026-11-18. The first attempt (shell 5.0.27) failed; done after the user chose to update the shell (5.0.28).

## What happened
- **Created** `%APPDATA%\Stremio\mpv.conf` (cache-pause-wait=10, 1 GiB forward / 256 MiB back
  buffer, network-timeout=180; no cache-pause-initial, because 6f handles starts). Also created
  `mpv-config.txt` holding its path. Neither file existed before.
- **The shell didn't read it.** After a restart, Settings → Device had **no "Custom mpv config"
  row**. The fork shows that row only when the shell reports `mpvConfig` among its transport
  properties (`src/common/Platform/shell/useShell.ts:125`).
- **The installed shell has no such support.** `stremio-shell-ng.exe` 5.0.27 (installed) contains
  no `mpv-config`, `mpv.conf` or `pick-mpv-config` strings; the only `%APPDATA%\Stremio` file it
  names is `window-state.json`.
- **Plan 07's premise was wrong for this build.** Plan 07 / `findings/03` described config loading
  from the shell's source (`mpv_config.rs`), apparently a newer revision than 5.0.27. The app
  shows "A new version of Stremio is available", which may be that.
- **Reverted:** both files were removed again, and `%APPDATA%\Stremio` is as before.

## Options
1. **Web-side rebuffer hold:** reuse 6f's mechanism, using only allowed properties. When mpv
   pauses for cache mid-play, the web UI keeps playback paused until N s is buffered (the same
   effect as `cache-pause-wait`), with the same visible line. Recommended: no shell change.
2. **Update the shell** to the offered new version, if it supports the config; then 7A as
   planned. Installs software, so it needs the user.
3. **Fork stremio-shell-ng** to add the cache options. The largest option.

## Update and result (user chose option 2)
- **Shell 5.0.28** (16 Nov) adds "mpv config file support" (stremio-shell-ng PR #115).
  - `mpv_config.rs` reads `%APPDATA%\Stremio\mpv-config.txt`: a plain path, the same format as
    above.
  - `player.rs` loads it after the shell's own video options. Only `keep-open` and `loop-file`
    are forced after it. The shell sets no cache options, so ours stand.
- **Backup:** `%LOCALAPPDATA%\Programs\Stremio-5.0.27-backup` (612 MB) before updating.
- **Installed** through the app's own "Install now" (StremioSetup-v5.0.28_x64).
- **Side effects of the installer:**
  - It relaunched plain Stremio, with Stremio's `server.js`. I closed it.
  - It rewrote the Start menu `Stremio.lnk` (standard target, the plain shell). The fork never
    managed a shortcut; it starts with `scripts/start-stremio.ps1`.
- **Re-created** `mpv.conf` and `mpv-config.txt`. Settings → Device now shows "Custom mpv config:
  …\Stremio\mpv.conf". The launcher works unchanged on 5.0.28 (Rivulet plus `--development
  --webui-url`).

## Verification (real app, HotD S02E04 4K, muted, stopped after; mpv events read from the shell IPC)
| Check | Result |
|---|---|
| Config loaded by mpv | Forward buffer reached **159 s ahead** (about 450 MB) before the seek. mpv's default 150 MiB cap would stop near 52 s, so `demuxer-max-bytes=1GiB` is in force, and the file is loaded. |
| Start unchanged | Playing from 6.1 s, no wait for 10 s (no `cache-pause-initial`) |
| Seek to 60% (uncached) | Seek → playing **3.2 s**, resuming with about 2 s buffered. `cache-pause-wait` doesn't apply to seeks, so seeks aren't slowed. 5c's 1.96 s was under other conditions. |
| `cache-pause-wait=10` after an underrun | Not observed: the source was fast and never underran. It's in the same file mpv demonstrably loaded. |

**Option 1** (web-side rebuffer hold) **isn't needed** now.
