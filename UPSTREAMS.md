# Upstreams

Every folder below holds an original project with its full history, plus our changes. `mise run sync` (`scripts/sync-upstreams.ps1`) merges each one's latest code; `mise run check-upstreams` reports what isn't merged yet, and the Release workflow refuses to release until everything is.

| Folder | Original project | Branch | Licence | Notes |
|---|---|---|---|---|
| `web/` | [Stremio/stremio-web](https://github.com/Stremio/stremio-web) | `development` | GPL-2.0 | Also kept on the latest [`@stremio/stremio-core-web`](https://www.npmjs.com/package/@stremio/stremio-core-web). |
| `server/` | [rivulet-kodi/rivulet-engine](https://github.com/rivulet-kodi/rivulet-engine) | `main` | MIT | An open-source replacement for Stremio's closed `server.js`. |
| `deps/torrent/` | [anacrolix/torrent](https://github.com/anacrolix/torrent) | `master` | MPL-2.0 | Used by `server/` through a `replace` in its `go.mod`. |
| `deps/go-utp/` | [anacrolix/go-utp](https://github.com/anacrolix/go-utp) | `master` | MPL-2.0 | Used by `deps/torrent/` and `server/` through `replace`s. |
| `shell/` | [Stremio/stremio-shell-ng](https://github.com/Stremio/stremio-shell-ng) | `main` | none stated | Filtered: binaries (libmpv, ffmpeg, `server.js` and its runtime) are left out; `scripts/build.ps1` downloads the media binaries we ship, pinned in `scripts/binaries.json`. The filter is deterministic, so later syncs merge normally. Files keep upstream's line endings (some are CRLF). |

Upstream's own `.github/` folders (`web/.github`, `server/.github`) and `server/.agents` are kept so syncs merge cleanly, but they're dormant here: GitHub runs only the root `.github`.

## Syncing by hand
A folder is merged with a subtree merge, which keeps the upstream history and lands its tree under the folder:

```
git fetch up-web development
git merge -s subtree -Xsubtree=web up-web/development
```

The shell goes through the filter first; `sync-upstreams.ps1` does it (it needs `pip install git-filter-repo`).

## Sending a change back upstream
`git subtree split --prefix=server -b server-only` gives a branch with only that folder's history, at the original project's paths. Push it to a fork of the original project and open a pull request from there.
