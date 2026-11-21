# Plan 02 — Instruments in the real server (step 2)

Goal: one play of the reference episode in the real app produces records that say, second by second, why the player waited. Every later step is accepted or rejected with these, so they come before any behaviour change. No behaviour changes in this step.

Why not just time "first byte": the same build gave 45 s and 88 s minutes apart. Acceptance in later plans is therefore **relative to events inside the run** (when a peer unchoked, how fast it was), and that needs these records.

Already answered, so not re-measured here: what mpv reads at start and on resume (`findings/10` §A: 32 KiB head, then exactly `[Cues, EOF)`, then from the first cluster; under 1 MiB before the first frame). Answered by step 1 if it ran: RTT to the productive peers, whether uTP hole stalls happen on the real swarm.

Work on the `wip/streaming-research` branches (step 0).

## What to record
All in memory, ring-buffered per torrent, served next to `/debug/status` and `/debug/dials/{infoHash}` (`internal/api/api.go`). Times are milliseconds since `/create` for that torrent.

| # | Record | Where | Fields |
|---|---|---|---|
| 1 | **Start timeline** `/debug/timeline/{ih}` | Rivulet engine | `/create`; metadata ready; DHT node count at create; first peer **known** and first **connected** per source (tracker, DHT, PEX, incoming); first unchoke; first chunk; first piece verified; first byte served to a stream request |
| 2 | **HTTP access log** `/debug/requests/{ih}` | Rivulet API layer | request id; endpoint class (`stream`, `opensubHash`, `subtitleSignature`, `probe`, `stats`, other); `Range`; start; first-byte delay; bytes sent; end reason. Still needed for the helper endpoints and to check plays against `findings/10` §A |
| 3 | **Reader stall log** (in the timeline) | `pinnedReader.Read` (`engine.go:~1761`) | every wait over 250 ms inside a read: file offset, piece, duration, request id. This is the server-side definition of "buffering" |
| 4 | **Per-peer samples**, 1 Hz | Rivulet, from `PeerConn` stats | address hash, client, transport, direction, source, choked/interested, useful bytes, outstanding, cancelled, request limit, pieces it has within our file, RTT |
| 5 | **Storage timings** | anacrolix fork `storage/` | duration of the first write to each file; every `Sync`; longest single write per second. Gates plan 03 A1–A3 |
| 6 | **uTP counters** | go-utp fork (below) | per connection: in-order, duplicates, reorder inserts, selective acks, duplicate acks, RTT, reorder peak; global: socket read errors |
| 7 | **Client lock holds** | anacrolix fork | longest hold per second, caller when over 50 ms. Gates plan 03 A4 |
| 8 | OS counter | capture script | `netstat -s -p udp` receive errors before and after |

### The go-utp fork (created here, used by plan 03)
- Clone `github.com/anacrolix/go-utp` to `C:\Users\bruno\Documents\git\go-utp` at the commit the anacrolix fork's `go.mod` pins (`9f1664acf866`). Branch `main`. Remote `brunokinast/go-utp` only with the user's OK; until then the anacrolix fork uses a local `replace`.
- Port the counters from the lab copy (`streaming-research\lab\transport\go-utp\purego\lab.go` and the counter lines in `conn.go`) as a proper API: a `Stats()` snapshot per `Conn` and per `Socket`, no globals, no env vars. Leave `UTPLAB_BIGSACK` and `UTPLAB_OSBUF` behind; those become plan 03 B2 and B3.
- Commit 1: counters only, no behaviour change, with a unit test.

Notes:
- TCP RTT: delay from a request sent on an empty queue to its first chunk.
- Items 5 and 7: small optional hooks (existing `Callbacks` or `expvar`), so upstream merges stay easy.
- Hash peer IPs in anything that could be shared; never log query strings (they can carry keys); never log the auth key or IPC key.

## Capture
1. `data/capture-play.ps1 <infoHash>`: at the end of a play, saves every `/debug/*` endpoint for the torrent plus `/debug/status` into `data/play-<timestamp>/`. No polling during the play (it changes lock timing; the ring buffers make it unnecessary).
2. Build Rivulet to the repo-root exe (firewall-approved). Start through `stremio-web\scripts\start-stremio.ps1`.
3. Plays of the reference episode, 4K HDR, muted, 120 s each, then stop:
   - **cold**: Rivulet just started, this torrent's cache removed first;
   - **warm**: Rivulet running for 10 minutes, cache removed again;
   - **resume at 50 %**, cache removed.
   Remove the cache through Rivulet's remove call or with the server stopped; note which.

## Output
`findings/11-measured-play.md`, per play:
- a time → event → cost table like `findings/09` §2;
- byte ranges requested before the first frame per endpoint (confirm mpv matches `findings/10` §A; what `/opensubHash` and `/subtitleSignature` cost);
- time to first DHT peer and first incoming peer, cold vs warm;
- RTT and transport of each peer that delivered more than 1 MB;
- first tail write and first `Sync` durations (the zero-fill question);
- the remaining "Unknown" rows of `00-START-HERE.md` filled in.

## Done when
The play folders exist, the findings file is written, and the user has seen it. This is the baseline every later step compares against.

## Out of scope
Any change to priorities, requests, transport or the web client. If something is obviously broken, write it down and leave it.
