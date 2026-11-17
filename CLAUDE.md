# Fork directives

Personal fork of [anacrolix/torrent](https://github.com/anacrolix/torrent), the BitTorrent library inside our Rivulet fork (`C:\Users\bruno\Documents\git\rivulet-engine`, which points its `go.mod` here with a `replace`). We own this code: fix things here properly when the cause is in the library, and merge upstream ourselves when we choose to.

- Our branch is `main` (upstream's is `master`). Commit to `main` and push to `origin` (brunokinast/torrent). Never push to anacrolix or force-push.
- Update from upstream with `git fetch upstream && git merge upstream/master`.
- Explore problems end to end before fixing: measure, trace across Rivulet and the swarm, find out why a limit exists before changing it (`git log -S`), and compare with libtorrent (qBittorrent's engine) when behaviour differs. See stremio-web's and Rivulet's CLAUDE.md principles.
- Keep changes upstream-friendly: configurable through `ClientConfig` with sensible defaults, documented, tested in the package's existing style (`qt`).
- Before committing, from the repo root with `GOWORK=off` (the go.work lists a possum submodule we don't check out): `go build ./...`, `go vet .`, and the root package tests. On Windows, build test binaries to a fixed path (`go test -c -o C:\Users\bruno\Documents\git\streaming-research\lab\testbin\anacrolix\<name>.test.exe .`) and run them from the package directory, never plain `go test`. That folder has firewall allow rules: the owner wants no firewall prompts. A new binary name needs `streaming-research\lab\tools\allow-testbins.ps1` run elevated before its first run.
- Upstream already has `gofmt` and `go vet` findings in files we don't touch; keep our files clean, and don't reformat theirs.
- Repo uses LF line endings (`core.autocrlf=false`).
- Our changes so far: `request-health.go` (per-peer request queue sized by rate, snubbing, idle-peer retry; `ClientConfig.RequestQueueTime` and `PieceTimeout`).
