# Licences

Stremio Turbo combines several projects, and each folder keeps its own project's licence. Changes made inside a folder are under that folder's licence.

| Folder | Project | Licence |
|---|---|---|
| `web/` | [Stremio/stremio-web](https://github.com/Stremio/stremio-web) | GPL-2.0 ([web/LICENSE.md](web/LICENSE.md)) |
| `server/` | [rivulet-kodi/rivulet-engine](https://github.com/rivulet-kodi/rivulet-engine) | MIT ([server/LICENSE](server/LICENSE)) |
| `deps/torrent/` | [anacrolix/torrent](https://github.com/anacrolix/torrent) | MPL-2.0 ([deps/torrent/LICENSE](deps/torrent/LICENSE)) |
| `deps/go-utp/` | [anacrolix/go-utp](https://github.com/anacrolix/go-utp) | MPL-2.0 ([deps/go-utp/LICENSE](deps/go-utp/LICENSE)) |
| `shell/` | [Stremio/stremio-shell-ng](https://github.com/Stremio/stremio-shell-ng) | none stated by the original project |

Everything else in this repository (the root files, `scripts/`, `docs/`, `research/`) is Copyright (C) 2026 Bruno Kinast and licensed under the GNU General Public License, version 2: see [LICENSE](LICENSE).

The installer also ships libmpv and ffmpeg/ffprobe, downloaded at build time under their own licences; `scripts/binaries.json` pins them.
