# 10 — Lab results (2026-11-16 evening): mpv's real reads, and the transport lab

Measured, not inferred. The session ended mid-series; "Next" lists what is unfinished.

## A. What the shell's mpv really reads (offline, the shell's own `libmpv-2.dll`)
Tool: `scratchpad\mpvlab\lab.py` (Python range server with logging/throttling + libmpv via ctypes, `vo=null`), file: the full-length test copy of the reference episode (head 80 MiB + tail on disk). mpv v0.41.0-524, same `stream-lavf-o` as the shell.

| # | Request | Bytes read | Note |
|---|---|---|---|
| 1 | `bytes=0-` | 32 KiB | closed as soon as #2 opens |
| 2 | `bytes=6686667960-` (exactly the Cues position) | 603 KiB to EOF | Cues and Tags in one request |
| 3 | `bytes=9552-` (first cluster) | sequential | first frame after ~0.3 MiB |
| resume | `bytes=<cue position>-` right after #3 (49 KiB read on #3) | sequential | `loadfile … start=+N` |

- **Under 1 MiB in total before the first frame**; four short-lived readers on a resume. Plan 05 A1/B1 are confirmed as designed (exact tail from the SeekHead; nothing else is needed).
- mpv sends `Connection: close`. **If a response lacks `Connection: close`, this ffmpeg build does not open a new request on a seek: it reads the old response to its end** (it read 4 GB straight through on my first server). Go's `net/http` echoes the header, so Rivulet is fine today; never put a keep-alive proxy in front of the stream endpoint. [measured; cause in ffmpeg not read]
- Supply below bitrate (0.7 MB/s against ~0.9 MB/s local bitrate, 60 s): default mpv paused **7 times**; with `cache-pause-initial=yes`, `cache-pause-wait=10` it paused **twice** (11 s and 28 s). Total paused time is the same arithmetic either way: `duration × (bitrate/supply − 1)`. The config changes how pauses are grouped, not how much waiting there is.
- Consequence for "no buffering": for supply `s` below bitrate `b`, a pause-free play needs an up-front wait of `D × (b/s − 1)` (58 min at 1.5 vs 1.9 MB/s → 15 min). That buffer must live on **disk in Rivulet**, not in mpv's RAM; the UI can compute and show it from the server's measured rate. Feeds plan 07 Part C and plan 08.

## B. Transport lab (built and run; `lab\transport\`, results in `data\transport-lab\`)
Setup as in `plans/done/01-transport-lab-design.md`: test qBittorrent 5.1.2 (libtorrent) seeding a 384 MiB random file on 127.0.0.1:52001, uTP only → `udprelay` (150 ms each way) → `utplab` (anacrolix fork + instrumented copy of go-utp in `lab\transport\go-utp`). No control receiver yet.

| Run | What | Result |
|---|---|---|
| A0, A0b, A0-osbuf | no loss | Steady ~3 MiB/s (the 1 MiB window ÷ 0.3 s ceiling). **11 s to reach speed** every time |
| L1 | 1 packet dropped at 20 s | hole repaired in one round trip; rate halves (as LEDBAT predicts) and creeps back; **then at 37 s new holes appear with no relay drop and the transfer sits at ~3 packets/s for 12 s** |
| L5 | 5 consecutive dropped at 20 s | same: recovers, then at 31 s holes appear and throughput is **zero for the remaining 28 s** |
| L40 | 40 consecutive | first attempt invalid (overlapping run); redone in §C |
| S10 | 10 single losses, 50 packets apart, at 20 s; our 30-packet selective ack | **~3 s at 130–380 KiB/s** (10 holes × 0.3 s), then the halved rate |
| S10-bigsack | same, selective ack covering the whole reorder buffer (`UTPLAB_BIGSACK=1`) | **no stall**: 2.2 MiB/s in the same half-second, all holes repaired in one round trip. A later spontaneous burst of holes (475 packets buffered) was also repaired in ~1 s |
| S10-bigsack-osbuf | same plus a 4 MiB OS socket buffer | no stall, no spontaneous holes |

**Proof for plan 03 B2:** stall length ≈ number of holes × RTT with our receiver, and ≈ one RTT with the fix. The 12 s and 28 s outages in L1/L5 are the same thing with 35–90 holes.

What this establishes:
1. **H3 confirmed (start-up ramp is ours).** The request limit crawls 2 → 3 → 6 → 10 → 20 → 39 → 66 → 112 → 211 over 15 s (`reqq` lines in `A0b.txt`). At 300 ms RTT, 3 MiB/s needs ≥ 58 requests outstanding just to cover one round trip. The request-health cap (`rate × RequestQueueTime`, rate = whole-connection average) is circular: low rate → short queue → sender has nothing to send → low rate. Plan 04 R1 (deep first queue, +1 per chunk) is the fix; expect ~3 s instead of 11 s.
2. **Refill-when-empty costs ~25 % at steady state.** The queue drains to 1 before refilling (`1/(41)`, `13/(50)` …), the sender idles one round trip per cycle: 2.2–2.4 MiB/s average against a 3.4 ceiling. Plan 04 R2 (low-water refill).
3. **H2 confirmed, and it is the buffering mechanism.** With several holes in the reorder buffer, libtorrent repairs **one hole per round trip** because our selective ack shows only 30 packets past the first hole: 12 s and 28 s of zero goodput in L1/L5 while every arriving packet is acked with a selective ack (`sack` column = `inorder` column, 1–2 per 0.5 s). A libtorrent receiver acks its whole reorder buffer. Fix = plan 03 B2. Lab build of the fix (`UTPLAB_BIGSACK=1`, mask covers the whole reorder buffer): **no stall** in S10-bigsack (table above).
4. **H1 did not show** without stalls: thousands of scheduled window updates, zero duplicate acks sent (they merge into data acks). Not shown with stalls either (§C).
5. **Our acks are sparse**: ~40 acks/s for ~2000 packets/s (one per ~50 packets). Not shown harmful yet; it makes the sender bursty. Keep in mind for plan 03 B5.
6. **Open: where the spontaneous holes come from.** In L1/L5 (and once in A0) packets went missing with the relay dropping nothing and Windows reporting 0 UDP receive errors; everything the relay forwarded was processed by us. **Update:** `udprelay2.exe` counts sequence gaps as packets arrive from the sender, and it saw **zero gaps** in a run that had a spontaneous hole burst. So qBittorrent sent everything in order and the packets are lost **after** the relay, on our side. Best explanation: our 64 KiB UDP socket buffer overflows when ~50-packet bursts arrive (Windows does not count this in "receive errors"; my earlier packet accounting was too coarse to see ~50 missing). Evidence so far: 4 of 6 runs with the default buffer had such a burst, 0 of 2 runs with `UTPLAB_OSBUF=4194304`. **Confirmed in §C** (9 of 13 vs 0 of 8). If it holds, the chain is: sparse acks → sender bursts → our socket buffer drops a clump → short selective ack → seconds of zero throughput. Both ends of that chain are ours to fix.
7. Lab rule learned: wait ≥ 60 s between runs. Reconnecting at once after an unclean exit gets a connection that never delivers (one whole run was zero).

## C. Step 1 lab runs (2026-11-16, 20:03–20:39; `data\transport-lab\`, summary by `scratchpad\summ-lab.py`)
**Lab bug found and fixed first.** In four runs (B4, B5, O3, O4, moved to `invalid\`) the relay forwarded ~15 packets while 384 MiB arrived at 70–98 MiB/s: qBittorrent had learned `utplab`'s listen port and **dialled it directly, bypassing the relay**. `utplab` now sets `AcceptPeerConnections = false`; the four runs were redone. Every other run, including all earlier ones, was checked: relay-forwarded bytes = downloaded bytes (ratio 1.00–1.08, the excess being resends).

| Run | Setup | Avg | What happened |
|---|---|---|---|
| B1, B2 | default 64 KiB socket buffer | 2.54, 2.33 MiB/s | clean |
| B3, B4 | default | 0.28, 0.28 MiB/s | at 16–17 s a clump of packets lost after the relay (relay: 0 gaps from the sender, 137–141 resends), then **~3 packets/s for the remaining 43 s** |
| B5 | default | 2.37 MiB/s | same loss at 59 s (637 packets buffered behind the hole), run ended |
| O1–O5 | 4 MiB socket buffer | 2.40–2.52 MiB/s | **all clean**: 0 resends |
| RQT0 | default buffer, request-health cap off | 0.28 MiB/s | queue limit doubled 2 → 500 by 7.5 s; at 14.5 s the queue jumped from 1 to 475 requests, the loss clump followed, then ~3 packets/s for 45 s |
| L40 | relay drops 40 packets at 20 s | 0.94 MiB/s | **~4.5 s of zero**, then about half rate |
| L40-bigsack | same, full-buffer selective ack | 1.47 MiB/s | **no zero period**: 2.0 and 4.4 MiB/s in the next second |
| ST | 200 ms read stall every 8 s, default buffer | 1.15 MiB/s | stalls at 8 s and 16 s harmless; after the 24 s stall the 1 MiB window had closed (min 5 KB) and reopened → loss clump (79 resends) → ~3 packets/s for 21 s |
| ST-osbuf | same, 4 MiB buffer | 2.38 MiB/s | clean through every stall |

Verdicts:
- **Socket buffer (plan 03 B3): confirmed.** Loss clumps after the relay in **9 of 13** default-buffer runs over both sessions (B3, B4, B5, RQT0, ST + 4 of 6 earlier) against **0 of 8** with 4 MiB (O1–O5, ST-osbuf, A0-osbuf, S10-bigsack-osbuf). Windows never counted them as UDP receive errors.
- **What fills the buffer: bursts we cause.** Every clump came right after something that let the sender dump a large window at once: the queue jumping from 1 to ~200–475 requests (refill-when-empty, B4 and RQT0) or our receive window reopening after a stall (ST). So plan 04 R2 (low-water refill) also removes a trigger of this loss, not just the 25 % idle.
- **Why a clump costs tens of seconds:** the lost packets are contiguous, so our 30-packet selective-ack mask shows nothing received after the hole; the sender has no evidence and repairs about one packet per round trip (relay resends ≈ seconds × 3). B2's full-buffer mask fixes the recovery (L40-bigsack); B3's buffer removes the loss. **Do both.**
- **H1 (window updates counted as duplicate acks): not shown,** even with stalls: 0–2 duplicate acks and 0 duplicate packets in every run. Drop plan 03 B1.
- **H4 (late stamps leave slow start): not shown.** Stalls without a loss clump (ST at 8 s and 16 s, ST-osbuf) left the rate unchanged.
- **Ramp (plan 04 R1): both parts are ours.** Without the cap (RQT0) the limit doubles once per drained queue, 2 → 500 in 7.5 s; with the cap (B runs) it reaches ~150 at 8 s and ~290 at 15 s; 80 % of plateau after 10–15 s either way. Starting at 2 and growing only when the queue empties is slow on its own; the lifetime-rate cap makes it slower. R1 needs a deep start, per-chunk growth and a short-window rate; removing the cap alone would not do.

## D. Real 4K HDR swarm (2026-11-16, 20:40–21:02; `data\swarmprobe\`)
Probe runs of 240 s on the reference torrent, file 0, streaming like a player: `plain` (our receiver), `fixed` (full selective ack + 4 MiB buffer), `tcp` (uTP off), then the test qBittorrent with normal settings as a yardstick. Stremio and Rivulet were closed. Firewall: the new lab rule (TCP/UDP 49883–49889) worked. An incoming peer reached the probe in every run, and no block rule was created. UPnP mapping returned "500" from the router for one protocol.

**Result: the swarm had no supply tonight, so there is no transport data.**
- **qBittorrent:** 1.9 MB in 210 s. Its peers were a seed at 0.1–0.7 KB/s and a 41 % leecher at 2–5 KB/s. Swarm counts said 11 leechers + 4 seeds, but none of the leechers that carried the stream in the recorded runs (`peer-2`, `216.183…`) appeared.
- **Probe:** 0.1–0.2 MiB per run from the same two peers, plus one incoming Transmission peer with 435 pieces that never unchoked us.
- **One RTT measured:** `peer-3`, the uTP seed that served 795 KiB/s in the recorded run, answered at **223 ms** RTT, then kept us choked. That fits the lab's 300 ms setting and `09` §3b's estimate (~0.33 s).

**Side finding: metadata took about 58 s in all three probe runs, against 4 s for qBittorrent.** For ~60 s the probe sat at 16–17 half-open dials out of 18–19 known addresses: the slow dial funnel of `02` §5 with a cold DHT. qBittorrent has a warm DHT and a persistent profile. Add this to plan 06 Part B's evidence.

**Other 4K sources for S06E01, measured live (qBittorrent yardstick, 90 s each, everything else in it stopped):**

| Torrent | Release | Supply now | HDR (read from the file's MKV track headers) |
|---|---|---|---|
| `02f13dca…` | XEBEC, NF WEB-DL, single episode (3 files), 8 MiB pieces | **6.0 MB/s at 90 s**, 135 MB in 90 s. Peers: a libtorrent 1.2 leecher at 3.7 MB/s (incoming uTP, likely in Brazil) and two seeds at 0.6–0.9 MB/s. 32 MiB contiguous at 90 s | **No.** No Colour element and no Dolby Vision mapping; the name has no HDR tag. SDR 4K |
| `27193d8b…` | FLUX, iP WEB-DL, HLG | 0.5 MB in 90 s | HLG per the name (not checked) |
| `9247c52e…` (reference) | RAZORS, NF, HDR + DV, season pack, 16 MiB pieces | 1.9 MB in 210 s | **Yes**: PQ transfer, BT.2020, Dolby Vision `dvvC` |

Torrentio's seed counts (9 / 7 / 8) predicted none of this. The only well-supplied 4K source tonight is SDR. It can still serve the transport probe, because peer behaviour does not depend on HDR, but not the 4K HDR playback tests.

**Still open:** RTT and loss behaviour of the productive leechers. Repeat Part B when the swarm has supply. The recorded good runs were around 01:00–03:40 local; the fast leecher was in the US. First check with the qBittorrent yardstick for 60 s whether any peer delivers more than 200 KB/s; otherwise the probe runs say nothing.

## E. Real 4K HDR swarm, second title: House of the Dragon S02E01 (2026-11-16, 21:29–21:40)
No Peaky Blinders 4K HDR source had supply tonight; measured live, the S05 packs and the S06 single episodes gave 0–4 MB in 60 s. The user OK'd another title. I chose House of the Dragon S02E01: FLUX MAX WEB-DL, **DV + HDR, 10.7 GB, 16 MiB pieces** (like the reference), torrent `fe521cc3…`, file 1. qBittorrent yardstick: 1.27 GB in 60 s, 41 MB/s at 60 s, mostly from uTP libtorrent 1.2 seeds. Then three probe runs of 150 s, 90 s apart (`data\swarmprobe\hotd-*.txt`):

| Run | Total | From TCP peers | **From uTP peers** | uTP hole events (> 500 packets) |
|---|---|---|---|---|
| plain (our receiver, 64 KiB buffer) | 2073 MiB | 1889 MiB (18 peers) | **182 MiB (10 peers)** | 5378 holes (3) |
| fixed (full selective ack + 4 MiB buffer) | 2200 MiB | 1553 MiB (14 peers) | **647 MiB (15 peers)** | 11276 holes (7) |
| tcp (uTP off) | 2216 MiB | 2216 MiB (25 peers) | — | — |

What it shows:
- **RTT to real uTP peers: median 263 ms** (p10 231, p90 311; 199–310 ms over 15 peers). The lab's 300 ms setting is realistic, and `07` §1's arithmetic (one loss event costs ~20 s) applies.
- **Hole bursts are common on real peers:** dozens per run, often 100–1000 packets at once (reorder peak up to 1023, the buffer's limit), with either receiver.
- **The fix raises what uTP peers deliver about 3.6×** (182 → 647 MiB). Per peer, `peer-4` connected at about the same time in both runs and gave **31.9 MiB with our receiver and 183.9 MiB with the fix (5.8×)**. In the plain run its hole bursts were followed by 8–104 packets per 2 s for several seconds, which is the H2 stall. Caveat: sequential live runs see different peer sets, so treat the factor as indicative; the mechanism matches the lab.
- **Bursts larger than about 800 packets still recover slowly with the fix** (`peer-5`: 845 holes, then 10 packets per 2 s for 6 s). They fill the 1024-packet reorder buffer, so later packets are dropped and need resending too. Plan 03 B2 should check the reorder buffer against the advertised window, and stage B3's buffer and B5's denser acks so the sender is less bursty.
- **Totals are flat at ~14–15 MiB/s in all three runs**, TCP-only included, while qBittorrent reached 41 MB/s. So the probe's ceiling is not the transport. Most likely it is anacrolix's default 64 MiB `MaxUnverifiedBytes` (4 pieces of 16 MiB; E1 in `05`), which the probe does not raise but Rivulet sets to 1 GiB. This ceiling is irrelevant for a ~2 MB/s stream.
- **TCP vs uTP (plan 03 C1):** TCP-only gave the same total, and no peer was seen on both transports. No evidence that preferring TCP helps once B2/B3 are in. C1 stays last.
- The Windows UDP receive-error counter moved by **+1** in a run with thousands of holes. It is useless for detecting this loss, so plan 02 needs the go-utp counters.

## Next
Moved into the plans (2026-11-16): the remaining lab runs and the real 4K HDR swarm probe are `plans/01-finish-measurements.md`; the selective-ack and socket-buffer fixes are `plans/03-transport-fixes.md` B2/B3; the request-queue fixes are `plans/04-request-queue.md`. Lab script: `lab/transport/run-lab.sh` (replaces `scratchpad/tlab/run2.sh`).

## State left on the machine (after step 1, 2026-11-16 21:05)
- Test qBittorrent is **stopped**, in normal settings. `lab/transport/qbt-mode.py lab|normal` switches it. The lab torrent `53b85e9d…` is still in its profile, seeding from `scratchpad	lab\seed\lab.bin`.
- Firewall: rules "Streaming lab inbound TCP/UDP" allow ports 49883–49889 (Private profile) for any lab exe.
- Old test copies deleted (`%TEMP%
ivulet-ab\`, `scratchpad\cpu-25\`, ~56 GB). `scratchpad
ivtest-app\` is kept for `mpvlab`.
- `utplab` now refuses incoming connections (the relay-bypass fix); `utplab2.exe` was rebuilt at the same path.
- No product repo was changed. `lab	ransport\go-utp` is a patched **copy** of the module (counters, `UTPLAB_OSBUF`, `UTPLAB_BIGSACK`), never to be pushed.
