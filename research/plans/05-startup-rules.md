# Plan 05 — Start-up rules: what we ask the first good peers for (step 5)

> **5a done 2026-11-18** (`findings/14`):
> - A1 (pins only before a stream; `warmMoov` removed), A4, B1 (`ReaderOffsetFirst`) and B2 (responsive readers) are built.
> - Real play: first frame **9.8 s**, 2.4 s after the first chunk (step 4: 14.6 s).
> - **Not needed, on evidence:** A2 (helpers come after the first frame, 0 ms), A3 (next episode 1.3 % of bytes in the first 60 s), B2's range-scoped wait (pending-write waits total 149 ms).
> - **Next: Part C.** Urgent chunks wait a median of 1.6 s in peer queues.
> - A5 and B3 remain, unmeasured.
>
> **5c done 2026-11-18** (`findings/15`): Part C built as queues sized by transport round trip (`RequestQueueRTTs`, `PeerConnRTT`) and critical chunks moved by delivery time (`ReaderCriticalBytes`; C3 + C4).
> - Real play: first frame **6.8 s**, 1.5 s after the first chunk; seek 1.96 s; cancels ≤ 1.2 %.
> - Lab: seek 1.76 → 0.86 s at equal throughput.
> - **Left:** the queue jump (cancel unsent requests ahead) only if seeks need more; B3 and A5 are unmeasured.
> - **Next lever:** metadata (plan 06), now ~70 % of start-up.

Goal: when a peer that can feed the stream shows up, the bytes the player is waiting for arrive first, at that peer's full rate. In the measured run (`findings/09` §2) about 40 of the 72 s were lost after such a peer was already connected. This is the largest start-up lever.

Prerequisites: step 2's baseline play (`findings/11`), steps 3–4 landed (so the peer delivers at full rate and ramps in seconds; otherwise this step's gain is hidden). The ramp and refill changes that used to be C1, C2 and C5 here are now plan 04.

Do the parts in order. Each is testable alone.

## Part A — Rivulet: only the player's own bytes are urgent (no fork change)

Files: `rivulet-engine\internal\engine\engine.go`.

| # | Change | Now | Why |
|---|---|---|---|
| A1 | Only a blocked stream reader makes a piece `Now`; pins sit just below | `primeBoundary` (`:2435`) puts the head **and** the tail piece at `Now`; `warmMoov` (`:1495`) opens a reader that drains the last 8 MiB for every non-faststart file, which includes every MKV | The fast peer's whole queue (947 requests) went to the tail piece while the next head piece had 2. `Now` on the tail outranks the head's readahead |
| A2 | Helper endpoints never raise priority | `/opensubHash` and `/subtitleSignature` read through normal readers (`internal/media/media.go:679–707`), which makes their pieces `Now` | A 64 KiB hash read should not cost a 16 MiB piece of the best peer's time before the first frame |
| A3 | Next-episode prefetch waits for a healthy buffer | `prefetchNext` (`:1896`) raises 8 pieces (128 MiB) of the next episode to `Normal` at the first read of the current one | On a thin swarm that is minutes of supply during the worst moment |
| A5 | Grace period when the last reader of a file closes | `pinnedReader.Close` (`engine.go:1846–1868`) sets the file to priority `None`, releases the pins and forgets `primed` at once | Any short-lived reader that happens to be the last one (a helper fetch, smart-play's race read in `stremio-web\src\smartPlay\race.ts:122`) cancels a preload or the pins; mpv reconnects flap the priorities |
| A4 | Remove the two `RIVULET_EXPERIMENT_*` switches | `engine.go:1450, 1455, 2462` | They become the behaviour below or go away |

Design:
- **A1.** The rule: **a piece is `Now` only because a stream reader is positioned on it. Static pins never sit at `Now` while a stream reader exists.** Priorities in the fork are `None < Normal < High < Readahead < Next < Now` (`types/types.go:48`).
  - What mpv does (**measured**, `findings/10` §A, the shell's own libmpv): reads 32 KiB of head, then opens `bytes=<Cues position>-` and reads Cues and Tags (603 KiB) to the end of the file, then reads from the first cluster (byte 9552); first frame after ~0.3 MiB of clusters. On a resume it opens `bytes=<cue position>-` right after. Under 1 MiB in total before the first frame. So the order the player needs is: first header bytes → tail (Cues and Tags) → the rest of the head.
  - Pins: head pieces `Now` only until the first stream reader opens (this is the preload phase, where no reader exists), then demote the pin to `Next`. Tail pieces `Next` from the start: above the head's readahead window, below the piece a reader is blocked on. Drop the `warmMoov` drain (a draining reader is a second `Now`).
  - Why the demotion matters: when mpv jumps to the tail, its reader makes the tail piece `Now`. If piece 0 were still pinned at `Now` it would tie, and partial pieces win ties, so the tail would wait for the remaining ~15 MiB of piece 0 while the player is blocked on the tail.
  - With Part B (chunk-level reads) this becomes: first MiB in ~2 s at 500 KiB/s, Cues in ~2 s, then playback data. Without Part B the player still needs two whole 16 MiB pieces first, so A1 alone only fixes the order.
  - For a resume (stream request at an offset > 0) nothing special: readers drive it.
- **A2 — how to tell them apart.** The stream handler has a single `eng.NewReader` call (`internal/api/api.go:958`); the helper endpoints fetch the media through the server's own stream URL, so they arrive there looking like a player. Mark internal fetches (a private header set by `internal/media`, stripped and never trusted from outside clients) and branch on it in the handler.
- **A2.** Give helper reads a background reader: set the needed pieces to `Normal` if they are below it, then wait for the bytes with a deadline (suggest 60 s) and read them from storage once present. No `torrent.Reader` while waiting. If a stream reader for the same file has been blocked in the last 5 s, do not even raise to `Normal`. Return the endpoint's existing "not available" answer on timeout; the web client already tolerates a missing hash.
- **A1 addition — exact tail from the file.** Parse the Matroska SeekHead from the first bytes of the file as soon as they are on disk (it sits at byte 52 of the reference file; see `findings/09` §5) and pin exactly `[Cues position, end of file)` with the start-chunk hint from B1. Keep the 8 MiB window only as the fallback for files without a usable SeekHead and for MP4. Store the parsed positions per file (small JSON next to the cache) so a later start does not need to wait for them.
- **A3 additions.** Choose the next episode by the episode number in the file name, not by torrent file index (on the reference pack the next index after E01 is E05, and its 7 GB file was created and zero-filled for nothing). Never prefetch a tail; prefetch the head only.
- **A3.** Call `prefetchNext` only when the playing reader has been unblocked for 60 s and its position is past 50 % of the file (cheap proxy for "buffer is healthy and the next episode is plausible").
- **A5.** Keep the file's priority and pins for 30 s after the last reader closes; cancel the timer when a reader opens. An explicit stop from the client (the web client's `remove`) ends it at once. This replaces the old player-buffer plan's Part B (`07-player-buffer.md`).
- Fix the comment on `diskMaxUnverifiedBytes` (per torrent, not client-wide) while here.

Tests: the existing engine tests around `primeBoundaryRange` and tail warm; add table tests for the new priorities. `gofmt -s -l`, `go vet ./...`, `go test -p 1 ./...` (the symlink test fails on Windows regardless).

## Part B — fork: serve the bytes of a read, not its piece

Files: `anacrolix-torrent` (`reader.go`, `torrent.go`, `requesting.go`), then Rivulet turns it on.

| # | Change | Now |
|---|---|---|
| B1 | Request chunks of a reader's piece **starting at the reader's offset**, then wrap | `iterUndirtiedRequestIndexesInPiece` (`torrent.go:3545`) starts at chunk 0; the last tie-break in `lessByValue` (`requesting.go`, `EagerOrdered(ml, leftRequest, rightRequest)`) is ascending request index |
| B2 | Rivulet calls `SetResponsive()` on stream readers | Only behind the env experiment (`engine.go:1455`) |
| B3 | Re-plan **every** peer when a reader seeks or a priority changes; cancel requests whose piece dropped out of the wanted set | `updatePeerRequestsForPiece` (`torrent.go:1666–1691`) only re-plans peers with no requests; priority drops never cancel |

Design:
- **B1.** `Torrent` keeps, per piece that has a reader positioned inside it, the chunk index of the lowest such reader offset (update it where reader positions already update piece priorities, `Torrent.updateReaderPieces`). Candidate iteration and the heap tie-break order chunks by `(chunk − start) mod chunksInPiece`. Pieces without a reader keep ascending order. With 16 MiB pieces this turns a read at the end of a piece from 1024 chunks into the few it needs.
  - Also expose the start chunk as a hint the client can set for a pinned piece without a reader (for example `Torrent.SetPieceRequestStart(piece, offset)`), so Rivulet can pin the tail with "start at file end − `tailWindowBytes`". The Cues sit at the end of the last piece; in ascending order they would arrive last.
- **B2.** Responsive reads return bytes that are written but not yet hash-checked. For video that is acceptable; a bad chunk shows as a glitch and the piece is re-fetched. Check `storage.go:56, 87` (`waitNoPendingWrites` is per whole piece): if a responsive read still waits for unrelated pending writes in the piece, make the wait cover only the read range. That is the one risky edit in this part; give it its own test.
- **B3.** Keep it cheap: on seek, mark all peers `needRequestUpdate` once; cancel only requests for pieces now at priority `None`. Do not cancel readahead that merely moved down.
- Make each of these a `ClientConfig` option with the old behaviour as default, so the fork stays mergeable; Rivulet sets them.

## Part C — fork: who may carry urgent chunks

Files: `requesting.go`, `peerconn.go`. Uses plan 04's short-window rate.

| # | Change | Now | Evidence |
|---|---|---|---|
| C3 | Steal by delivery time, not by queue length | `applyRequestState`: "don't steal from the poor" compares request **counts** (`diff` test) | 26 chunks of piece 0 stayed on a 13 KiB/s peer while a 576 KiB/s peer was connected: the fast peer had more requests, so it could never steal |
| C4 | Slow peers do not take new chunks of `Now` pieces when a fast peer has the piece | `getDesiredRequestState` gives every peer the same piece order | Same |

(C1, C2 and C5 moved to plan 04; numbering kept so older notes still match.)

Design (keep the numbers as `ClientConfig` fields with these defaults):
- **C3.** Estimated delivery of a request on peer `q` = `(requests ahead of it on q + 1) × chunk ÷ rate(q)`. Peer `p` may steal a request for a `Now` or `Readahead` piece when its own estimate is less than **half** the holder's and at least 1 s sooner, and `StealRequestGrace` has passed. For other priorities keep today's rule. The factor of two is what stops two similar peers from trading requests (the cancel storm in `findings/09` §3e).
- **C4.** When collecting candidates for `p`, skip unrequested chunks of a `Now` piece if another unchoked peer that has the piece has a short-window rate above `4 × rate(p)` and room in its queue. A peer with unknown rate counts as slow for this test only while such a proven peer exists; it still gets a deep queue on the next pieces, which is how it proves itself.

Tests: the fork has request-strategy tests in the root package in `qt` style. Add table tests with fake peers for: steal accepted at 2× and refused at 1.2×, slow-peer exclusion on and off, offset-first chunk order. Build test binaries to the fixed path in `STATUS.md`.

## Checked against the code (2026-11-16)
These were assumptions when the plan was first written; each was then read in source.

| Assumption | Result | Where |
|---|---|---|
| `Next` is a usable level between `Readahead` and `Now` | Yes. Pieces are ordered by numeric priority, then partial-first, then lower availability, then index. A piece's priority is the highest of its files' priority, the reader sets, and its own explicit priority | `internal/request-strategy/order.go:23–40`, `piece.go:381–396` |
| A responsive read returns as chunks arrive | Yes, with two conditions. It returns the **contiguous** run of received chunks from the read offset (so B1's offset-first order is required for it to help), and it is woken at chunk receipt (`unpendChunkIndex` broadcasts the reader condition). The read then waits until no write is in flight **anywhere in that piece** | `reader.go:122–145, 232–265`, `piece.go:175–179`, `storage.go:53–56`, `piece.go:233` |
| So is B2's range-scoped wait needed? | Probably not for correctness: the wait ends whenever the piece's in-flight write count touches zero, which happens between write batches. With two or three peers writing the same piece it may be delayed. Measure the wait in plan 02's stall log before changing it | same |
| A reader holds `Now` only while it is reading | Yes: a reader contributes priority after its first `Read`, and loses it on `Seek` until the next `Read` | `reader.go:161, 195, 470` |
| The head reader goes away when mpv jumps to the tail | Yes if mpv closes that connection: a disconnect watcher closes the reader at once. ffmpeg's HTTP seek opens the new connection and then closes the old one, so there is a brief overlap, not a gap. Confirm with the access log | `engine.go:1812–1819` |
| Helper endpoints are separate readers | No: they come through the stream handler like a player (see A2) | `api.go:958` |
| Closing the last reader is harmless | No: see A5 | `engine.go:1846–1868` |
| The first read of every reader has a 90 s limit | Yes (`startupReadTimeout`). On a thin swarm a tail read that starts late can hit it and mpv gets an error. After A1/B1 the tail needs seconds; if the stall log still shows waits near 90 s, raise it for readers on a file that already delivered bytes | `engine.go:1771–1777` |

## Acceptance (relative, so swarm noise does not decide it)
One play with plan 02's capture, cache removed. Let `T` be the time the first peer whose short-window rate exceeds 200 KiB/s was unchoked, and `R` its average rate over the next 20 s. Pass when all hold:
1. In `[T, T+10 s]`, at least 90 % of that peer's requests are inside the byte ranges a stream request was blocked on, or the head readahead.
2. Its request limit reaches `R × 3 s ÷ 16 KiB` within 3 s of `T` (plan 04's acceptance; recheck here).
3. No chunk of a `Now` piece is outstanding for more than 2 s on a peer more than 4× slower than the fastest unchoked peer that has that piece.
4. Cancelled requests stay under 5 % of outstanding on every peer.
5. First frame ≤ `T + 1 MiB ÷ R + 3 s` (mpv reads under 1 MiB before its first frame, `findings/10` §A; check against the play's access log).
6. No helper endpoint appears in the reader stall log, and no piece of the next episode is requested in the first 60 s.

If the swarm gives no such peer in 120 s, the run says nothing: repeat later, do not conclude.

## Out of scope
Transport (plan 03), request queue sizing (plan 04), discovery and preload (plan 06), the deadline scheduler (plan 09).
