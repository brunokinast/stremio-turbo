<!--
SPDX-FileCopyrightText: 2026 The rivulet-engine Authors

SPDX-License-Identifier: MIT
-->

# server/: Rivulet fork

The streaming server of Stremio Turbo, forked from [rivulet-kodi/rivulet-engine](https://github.com/rivulet-kodi/rivulet-engine) and bundled with the app by `shell/`. The root `CLAUDE.md` holds the principles, workflow and testing rules; these are the server-specific ones.

- Follow `AGENTS.md` (upstream's) for architecture, conventions and testing; it applies in full.
- Checks before committing: `mise run test:go` from the root (`gofmt -s -l` clean, `go vet`, `go test -p 1 ./...`). On Windows, `TestArchiveResolveLocalPath_SymlinkEscape` needs symlink privileges and `cmd/librivulet-engine` needs cgo; both fail locally regardless of changes.
- `go.mod` uses `deps/torrent` and `deps/go-utp` through `replace`s.
- Build: `mise run build` from the root (installer in `out/`); install it with the app closed.
