# Evidence: what the recorded runs show (and what they cannot show)

Reference: Peaky Blinders S06 pack, `9247c52e17a888088fefe6bcce418dba52a353b6`, 45.5 GB, 2711 pieces of **16 MiB**, file 0 = S06E01 (pieces 0–398, ≈ 6.7 GB). Runs on 2026-11-16, 01:00–03:40 local. Raw files in `data/`.

## Established (mechanism read in code and seen in data)
| # | Finding | Evidence |
|---|---|---|
| E1 | anacrolix's 64 MiB `MaxUnverifiedBytes` allowed 4 requestable pieces per torrent with 16 MiB pieces | `rivulet-status-60s.txt`: only pieces 0 and 398 in progress, peers with other pieces "not interested"; code: findings/02 §1 |
| E2 | **The tail piece downloads before the head** (both primed at Now; rarest-first tie-break) | `ab-1116-033153/*.txt`: every request on piece 398 for 60–70 s; first byte right after 398 completed |
| E3 | **Reads wait for a whole verified piece**; responsive mode removes that | same run: verified first byte 115 s vs responsive 73 s; 5 MB at 80 s |
| E4 | With head-first + responsive the first byte comes as soon as any block of piece 0 arrives | `ab-1116-033418`: 32 s vs 85 s |
| E5 | **A slow peer holding the earliest blocks stalls playback even when fast peers exist** (queue of 2, 10 KiB/s, never reassigned) | `ab-1116-033418/headfirst-responsive.txt` 50–90 s: piece 0 served only by peer-1 at ~10 KiB/s, player at 0.7 MB for a minute |
| E6 | One fast peer queues ~a whole piece (949–963 requests) | `diag2.txt` at 60 s; code: 963 = `maxLocalToRemoteRequests` |
| E7 | Dials are worked off at ~25 per 8–10 s; 143 addresses take ~40 s | `diag-fixes-v2.txt`, `diag-peers200.txt` ("known/pending/half-open") |
| E8 | The first-file guess primed the wrong episode's head and tail | status dumps: `1! … 1!` at pieces 2110/2711 (fixed, uncommitted) |
| E9 | Supply ≈ demand: qBittorrent pulled 1.6–2.1 MB/s; the episode needs ≈ 1.9 MB/s (6.7 GB over ≈ 58 min, runtime from memory) | qBittorrent runs in the session log; three runs |
| E10 | qBittorrent (plain sequential, no streaming mode) had 16 MiB contiguous at 18–19 s and 96–160 MiB at ~110–125 s | two runs with `ab-qbt2.py` |

## Not established
- Whether the uncommitted `request-health.go` (rate-capped queues, 20 s snub, idle retry) helps. Runs before/after differ less than two runs of the same build (first byte 88 s vs 45 s, same build, minutes apart).
- Whether 100 parallel dials help (that run never applied the setting).
- How much of the gap to qBittorrent is dial speed vs request scheduling vs ordering.

## Why live comparisons misled, and what that means for measurement
1. **Swarm drift**: the same build gives 45–115 s first byte within an hour.
2. **Simultaneous variants from one IP are invalid.** Tried in `ab-1116-*`:
   - the two test instances **found each other** (peer `<our public IP>:49881` is our own public address) and one uploaded the tail piece to the other;
   - libtorrent refuses a second connection from the same IP by default (`allow_multiple_connections_per_ip = false`), so the variants **split the swarm** between them (each knew only 4–8 peers).
   So the approved "swarmbench" idea of concurrent variants plus qBittorrent on the live swarm cannot give a fair comparison. Qualitative findings from those runs (E2–E5: *which piece* was requested, *who* held a block) are still valid, because they are mechanisms, not speeds.
3. Test instances start with **default server settings** (hard limit 3.5 MiB/s, soft 2.5 MiB/s), not the user's (37.5 / 4 MiB/s). Not binding at the speeds seen, but a harness must copy the real settings.

→ `design/60-measurement.md`: a deterministic **lab swarm** on loopback is the primary tool; the live swarm is for mechanism checks and for interleaved, repeated validation.
