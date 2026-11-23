# Research: making 4K HDR start fast and never buffer

The engineering record behind Stremio Turbo. Each finding is one measured step: what was wrong, the change, and numbers from before and after, with file:line references. The work ran as numbered plans from November 2026; the findings keep the plan and step names they were written under.

Raw evidence (traces, per-peer logs, benchmark runs) stays local in `data/`, which is never committed: it holds peer addresses.

## Findings
Where two findings disagree, the higher number wins.

**The starting point: reading the code and the first recorded runs**
- [01](findings/01-libtorrent.md) libtorrent, the reference engine · [02](findings/02-anacrolix.md) anacrolix/torrent, Rivulet's library · [03](findings/03-rivulet-and-stremio.md) from the click to the frame
- [05](findings/05-evidence.md), [06](findings/06-second-code-exploration.md), [08](findings/08-probe-correction-and-completion-path.md), [09](findings/09-evidence-reread.md) the recorded runs re-read against the code (06 and 09 correct the earlier ones)

**Transport: why a far uTP peer was slow, proved in a lab**
- [07](findings/07-utp-receive-path.md) the uTP receive path · [10](findings/10-lab-results.md) lab results and mpv's real reads · [11](findings/11-measured-play.md) baseline plays
- [12](findings/12-transport-fixes.md) selective ack, socket buffer, sparse files · [13](findings/13-request-queue.md) the request queue

**Start-up: only the player's bytes are urgent**
- [14](findings/14-startup-reads.md) start-up reads · [15](findings/15-critical-chunks.md) short queues and critical chunks · [16](findings/16-play-path.md) the real Play path, warm DHT
- [17](findings/17-race-rules.md) race rules · [18](findings/18-preload-resume.md) preload and resume · [19](findings/19-dial-rate.md) the dial rate limiter
- [20](findings/20-source-choice.md) the best picture that keeps up · [21](findings/21-start-buffer.md) the start buffer · [22](findings/22-board-warmup.md) Continue Watching warm-up · [23](findings/23-rate-memory.md) rate memory · [24](findings/24-mpv-config.md) mpv config

**Downloads**
- [25](findings/25-download-intent.md) download intent · [26](findings/26-download-budget.md) budget and eviction · [27](findings/27-download-ahead-web.md) download ahead · [28](findings/28-keep-alive.md) finishing after the app closes · [29](findings/29-episode-markers.md) episode markers
- [30](findings/30-thin-swarm-port.md) thin swarms and the listening port · [31](findings/31-pack-trial.md) season packs · [32](findings/32-port-warning-and-write-stall.md) port warning, a stalled write handle · [33](findings/33-loose-bugs.md) loose bugs

**One shared path for Now, Next and downloads**
- [34](findings/34-one-record-of-disk.md) one record of disk · [35](findings/35-downloads-screen-and-disk-copies.md) the Downloads screen · [36](findings/36-one-picker-next-slot-switching.md) one picker, switching mid-episode · [37](findings/37-manual-downloads.md) manual downloads

**Closing out, and parity with qBittorrent**
- [38](findings/38-review-fixes-and-cleanup.md) review fixes · [39](findings/39-live-checks.md) live checks · [40](findings/40-plan10-closed.md), [41](findings/41-plan9-closed.md) the last plans closed
- [42](findings/42-supply-gap.md) why Turbo got fewer peers · [43](findings/43-supply-parity.md) supply parity · [44](findings/44-mid-file-starts.md) mid-file starts · [45](findings/45-steady-state-speed.md) steady-state speed: dropped seedboxes, the uTP window

## Lab
- [`lab/bench`](lab/bench): the benchmark behind the README's numbers (Turbo, official Stremio and qBittorrent on the same torrent, fresh each run), and `ceiling.py`, one connection from a local libtorrent seeder with an optional delay.
- [`lab/player`](lab/player): mpv-driven plays and a smart-play emulator with its tests.
- [`lab/addons`](lab/addons): a local add-on serving season packs for tests.
- [`lab/tools`](lab/tools): `goexec`, which runs every `go test` binary from a fixed, firewall-allowed slot (set with `GOFLAGS=-exec=…\goexec.exe`), and the script that allows those slots.

The transport lab of findings 10–15 (a latency relay, uTP downloaders, a swarm probe and their run scripts) and the plans this work followed were removed once done. They are in the history at commit `24518c4c8`.

## Open
- Player buffer: count pauses before and after the mpv buffer settings on a thin swarm.
- The next-day check: the next episode on disk, a start under 2 s, no pauses.
- A libtorrent-based engine as an alternative to anacrolix, if supply parity regresses (findings 42–43).
- Prepare more than the first Continue Watching card, measured against the data it costs.
- Dial order: reach the peers that deliver before their slots fill (finding 45).
