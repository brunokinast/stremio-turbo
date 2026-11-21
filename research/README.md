# Streaming research: fast, low-buffering 4K HDR in the Stremio fork

Local working folder (not a git repo; `data/` holds raw dumps that include the public IP, so never publish it as is).
Written by Claude during the overnight exploration of 2026-11-16. Start at `plans/00-START-HERE.md`: the ordered path, one plan file per step. `STATUS.md` has the state of the repos.

## Goal
Streaming 4K HDR episodes (reference: Peaky Blinders S06E01, `tt2442560:6:1`, torrent `9247c52e…`, 16 MiB pieces) should start fast and buffer as little as possible, and there should eventually be an option to download ahead so no buffering is possible at all.

## Layout
- `STATUS.md` — state of the code (three repos), uncommitted work, how to resume.
- `findings/` — what each codebase actually does, with file:line references: `01-libtorrent.md`, `02-anacrolix.md`, `03-rivulet-and-stremio.md`, `05-evidence.md` (what the recorded runs show), `06-second-code-exploration.md` (new findings, corrections to 01–05, current order of work), `07-utp-receive-path.md` (why a far uTP peer is slow for us, with a swarm-free test and fixes), `08-probe-correction-and-completion-path.md` (latest: the probe does not gate mpv in the shell; what happens between last chunk and readable piece).
- `design/00-synthesis-and-roadmap.md` — early conclusions: engine decision and the block-deadline scheduler design. Its roadmap is replaced by `plans/`.
- `plans/` — the path: `00-START-HERE.md`, then `01`–`09` in order. `done/` holds finished plans (the transport lab design).
- `findings/09-evidence-reread.md` — the recorded runs read against the code (swarm shape, 72 s start timeline).
- `findings/10-lab-results.md` — latest: measured mpv reads and transport lab results.
- `lab/transport/` — lab tools (relay, uTP downloader, real-swarm probe, run script).
- `data/` — raw evidence copied from the session scratchpad (status dumps, per-peer logs, scripts).

Files are added as they are written; a missing file means that part was not reached.
