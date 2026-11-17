<!--
SPDX-FileCopyrightText: 2026 The rivulet-engine Authors

SPDX-License-Identifier: MIT
-->

# Fork directives

Personal fork of [rivulet-kodi/rivulet-engine](https://github.com/rivulet-kodi/rivulet-engine), used as the streaming server for the owner's Stremio setup (web UI fork: `C:\Users\bruno\Documents\git\stremio-web`, launched by its `scripts/start-stremio.ps1`). We own this code: change it properly where a fix belongs, and merge upstream ourselves when we choose to.

- Follow `AGENTS.md` for architecture, conventions and testing; it applies in full.
- Explore problems end to end before fixing: measure, trace the flow across components (addon, client, server, swarm), question existing limits by finding out why they exist, and propose flow-level improvements (reuse, caching, doing work early, better defaults) unasked. See stremio-web's CLAUDE.md principles.
- Commit straight to `main` and push to `origin` (brunokinast/rivulet-engine). Never push to rivulet-kodi or force-push.
- Before committing: `gofmt -s -l` clean, `go vet` and the tests pass. On this machine run the tests with `C:\Users\bruno\Documents\git\streaming-research\lab\tools\rivulet-test.sh` (the same as `go test -p 1 ./...`, but from fixed-path binaries the firewall already allows), never plain `go test`: its random temp paths raise a firewall prompt every run, and the owner wants none. A new test package needs `lab\tools\allow-testbins.ps1` run elevated once (on Windows, `TestArchiveResolveLocalPath_SymlinkEscape` needs symlink privileges and `cmd/librivulet-engine` needs cgo; both fail locally regardless of changes).
- Update upstream with `git fetch upstream && git merge upstream/main` (or a release tag).
- Build: `CGO_ENABLED=0 go build -trimpath -o rivulet-engine.exe ./cmd/rivulet-engine`, then copy it to `C:\Users\bruno\Tools\rivulet\rivulet-engine.exe` with Stremio closed.
- Repo uses LF line endings (`core.autocrlf=false`).
- Test streaming changes against **4K HDR** sources (what the owner watches), muted, and stop playback when done; see the Testing section of stremio-web's CLAUDE.md.
