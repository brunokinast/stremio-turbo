# 27. Download ahead in the web app (step 8c), and two anacrolix fixes found on the way

2026-11-18. stremio-web `90392412d`; anacrolix fork `9dfd8da2` (branch `wip/streaming-research`); Rivulet rebuilt and installed.

## The rule (the user's)
- A source fits when `rate × 0.8 × (time left of the current episode + the next episode's runtime) ≥ size`. At constant rates the closest point is the next episode's end, so the download may keep going while it plays.
- **Choice:** the largest source that fits in the remembered quality, else each lower resolution in turn.
- **Margin:** 0.8 of the measured rate (proposed; the user didn't object).
- The emulator mirrors it (`lab/player/smartplay_emul.py` `fits_window` / `choose_ahead`), with tests including the user's HotD example.

## When and what
- **When:** after 60 s of steady playback, then every 5 min while playing, and on leaving an episode played ≥ 10 s.
- **How many:** up to 2 episodes ahead, one at a time.
- **Sources:** the next episode's streams are fetched from the installed stream add-ons, the same requests core makes. Torrentio's raw `title` and `sources` are mapped to core's `description` and `announce` names; without that, sizes were missing and nothing qualified.
- **Measuring:** the top 3 unmeasured candidates are opened for 30 s, primed at **mid-file** and with a **baseline sample** taken first:
  - an already-primed head downloaded nothing;
  - a fast swarm finished within the first poll.
- **Rate memory can only rule a source out.** A rate remembered from a read of data already on disk (133 MB/s for the pack) qualified anything.
- **Adaptive check:** after 4 min, a download whose actual average rate since it was queued can't finish in time is deleted and replaced, with a toast.
  - **Why:** a 30 s probe can be far off either way. The HotD remux probed 19.9 MB/s and then downloaded at 1.7 MB/s; the pack probed 0.7 MB/s and later downloaded at 44.6 MB/s.
- **Toasts** for every decision: queued, queued in a lower resolution, already on disk, nothing fits, budget full, replaced. The last decision's inputs are kept in `sessionStorage['smartPlay.aheadTrace']`.
- **Playback:** an episode with a downloaded copy plays it directly ("Playing the downloaded copy"), with no race.
- **Watched:** past 90% of an episode, its download is marked watched.
- **Setting:** Settings → Streaming → "Download next episodes ahead" (on).

## Real-app runs (HotD, muted)
- **A:** found two bugs:
  - the 60 s trigger never fired: the player reports time in uneven steps, so it now counts wall time while the player's time advances;
  - sizes were missing, because the raw `title` field wasn't mapped.
- **A2:** S02E04 was queued from the pack (9.4 GB, already on disk).
- **B:** S02E04 played its downloaded copy at once. At 60 s, S02E05 was queued: the 33.6 GB remux, on a 19.9 MB/s probe.
- **C:** the check found the remux at about 4 MB/s average and replaced it.
- **D/E:** with the measurement fixes, the pack's S02E05 measured 0.7 MB/s at that moment. The 1.7 GB 4K DV file (3.2 MB/s) was chosen and finished in about 2 min.

## anacrolix fixes (`9dfd8da2`)
1. **Panic "too many outstanding requests"** killed Rivulet mid-run.
   - **Cause:** `applyRequestState` checked the peer's request limit and then `request()` recomputed it. With the rate-sized queue, `nominal()` depends on `time.Now()`, since its short-window rate decays every second, so the recomputed limit could be smaller and `mustRequest` panicked.
   - **Fix:** the limit is computed once per pass.
2. **Completed pieces were never checkpointed:** 468 "error checkpointing completed pieces: access denied" in one evening.
   - **Cause:** a promoted part file is read-only, and `fsync` opened it write-only. Every checkpoint touching it, such as a boundary piece shared with the next episode completing later, failed and was retried forever.
   - **Fix:** `fsync` succeeds on a read-only file, which has nothing pending (it was flushed before promotion). Test: `TestFsyncReadOnlyFile`.

## Open
- **Season packs are probed one file at a time.** Probing a pack's file measures the swarm, but the playing torrent's own rate isn't used.
- **The probe is a hint, not a promise:** the 4-minute check is what keeps choices honest.
