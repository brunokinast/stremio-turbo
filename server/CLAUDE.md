<!--
SPDX-FileCopyrightText: 2026 The rivulet-engine Authors

SPDX-License-Identifier: MIT
-->

# Fork directives

Personal fork of [rivulet-kodi/rivulet-engine](https://github.com/rivulet-kodi/rivulet-engine), the `server/` folder of Stremio Turbo (see the root `CLAUDE.md`), bundled with the app by `shell/`. We own this code: change it properly where a fix belongs, and merge upstream ourselves when we choose to.

- Follow `AGENTS.md` for architecture, conventions and testing; it applies in full.
- Explore problems end to end before fixing: measure, trace the flow across components (addon, client, server, swarm), question existing limits by finding out why they exist, and propose flow-level improvements (reuse, caching, doing work early, better defaults) unasked. See stremio-web's CLAUDE.md principles.
- Commit straight to `main` and push to `origin` (brunokinast/stremio-turbo). Never push to rivulet-kodi or force-push.
- Before committing: `gofmt -s -l` clean, `go vet` and the tests pass. `go test -p 1 ./...` is safe on the owner's machine: `GOFLAGS=-exec=...\streaming-research\lab\tools\goexec.exe` runs every test binary from a fixed, firewall-allowed slot and deletes it afterwards. Never run a test or tool binary from any other path (e.g. `go test -c -o x` then running `x`): it raises a firewall prompt (on Windows, `TestArchiveResolveLocalPath_SymlinkEscape` needs symlink privileges and `cmd/librivulet-engine` needs cgo; both fail locally regardless of changes).
- Upstream updates: `scripts/sync-upstreams.ps1` at the repository root.
- Build: `scripts/build.ps1` at the repository root (installer in `out/`), installed with the app closed. `go.mod` uses `deps/torrent` and `deps/go-utp` through `replace`s.
- Repo uses LF line endings (`core.autocrlf=false`).
- Test streaming changes against **4K HDR** sources (what the owner watches), muted, and stop playback when done; see the Testing section of stremio-web's CLAUDE.md.
