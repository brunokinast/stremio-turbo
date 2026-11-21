# Stremio Turbo

A souped-up Stremio for Windows: Stremio's own app and web UI, with an open-source streaming server and changes aimed at smooth 4K HDR playback from torrents.

**What's different**
- **Streaming server:** [Rivulet Engine](https://github.com/rivulet-kodi/rivulet-engine) instead of Stremio's closed `server.js`, tuned for fast starts and steady 4K playback. It also starts sooner and downloads whole episodes on disk.
- **Smart play:** the episode page measures the sources in the background and plays the best one that's fast enough, in the quality you last chose.
- **Next episode prepared** while you watch, and a seamless switch to another source if the current one slows down mid-episode.
- **Downloads:** a single episode, or a whole season in a chosen quality, managed on a Downloads screen.
- **Always current:** a release can only be built once every original project's latest code is merged (see [UPSTREAMS.md](UPSTREAMS.md)), and the app updates itself from this repository's releases.

It installs alongside the official Stremio and uses the same account.

## Install
Download `StremioTurbo-<version>-x64.exe` from the [latest release](https://github.com/brunokinast/stremio-turbo/releases/latest) and run it. The installer isn't code-signed yet, so Windows may show "Windows protected your PC": choose **More info → Run anyway**. Updates are checked by the app and verified with this project's signing key.

## Layout
| Folder | What |
|---|---|
| `web/` | The web UI (fork of Stremio's stremio-web) |
| `server/` | The streaming server (fork of Rivulet Engine) |
| `shell/` | The Windows app (fork of Stremio's stremio-shell-ng) |
| `deps/` | Our copies of the torrent and uTP libraries the server uses |
| `research/` | Measurements and plans behind the changes |
| `scripts/` | Build, upstream sync, release |

## Build
Needs Node.js 24 (corepack), Go, Rust (MSVC), Inno Setup 6 and Python with `git-filter-repo`.

```
pwsh scripts/build.ps1          # installer and update files in out/
pwsh scripts/dev.ps1            # run against the web dev server
pwsh scripts/sync-upstreams.ps1 # merge every original project's latest code
pwsh scripts/release.ps1 -Version 1.1.0 -Push
```

## Licences
Each folder keeps its project's licence: `web/` GPL-2.0, `server/` MIT, `deps/` MPL-2.0. `shell/` comes from Stremio's stremio-shell-ng, which states no licence. See [UPSTREAMS.md](UPSTREAMS.md). Stremio is a trademark of its owners; this project is not affiliated with Stremio.
