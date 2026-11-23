# deps/torrent/: anacrolix/torrent fork

The BitTorrent library inside the server, forked from [anacrolix/torrent](https://github.com/anacrolix/torrent); `server/go.mod` points here with a `replace`. The root `CLAUDE.md` holds the principles, workflow and testing rules; these are the library-specific ones.

- When behaviour differs from qBittorrent, compare with libtorrent (its engine), and find out why a limit exists before changing it (`git log -S`).
- Keep changes upstream-friendly: configurable through `ClientConfig` with sensible defaults, documented, tested in the package's existing style (`qt`). Our additions are mostly new files (`request-*.go`, `piece-progress.go`, ...); `git log -- deps/torrent` lists them.
- Checks before committing, from this folder with `GOWORK=off` (upstream's go.work lists a submodule we don't check out): `go build ./...`, `go vet .`, the root package's tests.
- Upstream has `gofmt` and `go vet` findings in files we don't touch: keep ours clean, don't reformat theirs.
