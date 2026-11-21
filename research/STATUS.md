# Status (updated as work proceeds)

> **Implementing model: start at `plans/00-START-HERE.md`.** Plan files are numbered in the order to do them (steps 0–9; step 0, the WIP snapshot, is done), the standing rules, and what is proven versus hypothesis. The sections below are background on the repos and older work.

## Repos
| Repo | Path | Remote | State |
|---|---|---|---|
| stremio-web | `C:\Users\bruno\Documents\git\stremio-web` | brunokinast/stremio-web | uncommitted smart-play work (below) |
| Rivulet | `C:\Users\bruno\Documents\git\rivulet-engine` | brunokinast/rivulet-engine | uncommitted engine work (below) |
| anacrolix fork | `C:\Users\bruno\Documents\git\anacrolix-torrent` | brunokinast/torrent, branch `main` (= upstream `master` 753d7120) | uncommitted request-health + dial callback |
| libtorrent (reference, read-only) | session scratchpad `ref\libtorrent` (RC_2_0); re-clone `https://github.com/arvidn/libtorrent` if gone | — | — |

**2026-11-16 evening:** all of the work below is committed on a local branch `wip/streaming-research` in each repo (not pushed; `main` untouched). All of it builds and its unit tests pass (known Windows-only failure: `TestArchiveResolveLocalPath_SymlinkEscape`).

## Uncommitted work
**anacrolix fork**
- `request-health.go` (+ test): per-peer request queue capped at `rate × RequestQueueTime` (3 s, min 2); snubbing after `PieceTimeout` (20 s) cancels the peer's requests and limits it to 1; a 1 s client tick retries unchoked, interesting peers that sit at 0 requests. New `ClientConfig.RequestQueueTime`, `PieceTimeout`.
- `Callbacks.OutgoingConnResult` (dial outcome per address) in `callbacks.go`, fired from `Client.outgoingConnection`.
- `CLAUDE.md`.
- **Unproven**: live A/B was within swarm noise. See `findings/05-evidence.md`.

**Rivulet**
- `go.mod`: `replace github.com/anacrolix/torrent => ../anacrolix-torrent` (local path; must become the pushed fork version before committing).
- `internal/engine/engine.go`: `MaxUnverifiedBytes` 1 GiB on disk / cache÷4 in RAM (was anacrolix's 64 MiB default: 4 pieces client-wide with 16 MiB pieces); `PrimeFile` + `fileMu` (guess no longer primes the wrong episode's head and tail); real `Peers()`.
- `internal/engine/peerhints.go` (scrape → peer hints), `dials.go` (connection log), tracker cap 5 → 25.
- API: `/{ih}/peers` real, `/debug/status`, `/debug/dials/{ih}`, `fileIdx` on `/create`; OpenAPI updated.
- Built exe in the repo root includes all of this; **the installed exe** (`C:\Users\bruno\Tools\rivulet\rivulet-engine.exe`) is an older build (peers endpoint + debug status, old anacrolix, 64 MiB cap).

**stremio-web**
- `src/smartPlay/`: preload (with `fileIdx`), race rewritten (no blind pick, widening, progress panel, retry on stalled stream), `getLastQuality`. Lint and type check pass; not tried in the app.

## Runtime state when the session ended
- Stremio closed (stopped for tests). Webpack dev server may still run on 127.0.0.1:8080.
- qBittorrent test profile in the session scratchpad (`qbt\`), Web UI 127.0.0.1:8089; process stopped.

## Firewall (Windows) notes
- The prompt never blocks a process; unanswered, the exe just gets no inbound peers (which skews comparisons).
- Approval is per exe path. Already approved: `Documents\git\rivulet-engine\rivulet-engine.exe`, `Tools\rivulet\rivulet-engine.exe`, qBittorrent, and the fixed test binaries in the scratchpad `testbin\`.
- Rule for tests: one exe path for all variants (variants differ by env vars), unit tests bind loopback only, Go test binaries built to a fixed path (`go test -c -o …`) and run from the package dir.
- One-time fix needing elevation (user must run): allow rules for a fixed `bench` folder, or `Set-NetFirewallProfile -NotifyOnListen False`.

## Where to resume
See the bottom of this file ("Log") for what the exploration covered and what it did not reach.

## Added during the exploration (read this)
- `internal/engine/engine.go` in Rivulet now has **two env-gated experiments** (uncommitted): `RIVULET_EXPERIMENT_RESPONSIVE=1` (responsive reads) and `RIVULET_EXPERIMENT_TAIL=high` (head at Now, tail at High, no tail pre-read). They are off by default and must become real settings or be removed (roadmap step 2).
- The comment on `diskMaxUnverifiedBytes` says the cap is client-wide; that is wrong for disk storage (it is per torrent). Fix when committing.
- The approved "swarmbench" plan (concurrent variants on the live swarm) is **superseded**: see `findings/05-evidence.md` and `design/00-synthesis-and-roadmap.md` §6. `bench/swarmbench` was never started.
- `data/ab.py` runs variants side by side from one exe; use it only for mechanism checks, not speed comparisons.
- Test instances start with default server settings, not the user's.

## What the exploration covered
- Done: libtorrent request/timeout/streaming/choker/dial code (findings/01); anacrolix ordering, request model, reads, dialling, upload (findings/02); Rivulet reader policy and the Stremio player path incl. mpv and the shell's allowlist (findings/03); evidence table (findings/05); synthesis, engine decision, scheduler design, levers, measurement design, roadmap (design/00).
- Not reached: separate per-step plan files in `plans/` (the roadmap in design/00 §7 has scope and acceptance per step); Rivulet's HLS/transcode path; stremio-core; libtorrent's uTP and disk layers; a design document for merged swarms; whether mpv reads the MKV tail at a fresh start (check Range requests in a real play before shipping roadmap step 2a).

## Read next: `findings/06-second-code-exploration.md`
Written after a second, read-only pass (2026-11-16 morning). It changes the picture: the reference swarm's supply is one uTP peer; Windows cache files are not sparse (zero-fill on the tail write); seeks only re-plan idle peers; the probe has a 30 s timeout and default probe sizes; `btMaxConnections` is not wired; the shell loads a user mpv config, so no shell fork is needed for buffering. Its §9 is the current order of work and §10 lists corrections to the older files. No code was changed in that session; `stremio-shell-ng` was cloned to the session scratchpad (`ref\stremio-shell-ng`) for reading.

## Then: `findings/07-utp-receive-path.md`
Third read-only pass. Explains the uTP rate gap as loss-event frequency (one halving costs ~20 s at 250 ms RTT). Two receiver behaviours in `go-utp/purego` raise it against libtorrent senders: window-update acks are counted as duplicate acks (spurious loss whenever our read loop stalls and drains), and the selective ack covers only 30 packets. Gives a loopback test that proves or kills it without the swarm, the fixes (go-utp fork), and the changes to the order of work in `06` §9. Not measured yet.

## Then: `findings/08-probe-correction-and-completion-path.md`
Corrects `06` §2: in the shell `/hlsv2/probe` does not run before mpv loads (`window.qt` disables the probe path), so the "cheaper probe" item is mostly moot. Tail demand at start comes from `/opensubHash`, `/subtitleSignature`, mpv and Rivulet's priming; helper reads should get a background class. Also maps last-chunk → readable: fsync holds the storage mutex against all writes and player reads; readers are unblocked only after the completion write.

## Then: `findings/09-evidence-reread.md` and the transport lab (`plans/done/01-transport-lab-design.md`, results in `findings/10-lab-results.md`)
`09` re-reads the recorded runs: the reference torrent is a 43 GB season pack with 5 live peers, two near-useless seeds, and two or three incoming leechers that hold our episode. It gives a timeline of the 72 s start (28 s without a usable peer, 20 s ramp, best peer spent on the tail piece, head chunks parked on a 13 KiB/s seed) and six new findings (request ramp throttles the sender's ramp; linear growth means the sender left slow start at once; cancel storms; uTP reader waits on the client lock; discovery is incoming and the tests ran a cold DHT). `plans/01` is a loopback lab (qBittorrent → delay relay → anacrolix) that decides between four transport hypotheses. Ready to hand to an implementing model.

## Log
- 2026-11-16 ~03:00: folder created; evidence copied to `data/`.
- ~03:40: findings 01, 02, 03, 05 and design/00 written; two live mechanism experiments run (`data/ab-1116-033153`, `data/ab-1116-033418`).
