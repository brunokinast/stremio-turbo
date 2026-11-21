# START HERE — the path, in order

Goal: 4K HDR episodes in the Stremio fork start fast and do not buffer, and eventually download ahead so buffering is impossible. Reference: Peaky Blinders S06E01 (`tt2442560:6:1`), torrent `9247c52e17a888088fefe6bcce418dba52a353b6`, file 0 (6.69 GB, ~58 min, needs ~1.9 MB/s; 16 MiB pieces).

Plan files are numbered in the order to do them. **Do one step, report to the user, stop.** (User's rule: discuss → plan → build, one part at a time.)

## The picture in five lines
1. The swarm is tiny: two or three far leechers (incoming uTP) carry the whole stream; seeds are near-useless (`findings/09` §1).
2. **Start-up** loses most time *after* a good peer connects: tail piece first, whole 16 MiB pieces before any byte, head chunks parked on slow peers, slow request ramp (`findings/09` §2–3). The player only needs < 1 MiB before its first frame (`findings/10` §A).
3. **Buffering mid-play** comes from transport: a burst of `k` lost packets freezes a uTP stream for ~`k` round trips because our selective ack covers 30 packets (proven in the lab, `findings/10` §B); the losses are probably our 64 KiB UDP socket buffer.
4. Our request queue ramps in 11 s and idles the sender once per batch (−25 %) (proven in the lab).
5. When supply is below the bitrate, nothing but a disk buffer helps: total pause = `duration × (bitrate/supply − 1)`; mpv settings only group the pauses (`findings/10` §A) → plan 08.

## Steps
| # | Plan | What | Why here | Done when |
|---|---|---|---|---|
| 0 | (below) | Snapshot uncommitted work | Days of uncommitted changes in three repos | **Done 2026-11-16**: local branch `wip/streaming-research` in each repo (stremio-web `cafc99155`, rivulet-engine `64746c7`, anacrolix-torrent `4d6ea0fa`); nothing pushed; `main` untouched |
| 1 | `01-finish-measurements.md` | Lab leftovers (socket buffer, 40-packet burst, stalls, cap off) + **the real 4K HDR swarm probe** | Tools are built; settles which transport fixes to build and fills the RTT unknown. No product code | **Done 2026-11-16** (`findings/10` §C–E). The reference swarm had no supply; the real-swarm part ran on House of the Dragon S02E01 (4K DV/HDR, 16 MiB pieces) |
| 2 | `02-instruments.md` | Timeline, access log, stall log, per-peer samples, storage timings, lock holds in Rivulet; go-utp fork with counters; cold/warm/resume plays | Baseline that every later step is judged against (swarm noise is larger than most effects) | **Done 2026-11-16** (`findings/11`): `GET /debug/trace/{ih}` and `/debug/locks` in Rivulet; 4 plays on House of the Dragon S02E01. Peaky cold play still owed when its swarm has supply |
| 3 | `03-transport-fixes.md` | go-utp: selective ack (proven), socket buffer, ack timer; storage stalls if timed | Mid-play freezes; hides every other gain until fixed | **Done 2026-11-17** (`findings/12`): B2, B3, B4, B6 counter, storage S1; lab S10/L40 stalls gone; real play 0 stalls (was 2 / 13 s), uTP share 10 → 20 %, writes 4× cheaper. **3b (A1, sparse files) done the same day**: no start-up write stall |
| 4 | `04-request-queue.md` | Short-window rate, deep first queue with per-block growth, low-water refill | 11 s ramp → ~3 s; −25 % steady loss removed; lab-judged | **Done 2026-11-17** (`findings/13`): lab ramp 9.5 → 3 s, 3.3 MiB/s (ceiling); real play first frame **14.6 s** (step 3: 23.7). Then 4b/4c (B6): uTP window counts reorder bytes, reorder buffer 2016 for libtorrent's 528-byte floor packets: live drops 2163 → 0, uTP share 35 % |
| 5 | `05-startup-rules.md` | Only the player's bytes are urgent; exact tail from the SeekHead; offset-first chunks; responsive reads; re-plan on seek; reader-close grace; steal by delivery time | Largest start-up loss (~40 of 72 s) | **5a, 5c done 2026-11-18** (`findings/14`, `15`): first frame 6.8 s (1.5 s after the first chunk), seek 1.96 s. Metadata is now ~70 % of start-up, so plan 06 next |
| 6 | `06-preload-and-discovery.md` | Try smart-play preload; trackers, warm DHT, stable port, faster dialling | Moves discovery (28 s) and ramp before Play | **6a–6d done 2026-11-18** (`findings/16`–`19`): click at once 4.6 s (dial limiter 10→30/s), dwell 0.2 s, resume after dwell 0.9 s. **6e done** (`findings/20`): the best picture that keeps up, with a toast naming the passed-over source. **6f done** (`findings/21`): a ramping start is held on its first frame until 4 s is buffered. **6g done** (`findings/22`): board warm-up of the top continue-watching item. **6h done** (`findings/23`): rate memory; Resume → playing 1.75–2.5 s |
| 7 | `07-player-buffer.md` | mpv config file (Part A any time with the user's OK); "buffer first" UI later | Fewer, longer pauses instead of 1 s stutter | Measured pause count before/after |
| 8 | `08-download-ahead.md` | Background download of the next episodes | The only cure when supply ≤ bitrate | **Built** 8a–8e (`findings/25`–`29`). Open: the next-day check (next episode on disk, start < 2 s, zero pauses) |
| 9 | `09-later.md` | Source choice by measured supply, deadline scheduler, peer memory, merged swarms, giving back | Needs the earlier measurements | **Closed 2026-11-21** by plan 13 (`findings/41`): giving back built (1 MiB/s, only while downloading); the rest closed with measurements |
| 11 | `11-shared-path.md` | One record of disk, one health rule, one picker for Now, Next and mid-episode switching | Replaced the race, preload, warm-up and download-ahead with one path | **Done 2026-11-20** (`findings/34`–`36`). The `/downloads` hang was a deadlock, fixed in plan 12 (`findings/37`) |
| 12 | `12-manual-downloads.md` | Manual download of one episode (streams page) or a season (episode list), earliest episode first | The user asked for it after plan 11 | **Done 2026-11-20** (`findings/37`). Also fixed the `/downloads` hang: a client deadlock between uTP's socket lock and anacrolix's client lock |
| 10, 13 | `10-remaining.md`, plan 13 (`~/.claude/plans/playful-yawning-kettle.md`) | Everything left before new work: review fixes, cleanup, design pass, live checks, plan 10's measurements | Close every open item first | **Done 2026-11-21** (`findings/38`–`41`). Open: R3 (passive next-day check); pre-merge list at monorepo time |

Why this order: measure (1–2) before changing anything; then sustained rate per peer (3–4), because transport freezes and the slow ramp hide start-up gains and are judged deterministically in the lab; then what we ask for first (5); then start earlier (6); then the player and the disk buffer (7–8). Step 7 Part A and the plan 08 decisions do not depend on the others and can be raised with the user at any time.

## What is established, and what is not
| Claim | Status |
|---|---|
| Swarm: 43 GB season pack, 5 live peers, supply from 2–3 incoming leechers | Recorded data (`findings/09`) |
| 72 s start: 28 s no usable peer, ~20 s ramp, best peer on the tail piece, head chunks on a 13 KiB/s seed | Recorded data |
| File layout: headers end at byte 9552; Cues + Tags (~0.6 MiB) at the very end; SeekHead at byte 52 gives their position | Parsed |
| mpv reads 32 KiB head → `[Cues, EOF)` → from the first cluster; < 1 MiB before the first frame; needs `Connection: close` on responses | **Measured** (`findings/10` §A) |
| Request limit ramps 2 → 211 over 15 s (11 s to speed); refill-when-empty costs ~25 % | **Measured in the lab** |
| 30-packet selective ack: stall ≈ holes × RTT; full-buffer selective ack: no stall | **Measured in the lab** |
| Loss clumps on our side come from the 64 KiB socket buffer, triggered by sender bursts we cause (refill from empty, window reopening after a stall) | **Measured in the lab**: 9 of 13 runs vs 0 of 8 with 4 MiB (`findings/10` §C) |
| Window-update acks counted as duplicates (H1), late stamps (H4) | **Not shown**, with or without stalls: plan 03 B1 dropped |
| `prefetchNext` warms the next file *index*, not the next episode | **Measured** (`findings/11`): warmed S02E03, S02E02 is index 0 |
| Storage cost | **Measured and fixed** (`findings/12`): writes were slow because every read opened the file under the storage mutex, and MarkComplete outliers were a part-file stat waiting for a sync; now writes 0.37 ms mean, MarkComplete max 0.5 ms. **The first tail write zero-fills the non-sparse file: 7.5 s, blocking head writes and all peers' read loops (fixed in 3b: sparse files)** |
| Client lock holds ≥ 50 ms | **None** in 4 plays (`findings/11`) |
| Start on a healthy swarm: first frame 21–24 s, of which ~15 s is the first 16 MiB piece; every peer's request limit sits at 2 for ~10 s | **Measured** (`findings/11`); **reinterpreted** (`findings/12`): those 10 s are mostly the zero-fill write stall (peers can't deliver while their read loop is blocked) → fixed in 3b; re-baseline with one normal cold play before plan 04 R0/R1/R3 |
| Resume at 50 %: first frame 48 s (head 26 s, tail 16 s before the seek); after it, 85 head pieces vs 11 at the resume point, a 35 s stall | **Measured** (`findings/11`) → plan 05 |
| RTT to real uTP peers | **Measured**: median 263 ms (p10 231, p90 311) on a live 4K HDR swarm; 223 ms to the reference's uTP seed (`findings/10` §D–E) |
| Full selective ack + 4 MiB buffer on a real swarm | uTP peers delivered ~3.6× more (one peer 5.8×); hole bursts of 100–1000 packets are common (`findings/10` §E) |
| Time to metadata, cold | Probe ~58 s vs qBittorrent 4 s (`findings/10` §D) → plan 06 B |
| What `/opensubHash` costs at start; discovery cold vs warm | **Measured**: `/opensubHash` 8–36 ms after file-loaded, Rivulet has no `/subtitleSignature`; metadata 6.9 s cold vs 2.7 s warm process (`findings/11`) |

## Read first (20 minutes)
1. `../findings/10-lab-results.md` (latest measurements), then `../findings/09-evidence-reread.md` (the swarm and the 72 s timeline).
2. Background as needed: `../findings/07` (uTP), `08` (completion path), `06` (second code pass; §8 list of improvements). Where documents disagree, the higher number wins. `../design/00-synthesis-and-roadmap.md` is older reasoning; its roadmap is replaced by this table, its deadline-scheduler design (§4) is still the target of plan 09.
3. The `CLAUDE.md` in each repo (`stremio-web`, `rivulet-engine` has `AGENTS.md`, `anacrolix-torrent`).

Superseded, do not follow: the "swarmbench" plan (concurrent variants on the live swarm; invalid, `findings/05`), and the per-step numbering in `design/00` §7 and `findings/06` §9.

## Standing rules (from the user; they override convenience)
- Never type or print credentials, the Stremio auth key or the server IPC key. Never publish `data/` (it contains the public IP).
- Bind every service and test instance to 127.0.0.1 (`BIND_ADDRESS=127.0.0.1`).
- Never push to Stremio's, rivulet-kodi's or anacrolix's repositories. Never force-push. Our remotes are `brunokinast/*`; creating a new one (e.g. `brunokinast/go-utp`) needs the user's OK.
- Back up before changing the user's Stremio account or server settings, and only when asked.
- Proper fixes in the component that owns the problem (forks are fine); no env-var workarounds left in place; keep upstream merges easy (new behaviour behind `ClientConfig` fields).
- Tests: 4K HDR only, muted, stop playback afterwards. Only run a test whose data can explain a cause. Do not repeat live runs to "see if it is better".
- Windows firewall: **no prompts**, enforced by tooling.
  - `GOFLAGS=-exec=…\lab\tools\goexec.exe` (user Go env) runs every `go test`/`go run` binary from a fixed, allowed slot (`lab\testbin\slots`) and deletes it afterwards, so plain `go test` is safe.
  - Don't run test binaries from other paths.
  - Lab tools that listen live in `lab\transport\`; after adding one, run `lab\tools\allow-testbins.ps1` elevated (it also prunes stale rules). `GOWORK=off` in the anacrolix fork.
- pnpm only; TypeScript for new web files; user-facing text through i18n; LF line endings.
- Commit to `main` and push to `origin` only after lint and build pass and the change was tried in the real app.

## Landing the snapshot on `main` (with the first step that ships code)
Work continues on the `wip/streaming-research` branches. Before anything from them reaches `main`:
- Rivulet `go.mod`: the local `replace ../anacrolix-torrent` must become the pushed fork version.
- anacrolix fork: request-health on by default (`config.go:354–355`) → default off until plan 04 passes.
- Rivulet: fix the `diskMaxUnverifiedBytes` comment (per torrent on disk); the two `RIVULET_EXPERIMENT_*` switches become settings or go (plan 05 A4).
- `TestArchiveResolveLocalPath_SymlinkEscape` fails on Windows for unrelated reasons; leave it.

## Machine state (2026-11-16 evening)
- Test qBittorrent (session scratchpad `qbt\`, Web UI 127.0.0.1:8089) runs with lab settings; see plan 01 for restoring it.
- ~35 GB of test copies of the episode in `%TEMP%\rivulet-ab\`, `scratchpad\rivtest-app\`, `scratchpad\cpu-25\` (`mpvlab` needs `rivtest-app`); delete the rest once the user agrees.
- The session scratchpad can be cleared by a reboot; durable lab files are in `../lab/transport/`.
