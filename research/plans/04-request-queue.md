# Plan 04 — Request queue: ramp fast, never let the sender idle (step 4)

> **Done 2026-11-17** (`findings/13`): R0–R2 built as `ClientConfig.InitialRequestQueue` and `RequestLowWater` (anacrolix `60e7f385`), set by Rivulet (`01229fa`). R3 (request-health defaults off before `main`) stays a landing TODO.

Goal: a peer that can send fast gets enough requests within one or two round trips, and never runs out of them. Split out of the start-up rules because the lab proved both problems and can judge the fix without the swarm (`findings/10` §B items 1–2):
- the request limit crawls 2 → 3 → 6 → … → 211 over 15 s, so the transfer takes **11 s** to reach speed at 300 ms RTT;
- the queue drains to empty before refilling, so the sender idles once per batch: **~25 %** below the ceiling at steady state.

Step 1 answered the design question (`findings/10` §C): without the cap the limit still only doubles once per drained queue (2 → 500 in 7.5 s); with the cap it is slower still. Both must change, so R0–R2 stand as written. Step 1 also showed the queue jumping from 1 to 200–475 requests on refill makes the sender burst and overflow our socket buffer: R2 removes that trigger too. Step 3 lands first so transport stalls do not hide this step's gain.

Files: `anacrolix-torrent` — `peerconn.go` (`nominalMaxRequests` ~:1776, `isLowOnRequests` ~:2104, chunk receipt ~:2132), `requesting.go` (`peakRequests` ~:486), `request-health.go`, `peer.go` (`downloadRate` ~:194), `config.go`.

## Changes
| # | Change | Now |
|---|---|---|
| R0 | **Short-window rate**: useful bytes over the last ~5 s, updated per chunk; used by R1–R2 and by `request-health` | `downloadRate()` averages the whole connection and lags |
| R1 | **Deep first queue, growth per block**: a peer with no rate starts at `InitialRequestQueue = 32` (512 KiB); while its short-window rate is unknown or rising, +1 to its limit per useful chunk (doubles per round trip, as libtorrent's slow start, `findings/01` §2); once stable, limit = `rate × RequestQueueTime` (3 s), never below 4 for a peer that delivered in the last 5 s | Limit = `min(peer reqq, 2 × requests added by the last update, 963)`, and the request-health cap uses the lifetime rate: circular (low rate → short queue → sender has nothing to send) |
| R2 | **Low-water refill**: `isLowOnRequests` = outstanding ≤ half the limit; at most one request update per peer per 100 ms | Update only when the queue is empty: one idle round trip per batch |
| R3 | `request-health` defaults: switch them off (`RequestQueueTime = 0`, `PieceTimeout = 0`) before anything lands on `main`; switch them back on in `NewDefaultClientConfig` only after R0–R2 pass | On by default in the WIP snapshot (`config.go:354–355`) |

All numbers are `ClientConfig` fields with today's behaviour available, so the fork stays mergeable. Rivulet sets them.

## Tests
- Fork, root package, `qt` style table tests with fake peers: limit after N chunks (R1), refill fires at half (R2), update rate-limited to 100 ms, short-window rate decays after silence (R0).
- Build the test binary to the fixed path in `STATUS.md`; `GOWORK=off`; `go vet .`.

## Acceptance (lab, deterministic)
Rebuild `utplab` against the changed fork (same exe path), `run-lab.sh` with no relay loss, `UTPLAB_OSBUF=4194304 UTPLAB_BIGSACK=1` (step 3's fixes in place), three runs:
1. Time to 80 % of plateau ≤ 3 s (was 11 s).
2. Steady-state average ≥ 90 % of the window ceiling (~3.2 of 3.4 MiB/s; was 2.2–2.4).
3. `reqq` lines never show the queue at ≤ 1 outstanding after the first second.

Then one real play with step 2's instruments: the first fast peer's limit reaches `R × 3 s ÷ 16 KiB` within 3 s of its unchoke.

## Out of scope
Who may carry urgent chunks and stealing (plan 05 Part C), the deadline scheduler (plan 09).
