# 12. Manual downloads: one episode, or a whole season

2026-11-20. Decided with the user. **Built the same day** (`findings/37`); the approved plan is `~/.claude/plans/playful-yawning-kettle.md`. Changes from this draft: the quality is the row clicked (season: a chooser listing every quality, one click per row, toast with undo), each episode's sources are raced (not seeders only), and the cross-torrent throttle was measured as not needed.

## What the user chose
- **One episode:** a ⬇ on each quality row of the episode's streams page, and on each source in the expanded list. The row (or the source) is the quality choice, so no extra chooser. A specific source downloads that episode only.
- **A season:** a "Download season" button next to the season picker on the episode list. It opens a chooser with each quality's episode count and total size.
- **Season source rule: the pure best per episode.** Each episode takes its highest-seeded source in that quality, even if the season ends up as several releases.
- **Order:** everything downloads at once, but the earliest episode has first claim. A later episode only takes the capacity the earliest one can't use.

## Defaults (not asked; each has a visible notice where it decides for the user)
- No speed test: a download doesn't have to keep up with playback, so seeders decide, not the 1.25× rule.
- **Seeders are live counts** (Rivulet `/scrape`, as the quality rows use), not Torrentio's.
- **An episode without the chosen quality:** the next lower one (the same order as `candidates.ts`), named in the chooser ("E4: 4k instead") and in the confirmation toast.
- **Skipped episodes:** any with a complete copy on disk, or not yet aired. The chooser shows them ("1 already on disk").
- **Space check:** if the selection doesn't fit in the free space with 10 GB to spare, the chooser shows the size against the free space and disables the button.
- **Origin `manual`:** smart play never replaces or deletes these. The Next slot's watch and the switch's delete guard act only on records whose origins are all automatic (`picker`, `play`, `next`). This is already true for the switch; check it for the Next slot's replacement.
- A manual download stays until deleted, watched or not (as today).
- **Stalled:** 0 B/s for 10 min shows "Stalled" with "Try another source" on the Downloads screen. That runs the next-best source of the same quality, skipping that torrent.

## Measured before planning (Torrentio counts, limit=10)
| Show, quality | Packs | Best pack 👤 | Single-episode torrents 👤 | Per-episode best is a pack |
|---|---|---|---|---|
| HotD S2, 4k DV \| HDR | 6 | 121 (87.5 GB) | 16–35 (76 GB) | 8/8 |
| HotD S2, 1080p | 7 | 415 | 114–147 | 8/8 |
| Severance S1, 4k DV \| HDR | 3 | 71 | 0–26 (6 of 9 eps) | 9/9 |
| Severance S1, 4k HDR | 1 | 34 | 0–67 | 1/9 |
| Peaky S6, 4k | 0 | — | 10–13 | 0/6 |
| Breaking Bad S5, 1080p | 10 | 508 | none | 16/16 |

- Every pack also shows up as a source on each episode's own list, so the best per episode usually lands on the same pack for the whole season. It mixes releases only when a single-episode source beats it (Severance 4k HDR).
- Multi-season packs (Peaky S1–6, Breaking Bad S1–5) are fine: only the wanted files are downloaded.
- Script: `lab/addons/season-packs.py <imdb> <season> <episodes>`.

## Step 1. Rivulet: order among kept files
- **Within one torrent (a pack):** the earliest wanted incomplete file is set to High priority, the rest stay Normal. anacrolix asks each peer for higher-priority pieces first, and peers that lack them send later files, which is the "first claim, others use what's left" rule. A playing file's readahead still outranks both.
- **"Earliest":** a new `order` on the download request (the web sends the episode number). Ties go to the oldest request. This is re-applied each `tickDownloads` and when a file completes.
- **Across separate torrents:** there's no built-in priority; each torrent has its own swarm and they only compete for the PC's link.
  - **Measure first:** two 4K episodes from separate torrents at once. Is the sum capped by the link (it would be if each alone reaches the same rate as the sum), and how much does the first lose?
  - **Build only if it loses:** while the first one's rate is still rising, cap the later torrents' connections and request slots, and release them once it stops gaining.
- **Tests:** the High file moves on when the first completes, and the order survives a restart.

## Step 2. Web: one episode (streams page)
- A ⬇ on each quality row (`QualityRows.tsx`) and each source in the expanded list.
- **Click:** the row's best source by live seeders, or that source, is kept with origin `manual` and the episode's meta. A toast names it ("Downloading E3 · 4k DV | HDR · 10.9 GB").
- **States:** the ⬇ becomes the progress state once a record exists for that episode, and the episode list's chip already shows it. An episode already on disk shows no ⬇ (the "On disk" mark is there).

## Step 3. Web: a season (episode list)
- **The button:** "Download season" beside the season picker (`Details/Videos/Videos.tsx`).
- **The chooser:**
  - One row per quality, with episode count, total GB and any step-downs.
  - It fetches every aired episode's streams through the user's add-ons (as `next.ts` `fetchStreams` does; ~0.3 s each, in parallel). It shows sizes at once, and live seeders as `/scrape` reports them (≤ 10 s).
  - Skipped episodes are listed. Confirm queues every episode with `order` = its episode number.
- **Picking:** for each episode, its quality tier by `groupByQuality`, then the highest live seeders. No release preference, by the user's choice.

## Step 4. Downloads screen
- Manual records as today, plus "Stalled" and "Try another source".
- i18n keys in `src/smartPlay/i18n.en.json`.

## Checks (real app, muted, stopped after)
- **One episode:**
  - ⬇ on HotD S2E1 4k DV | HDR → a kept/`manual` record and a toast;
  - the chip on the list;
  - the Downloads row;
  - then deleted.
- **Season:**
  - the chooser on HotD S2 shows 8 episodes per quality with sizes;
  - Peaky S6 4k shows per-episode releases;
  - an episode on disk is listed as skipped.
  - For a live season run, a small 720p season is used so the test doesn't download 80 GB. Then check: E1 is at High priority, E2+ still move, and E2 becomes High once E1 completes. Then delete.
- **Protection:** a manual record of the next episode survives the Next slot's watch.
- Lint, build, Jest, Go tests.

## Open (decide when reached)
- Whether the season button also offers "From episode N onward". The user picked the plain season button for now.
