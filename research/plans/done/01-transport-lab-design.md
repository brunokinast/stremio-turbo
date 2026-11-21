# Plan 01 — Transport lab: why a libtorrent peer sends to us slowly over uTP

> **Done (design record).** Built and mostly run on 2026-11-16; results in `findings/10-lab-results.md`. The remaining runs are in `plans/01-finish-measurements.md`. Kept for the setup details.

Written by the research model for an implementing model. This is a **diagnostic**; it changes no product behaviour. Read `findings/07` and `findings/09` §3a–3b first.

## Question
A libtorrent peer gave qBittorrent ~2 MB/s and gave Rivulet 0.3–0.6 MB/s with a deep request queue, growing linearly. Which of these is the cause?

| Id | Hypothesis | Source |
|---|---|---|
| H1 | Our window-update acks are counted as duplicate acks → spurious loss → window halved | `07` §2 |
| H2 | Our 30-packet selective ack makes real loss cost several round trips | `07` §3 |
| H3 | Our shallow request queue keeps the sender application-limited, so its window never grows | `09` §3a |
| H4 | Our reader stamps packets late (stalls, lock waits), sender sees delay ≥ 100 ms and leaves slow start | `07` §4, `09` §3f |

The lab must give a yes/no for each, on this machine, without the swarm, in runs of 60 s.

## Setup (everything binds 127.0.0.1 only)

```
qBittorrent (libtorrent sender)  <--UDP-->  udprelay (delay, loss)  <--UDP-->  utplab (anacrolix fork + go-utp fork)
        127.0.0.1:52001                    52011 | 52010                         ephemeral
```

1. **Content.** One 512 MiB file of random bytes and a `.torrent` for it with 16 MiB pieces, no trackers, private flag set. Build it with the fork's `metainfo` package (a 30-line Go program) so no extra tool is needed.
2. **Sender.** The existing qBittorrent test profile in the session scratchpad (`qbt\`, Web UI `127.0.0.1:8089`). Never touch the user's own qBittorrent or Stremio profile. Through the Web API: listen port 52001, interface address 127.0.0.1, DHT/PEX/LSD off, no rate limits, protocol "µTP only". Add the torrent with the save path that already holds the file, wait until it shows 100 % and "seeding".
3. **Relay `udprelay`** (new, Go, ~100 lines, in the lab folder). Listens on 52010 for the downloader and talks to 52001 from 52011. Forwards both ways, in order, after a configurable one-way delay. Options: `-delay 150ms`, `-loss 0.0001` (random), `-drop-burst N@K` (drop N packets K apart, once, after T seconds), `-rate` cap in bytes/s. Prints packets and bytes each way per second.
4. **Downloader `utplab`** (new, Go, in the lab folder, own `go.mod` with `replace` to `..\anacrolix-torrent` and to the local go-utp clone). An anacrolix `Client` with `ListenHost 127.0.0.1`, TCP disabled, no DHT, no trackers, no PEX, file storage in a temp folder that is deleted first. It adds the torrent, calls `AddPeers` with `127.0.0.1:52010`, downloads everything, and every 250 ms writes a CSV row: time, useful bytes, outstanding requests, request limit, plus the counters below. Flags:
   - `-initial-queue N` (requests allowed before any rate is known);
   - `-stall 200ms -stall-every 8s` (a storage wrapper that sleeps inside `WriteAt`, standing in for the fsync at piece completion);
   - `-request-health on|off`.
5. **go-utp clone** at `C:\Users\bruno\Documents\git\go-utp`, checked out at the commit in the fork's `go.mod` (`9f1664acf866`), branch `main`, never pushed upstream. Add a `Stats()` snapshot on `purego.Socket`, per connection:
   - `dupData`: data packets whose sequence was already delivered (`conn.go` "ancient" branch and the duplicate check in the reorder path);
   - `windowUpdateAcks`: state packets sent with the same ack number as the previous state packet and no selective ack;
   - `sackAcks`, `reorderInserts`;
   - `readBufPeak`, `rcvWindowMin`;
   - `procLagMaxUs`: largest gap between two returns of `pc.ReadFrom` minus time blocked in the read itself, and largest wait for `s.mu` in `processReceivedMessage`.
   Counters only; no behaviour change in this step.
6. **Control receiver.** A second qBittorrent profile (`qbt2\`, Web UI 8090, listen 52002, µTP only) that downloads the same torrent through the relay. It shows what the sender does with a libtorrent receiver under the same delay.

Note the built-in ceiling: both receivers advertise a 1 MiB window, so at 300 ms RTT nothing exceeds ~3.5 MB/s. Compare shapes and ratios, not absolute speed.

## Runs (60 s each, three repeats, relay delay 150 ms each way unless stated)

| Run | Receiver | Variation | Answers |
|---|---|---|---|
| C0 | qBittorrent | none | Baseline ramp shape and plateau |
| A0 | utplab | defaults | Same shape as C0? If yes, the lab does not reproduce the problem: stop and add the real-swarm counters instead |
| A1 | utplab | `-initial-queue 2` vs `64` vs `256` | H3: does ramp time follow the queue? |
| A2 | utplab | `-stall 200ms -stall-every 8s` | H1/H4: do `dupData` and `windowUpdateAcks` jump after each stall, with a rate saw-tooth? |
| A3 | utplab | stall 50, 100, 400 ms | Threshold: how long a stall is harmless |
| C1/A4 | both | relay `-loss 0.0001` | H2: rate ratio under random loss |
| C2/A5 | both | `-drop-burst 5@40` once at 20 s | H2: seconds to recover the previous rate |
| C3/A6 | both | delay 30 ms each way | Scaling with RTT (expect the gap to shrink with RTT²) |

## Reading the result

| Observation | Conclusion | Follow-up plan |
|---|---|---|
| A1: ramp time drops from ~20 s to ~2 s with a deep queue | H3 confirmed | Request-model change: deep initial queue, grow per block |
| A2: `dupData` > 0 on a lossless link, rate halves after stalls | H1 confirmed (every such packet is a spurious retransmit) | go-utp: window updates only when the advertised window was small |
| A2: no `dupData`, but rate still drops and `procLagMaxUs` > 100 000 | H4 confirmed | Keep the reader independent of the client lock; stamp at read time |
| A4/A5 recover several times slower than C1/C2 | H2 confirmed | go-utp: selective ack sized to the reorder span |
| A0 ≈ C0 and none of the above | Cause is outside the transport pair (path loss, the peer's own limits) | Real-swarm run with the same counters |

## Rules
- Fixed exe paths for `utplab` and `udprelay` (build once, vary by flags). They bind loopback only, so the firewall should not prompt.
- `GOWORK=off`. Do not commit the anacrolix fork or Rivulet as part of this plan; the go-utp clone gets one local commit with the counters.
- Stop both qBittorrent test instances and delete the lab's temp downloads at the end.
- Deliverable: `data/transport-lab/<run>.csv`, one summary table (ramp time to 80 % of plateau, plateau rate, counters), and a short `findings/10-transport-lab.md` stating which hypotheses held. No fixes in this step.
