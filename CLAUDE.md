<!--
SPDX-FileCopyrightText: 2026 The rivulet-engine Authors

SPDX-License-Identifier: MIT
-->

# Fork directives

Personal fork of [rivulet-kodi/rivulet-engine](https://github.com/rivulet-kodi/rivulet-engine), used as the streaming server for the owner's Stremio setup (web UI fork: `C:\Users\bruno\Documents\git\stremio-web`, launched by its `scripts/start-stremio.ps1`). We own this code: change it properly where a fix belongs, and merge upstream ourselves when we choose to.

- Follow `AGENTS.md` for architecture, conventions and testing; it applies in full.
- Commit straight to `main` and push to `origin` (brunokinast/rivulet-engine). Never push to rivulet-kodi or force-push.
- Before committing: `gofmt -s -l` clean, `go vet` and `go test -p 1 ./...` pass (on Windows, `TestArchiveResolveLocalPath_SymlinkEscape` needs symlink privileges and `cmd/librivulet-engine` needs cgo; both fail locally regardless of changes).
- Update upstream with `git fetch upstream && git merge upstream/main` (or a release tag).
- Build: `CGO_ENABLED=0 go build -trimpath -o rivulet-engine.exe ./cmd/rivulet-engine`, then copy it to `C:\Users\bruno\Tools\rivulet\rivulet-engine.exe` with Stremio closed.
- Repo uses LF line endings (`core.autocrlf=false`).
- Test streaming changes against **4K HDR** sources (what the owner watches), muted, and stop playback when done; see the Testing section of stremio-web's CLAUDE.md.
