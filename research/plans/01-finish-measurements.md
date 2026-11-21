# Plan 01 — Finish the measurements: lab leftovers and the real 4K HDR swarm (step 1)

Goal: settle the open transport questions with tools that already exist, before any product code changes. Everything here is built; this step only runs and reads. Read `findings/10-lab-results.md` first; the lab's design is in `done/01-transport-lab-design.md`.

No product repo changes. Output: new rows and verdicts in `findings/10-lab-results.md`, raw files in `data/transport-lab/` and `data/swarmprobe/`.

## Setup (already on the machine)
- Lab tools: `lab\transport\` — `udprelay2.exe`, `utplab2.exe`, `swarmprobe.exe`, `run-lab.sh`, `lab.torrent`, patched go-utp copy (`go-utp\`, counters, `UTPLAB_OSBUF`, `UTPLAB_BIGSACK`).
- Sender: qBittorrent test profile (session scratchpad `qbt\`, Web UI 127.0.0.1:8089), listening on 127.0.0.1:52001, uTP only, DHT/PEX/LSD/UPnP off, seeding `lab.torrent` from `scratchpad\tlab\seed\lab.bin`.
- If the scratchpad was cleared: create a new 384 MiB random file, `utplab2.exe -make <file> -torrent lab\transport\lab.torrent`, add it to the test qBittorrent with the file's folder as save path, wait for "seeding". Old qBittorrent preferences were saved to `scratchpad\tlab\prefs.before.json`; if that is gone, restore by hand after step 1 (listen port, interface "any", DHT/PEX/LSD/UPnP on, TCP and uTP).

## Rules for every run
- One run at a time, **60 s between runs** (an immediate reconnect after an unclean exit gets a connection that never delivers).
- From Git Bash: `cd lab/transport && [ENV] ./run-lab.sh <name> "<udprelay flags>" "<utplab flags>"`. Default relay delay is 150 ms each way (300 ms RTT).
- A run that overlaps another, or whose relay shows `seq gaps` > 0, is invalid; mark it so in the table.

## Part A — Lab leftovers (~40 min, loopback only)

| # | Runs | Question | Decides |
|---|---|---|---|
| A1 | 5 × `-secs 60` plain, 5 × the same with `UTPLAB_OSBUF=4194304`, alternated | Do the unexplained hole bursts come from our 64 KiB UDP socket buffer? (so far 4 of 6 vs 0 of 2) | Plan 03 B3. Count runs with a hole burst (`reoIns` > 50 in one sample with no relay drop) |
| A2 | `-burst 40` at 20 s: once plain, once `UTPLAB_BIGSACK=1` | Replaces the invalid L40 run: recovery after a 40-packet loss | Size of plan 03 B2's gain for socket-buffer-sized bursts |
| A3 | `-stall 200ms -stall-every 8s`: plain, then with `UTPLAB_OSBUF=4194304` | H1 (window-update acks counted as duplicates) and H4 (late stamps). Watch `dupAck`, `dupOld`, and a rate drop after each stall | Whether plan 03 B1 is needed |
| A4 | `-rqt 0` (request-health cap off), once | Is the 11 s ramp from the `rate × 3 s` cap or from the doubling-on-empty rule? | The design of plan 04 C1 |

How to read A3: a lossless link means every `dupOld` packet is a spurious resend. `dupOld` > 0 after stalls and a halved rate = H1 confirmed → build plan 03 B1. Nothing moves = H1 not shown, skip B1 and say so.

## Part B — The real 4K HDR swarm (the user's real test; ~6 min)
The probe streams file 0 of the reference torrent like a player reading as fast as possible, and prints per peer every 2 s: rate, request queue, and for uTP peers RTT, holes, duplicates, selective acks, plus OS UDP receive errors.

1. **Firewall first.** `lab\transport\swarmprobe.exe` is a new exe path: unapproved, it gets no incoming peers, and both productive peers on this swarm were incoming. Ask the user to approve the prompt on first start (or to allow building it to the already-approved `scratchpad\testbin\rivulet-engine.test.exe` path).
2. Stop the lab qBittorrent first, or restore its preferences: it must not hold port 49883 or seed to the probe.
3. Two runs, 150 s each, ≥ 2 min apart, BIGSACK off first:
   ```
   swarmprobe.exe -ih 9247c52e17a888088fefe6bcce418dba52a353b6 -trackers trackers.txt -file 0 -dir <scratch>\real -secs 150 > ..\..\data\swarmprobe\plain.txt
   UTPLAB_BIGSACK=1 UTPLAB_OSBUF=4194304 swarmprobe.exe ... > ..\..\data\swarmprobe\fixed.txt
   ```
   (Create `data\swarmprobe\` first. Delete `<scratch>\real` after each run.)
4. Read, per peer that delivered > 1 MB: transport, **RTT** (fills the last "Unknown" in `00-START-HERE.md`), hole bursts followed by low `pk+` for several samples (the H2 stall in the wild), OS UDP errors.

Caveat to state in the write-up: two runs on a live swarm differ by more than most effects (same build gave 45 s and 88 s first byte). Compare **mechanisms** (does a hole burst stall a fast peer for seconds; does the fixed run recover in one sample), not totals.

## Done when
`findings/10-lab-results.md` has: the A1 count, A2/A3/A4 rows, a yes/no per hypothesis H1–H4, and a "Real swarm" section with RTT per productive peer and whether H2 stalls occurred. Report to the user and stop.

## Clean-up
Close the lab qBittorrent or restore its preferences; delete `lab\transport\dl\` and the probe's download folder.
