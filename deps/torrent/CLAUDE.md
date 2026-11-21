# Fork directives

Personal fork of [anacrolix/torrent](https://github.com/anacrolix/torrent), the BitTorrent library inside our Rivulet fork (`server/`, which points its `go.mod` here with a `replace`). It is the `deps/torrent/` folder of Stremio Turbo (see the root `CLAUDE.md`). We own this code: fix things here properly when the cause is in the library, and merge upstream ourselves when we choose to.

- Commit to Stremio Turbo's `main` and push to `origin` (brunokinast/stremio-turbo). Never push to anacrolix or force-push.
- Upstream updates (anacrolix `master`): `scripts/sync-upstreams.ps1` at the repository root.
- Explore problems end to end before fixing: measure, trace across Rivulet and the swarm, find out why a limit exists before changing it (`git log -S`), and compare with libtorrent (qBittorrent's engine) when behaviour differs. See stremio-web's and Rivulet's CLAUDE.md principles.
- Keep changes upstream-friendly: configurable through `ClientConfig` with sensible defaults, documented, tested in the package's existing style (`qt`).
- Before committing, from the repo root with `GOWORK=off` (the go.work lists a possum submodule we don't check out): `go build ./...`, `go vet .`, and the root package tests. Plain `go test` is fine on the owner's machine: `GOFLAGS=-exec=...\streaming-research\lab\tools\goexec.exe` (set with `go env -w`) runs every test binary from a fixed, firewall-allowed slot and deletes it afterwards. Don't build test binaries elsewhere and run them directly (`go test -c -o …` then executing it): that path has no firewall rule and raises a prompt.
- Upstream already has `gofmt` and `go vet` findings in files we don't touch; keep our files clean, and don't reformat theirs.
- Repo uses LF line endings (`core.autocrlf=false`).
- Our changes so far: `request-health.go` (per-peer request queue sized by rate, snubbing, idle-peer retry; `ClientConfig.RequestQueueTime` and `PieceTimeout`).
