# Plan 08 — Download ahead: choices for the user, then a build plan

Status: **decided 2026-11-18; building 8a–8e.** The approved plan is in `08-approved.md` (it supersedes the build order below). Decisions:
- automatic next episodes;
- the server runs until downloads finish and keeps the PC awake (not the screen) meanwhile;
- markers on the episode list;
- a 60 GB budget, watched episodes deleted first, uploads at the existing limit.

The source rule is the user's: the best source whose rate ≥ size ÷ (time left in the current episode + the next episode's runtime). The downloaded copy plays directly.

**Manual downloads are deliberately not planned:** the user will elaborate them after reviewing plans 1–9.

## Why this is the cure
- The reference episode is 6.69 GB for about 58 minutes: it needs ~1.9 MB/s. The swarm gave 0.5–2 MB/s from two or three peers. When supply is at or under the bitrate, no request scheduler can prevent pauses; only starting earlier can.
- The same swarm is ample over time. One episode a day is 6.7 GB per day, an average of 78 KB/s. At a poor 0.5 MB/s an episode takes 3.7 hours; at 1 MB/s, 1.9 hours.
- So: download when nobody is watching, play from disk. A fully downloaded episode starts instantly and cannot buffer.

Facts about this machine (checked 2026-11-16): cache limit is set to 150 GiB (`cacheSize` in `server-settings.json`), 367 GB free on the disk, cache currently 29.5 GB. The launcher (`stremio-web\scripts\start-stremio.ps1:42–43`) force-stops Rivulet the moment the Stremio window closes.

## Decision 1 — What gets downloaded ahead
| Option | What it does | Cost |
|---|---|---|
| **A. Next episode, automatically** (recommended) | When you stop or finish an episode, the rest of it and the next one or two episodes of that series download in the background | Uses disk and bandwidth without a click; needs a setting to turn off |
| B. Only what you ask for | A "Download" action on an episode or season | Nothing happens unless you plan ahead |
| C. Both | A as the default, B for a trip or a whole season | Most UI work |

Recommendation: C, built as A first (it covers the daily case with no UI beyond a setting), B second.

## Decision 2 — When the server may run
| Option | What it does | Cost |
|---|---|---|
| A. Only while Stremio is open | No change to how things start | Downloads stop when you close the window; a 4-hour download rarely finishes |
| **B. Keep running until pending downloads finish** (recommended) | Closing Stremio leaves the server running quietly; it exits when the queue is empty | Launcher change; a way to see and stop it (see Decision 4) |
| C. Always running from Windows start-up | Server is a background task | Also keeps peer discovery warm, so every start is faster; uses a little memory and upload all the time |

Recommendation: B now; C is an easy step later if you like the result.

## Decision 3 — Disk budget and what is deleted first
Suggested default: 60 GB for ahead-of-time downloads inside the existing 150 GiB cache (about 8 episodes of this size). Deletion order: watched episodes oldest first; then unwatched automatic downloads oldest first; never something you asked for by hand, until you remove it. Tell me a different number or rule if you prefer.

## Decision 4 — What you see
Three levels, pick one to start with.

Option 1 — a marker on the episode list (smallest):
```
S06E01  Black Day            ● watched
S06E02  Black Shirt          ⬇ on disk
S06E03  Gold                 ⬇ 41 %  · 1.2 MB/s · about 55 min
S06E04  Sapphire
```

Option 2 — marker plus a Downloads page:
```
Downloads                                   38.2 GB of 60 GB
──────────────────────────────────────────────────────────────
Peaky Blinders  S06E02   on disk     6.7 GB    [Remove]
Peaky Blinders  S06E03   41 %        1.2 MB/s  [Pause] [Remove]
──────────────────────────────────────────────────────────────
Automatic: next 2 episodes of what I watch   [on]
Keep downloading after I close Stremio       [on]
```

Option 3 — Option 2 plus a tray icon while the server runs with Stremio closed (shows progress, "stop now").

Recommendation: Option 1 with the two settings in the existing Settings page first; Option 2 when manual downloads (Decision 1 B) are built. For Decision 2 B without a tray icon, the server simply exits when done; Option 3 only if you want to see or stop it meanwhile.

## One more thing you should decide: uploading
A downloaded episode stays on disk, so the server will also upload it to others while it runs. On small swarms that is what earns download speed from other leechers (all our useful peers were leechers). If you want a cap or none at all, say so; the default would be the existing upload limit setting.

## Build order once decided (for the implementing model)
Prerequisites from other plans: sparse files (`03` A1), so a queued episode does not write 7 GB of zeros first; the next-episode-by-number fix (`05` A3); reader-close grace (`05` A5).

1. **Rivulet: a download intent.** A per-file state "download fully in the background" that survives the last reader closing (today `pinnedReader.Close` sets the file to priority `None`). Background files sit at `Normal`; anything a player reads still outranks them. Persist the queue (JSON in the app folder) so it survives restarts. API: add, list with progress, pause, remove. OpenAPI and tests per `AGENTS.md`.
2. **Rivulet: budget and eviction** per Decision 3, on top of the existing cache accounting.
3. **Web: automatic next episodes** (Decision 1 A). When playback stops or ends, ask the server to queue the remainder of this episode and the next N. In a season pack the next episode is another file of the same torrent; otherwise use the source smart play would pick for that episode (its binge-group logic exists in `src/smartPlay`). A setting, off switch, i18n strings.
4. **Launcher: keep the server alive** per Decision 2. Replace the unconditional stop with: ask the server whether the queue is empty; if not, leave it running with a flag that makes it exit when the queue drains. Starting Stremio again must adopt the running server rather than kill it (today line 21 kills any running instance).
5. **Web: markers** (Decision 4 Option 1): the episode list reads the server's list and shows state. Present two or three visual variants to the user before settling, as their rules ask.
6. Later: manual downloads and the Downloads page; start-up task; tray icon.

## How to judge it
After one evening of normal use: the next episode is fully on disk the next day, starts in under 2 seconds from Play, and shows zero cache pauses in plan 02's stall log. Report bytes downloaded per hour while idle, since that number says how many episodes ahead is realistic on a thin swarm.
