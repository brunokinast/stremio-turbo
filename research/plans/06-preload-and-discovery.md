# Plan 06 — Preload and discovery: pay the waiting before Play (step 6)

> **6a done 2026-11-18** (`findings/16`):
> - Measured the app's real path (preload → race → player) with an emulation:
>
>   | Path | Play → first frame |
>   |---|---|
>   | Click at once | 13.7 s (empty DHT), 8.8 s (warm DHT) |
>   | 15 s on the page | **0.4 s** |
>
> - **Part B3 (warm DHT) is built:** metadata 6.3 → 3.9 s.
> - **Part A** (does preload work?) is answered: yes, after a dwell.
> - **Next, 6b** (findings/16's table):
>   - race reads keep the pins;
>   - the race decides at 1 MiB when it is alone;
>   - after a dwell, choose by rate;
>   - fix the doubled `tracker:` prefix.
> - Part C (continue-watching preload): **done as 6g** (`findings/22`), with the scope the user chose (top board item, setting on by default).
>
> **6b done 2026-11-18** (`findings/17`; Rivulet `5206e8c`, stremio-web `5c84d8f2e` on `wip/streaming-research`):
> - **Built:** probe reads keep the pins; the race wins alone at 1 MiB; a dwell chooses by unchoked peers; tracker prefix fixed.
> - **Dwell:** 0.2 s emulated, 0.74 s in the real app.
> - **Click at once:** first byte → frame 3.9 → 2.0 s, but Play → frame 9.1 s; discovery dominates.
> - **Next candidates:**
>   - preload when no quality is remembered;
>   - preload the resume position;
>   - B2 and B5.
>
> **6c done 2026-11-18** (`findings/18`; Rivulet `0e28e7c`, stremio-web `cb1abf036`):
> - **Resume after a dwell:** 6.0 → 0.9 s (`startFraction` primes the resume point).
> - **Fallback tier:** same resolution as the last quality, else the first at 4K or below, with a toast saying so.
> - **B1 measured:** addresses arrive in 0.2–0.4 s, but connecting takes 2–6.5 s (likely the 4 s handshake timeout on the obfuscated attempt) and the half-open limit of 25 stalls dialling. B2 is not the lever.
> - **Next, 6d:** measure the handshake phases, then fix the connect latency and the half-open limit.
>
> **6d done 2026-11-18** (`findings/19`; anacrolix `7f1157f2`, Rivulet `ac45a75`):
> - **Cause:** not the handshake but anacrolix's client-wide `DialRateLimiter` (10/s). The first 64 attempts waited 3.1 s at the median.
> - **Fix:** 30/s (libtorrent's default) → median wait 0.
> - **Result:** click-at-once Play → first frame 12.6 → **4.6 s**; winner's first byte 6.6 → 2.6 s.
> - **Next:** a bitrate-aware race decision (a 31 GB remux won a race and stalled), then Part C.

> **6e done 2026-11-18** (`findings/20`; stremio-web `8c2c714b7`, Rivulet `2cc06e5`): the highest-bitrate source whose rate covers its bitrate plays; a toast names the larger source passed over. Dwell 0.2 s, no stalls; real app chose the 11.9 GB source over the 36.9 GB remux. Open: a 2 s stall just after the first frame on click-at-once starts.
>
> **6f done 2026-11-18** (`findings/21`; stremio-web `7b990b332`): a smart-play source under 2x its bitrate is held on its first frame until 4 s is buffered (cap 20 s), with "Buffering 4 s before playing" under the spinner. It removes the click-at-once ramp stall; dwell starts are unchanged.

Goal: by the time the user presses Play, the peers that hold the episode are connected and ramped, and the first bytes are on disk. In the measured run 28 s passed before any usable peer and ~20 s more before it was at speed (`findings/09` §2). Engine fixes cannot remove that; starting earlier can.

Prerequisite: plan 02's capture (to measure), plan 05 Part A (so a preload does not spend the peer on tail pieces).

## Part A — Try the smart-play preload that already exists (no new code)
State: `stremio-web\src\smartPlay\` is uncommitted and has never run in the real app. `useSmartPlay.ts:56–75` calls `preload()` (`serverApi.ts:121`, a `POST {infoHash}/create` with `fileIdx` and trackers) for the likely sources once the stream list settles; Rivulet's `PrimeFile` then starts the head.

1. `pnpm run lint`, `pnpm run build`; start through `scripts\start-stremio.ps1`.
2. Open the reference episode's page, wait a fixed 15 s, press Play (4K HDR, muted). Capture with plan 02. Repeat once with preload disabled (a temporary local flag; do not commit it).
3. Report: seconds from Play to first frame in both, and from the timeline what was already done at Play (peers connected, unchoked, bytes on disk).
4. Check the engine survives the wait: `purgeReaderless` (`engine.go:1023, 1232, 1281`) drops torrents without readers after a grace window. If a preloaded torrent can be purged before a plausible Play (say within 3 minutes of the page being open), give preloaded engines their own, longer window and have the web client release them when the page closes (`remove` exists in `serverApi.ts`).
5. Count the cost: bytes downloaded per preloaded source that was never played. If several sources are preloaded, cap each at the head window until one is chosen.

Done when the two numbers are in `findings/13-preload.md` and the user has seen them. If preload works, commit the smart-play work per the repo rules; if it misbehaves, fix that first.

## Part B — Find the right peers sooner
Evidence: both productive peers were **incoming**; trackers returned only two near-useless seeds to Rivulet, while qBittorrent's flags show it got the fast peer without DHT or PEX (so from a tracker). The test instances also started with an empty DHT table every time.

Do these in order; each is small and separately measurable with the timeline's "first known / first connected per source" rows.

| # | Change | Check first |
|---|---|---|
| B1 | For each peer that delivered > 1 MB, record when and from which source we first knew its address, when we dialled, and the result | This is a report from plan 02 data plus `/debug/dials`; it tells which of B2–B5 matters |
| B2 | Announce to every tracker at once on `/create`, all tiers, and compare the tracker list Rivulet uses with what Torrentio supplies (`sources`) | Tracker cap is 25 in the uncommitted work; confirm no tracker is dropped or delayed |
| B3 | Keep the DHT warm across restarts: save the routing table's good nodes on shutdown and every few minutes, load them as starting nodes | `applyDHTBootstrap` (`engine.go:2667–2694`) only adds configured bootstrap nodes; I found no node persistence by grep — verify, then add it using the dht package's own node-file helpers |
| B4 | Stable listen port: choose once, store it, reuse it (`BT_LISTEN_PORT=0` picks a new one every start) | A stable port keeps the UPnP mapping and lets peers that learned our address before a restart still reach us |
| B5 | Connect to every reachable peer quickly, including ones that will not feed us | Peer exchange is how leechers learned our address within a minute; each connected peer advertises us to its own peers. Wire `btMaxConnections` (stored but unused, `findings/06` §6) and raise the dial rate; anacrolix defaults are 25 half-open, 20 s dial timeout |

Acceptance for Part B: in a cold play, the first peer that later delivers > 1 MB is known within 10 s and unchoked within 15 s of `/create`; state the before number from plan 02 next to it.

Note for B3: the launcher force-stops the server when the Stremio window closes (`stremio-web/scripts/start-stremio.ps1:43`, `Stop-Process -Force`), so nothing can be saved "on shutdown". Save the DHT nodes periodically.

## Part C — Start before the user even opens the page (design first, then ask the user)
The commonest play is "continue watching" from the board, which never shows the stream list, so Part A gives it nothing.

1. Read how the board resolves the stream for a continue-watching item (stremio-core's stored last stream and binge group) and whether the web client has the info hash before the player opens.
2. Proposal to put to the user, with numbers from Part A: at app start, preload the head of the top one or two continue-watching items (≤ 32 MiB each); during playback, when position passes 80 %, preload the next episode's likely source. For season packs the torrent is already open, so this is plan 05's A3 rule; for single-episode torrents it is a new `/create`.
3. Only build after the user picks the scope (this downloads without an explicit click, so it needs their OK and a setting).

## Out of scope
Choosing between sources by measured supply (plan 09).
