# 44. Mid-file starts: urgent chunks queued behind older requests

2026-11-22. Found while capturing the README's resume GIF: resuming Oppenheimer (4K DV, 18.3 GB, 16 MiB pieces) from Continue Watching took 23.6 s to its first frame, with nothing on disk.

## What the trace showed
- The download wasn't the limit: 7–20 MB/s from the fourth second.
- The player's reads after the seek were. mpv reads the head, the tail, then the resume point, about 68 MB before the first frame. 20 s of the start went to reader waits, 14 s of it on 11 single chunks.
- New diagnostics (`ChunkWaitState.AheadLater`, `AheadOtherPieces`, in the trace's `readerWaits` as `aheadLater` and `aheadOtherPieces`) showed what those chunks waited behind.

There were two causes.

1. **Older, less urgent requests ahead in the holder's queue.**
   - Waits sat behind 75, 141 and 230 requests in other pieces, all earlier in the file (`aheadLater` 0). These were left from the head reads and from the kept file downloading whole.
   - Peers serve requests in the order they get them, so an urgent chunk requested later waits behind all of them: an estimated 10 s on one peer.
   - The critical steal (findings/15) rarely helps here. Every peer's queue is sized to its own rate, so a new request on any other peer is about as far back as on the holder, and the "2× sooner" rule doesn't fire.
2. **A silent holder keeps its requests.** One peer delivered nothing for 11 s while holding about 40 requests. The reader waited on its chunks five times, 0.4–1.9 s each. Overdue chunks move one at a time, as the reader reaches them, and the snub that frees everything comes only after `PieceTimeout` (20 s).

## Change (`deps/torrent/request-critical.go`, `clearWayForCritical`)
When a read blocks on a chunk worth a replan (the existing `readerWaitMayNeedReplan`, or when its request turns overdue):
- **Older requests are cancelled.** The holder's requests ahead of the chunk, in pieces of lower priority, are cancelled. The chunk moves up, since a peer drops cancelled requests it hasn't sent, and the cancelled ones return to the pool.
- **A silent holder is snubbed.** A holder that has delivered nothing useful for 2 s, with the chunk requested at least 2 s ago, is snubbed at once: all its requests move, not only this one.

## Results
Cold resume of the same title, its data deleted before each run, measured from click to first frame:

| Run | Before | After |
|---|---:|---:|
| First trace, weaker swarm | 23.6 s, 20.5 s | — |
| The same hour, back to back | 11.1, 11.3, 9.0 s (median 11.1) | 9.3, 7.4, 7.2 s (median 7.4) |
| Waits behind older requests | 4.0–6.0 s per start | 2.5–2.7 s |

Waits behind older requests remain because the first wait on each chunk comes before its cancels take effect (a round trip).

## Next
- Bench a fresh 4K HDR episode from the start (research/lab/bench) before the next release, to confirm starts from the beginning are unchanged.
- Continue Watching prepares only its first card, so any other card resumes cold. Preparing the top few is worth measuring against the data it costs.
