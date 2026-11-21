# 28. The server finishes downloads after Stremio closes; the PC stays awake (step 8d)

2026-11-18. Rivulet `fb656a5`; stremio-web `cc87353cd` (launcher).

## Built
- **Rivulet:**
  - `POST /downloads/exit-when-done {on}`: the process exits cleanly once no download is left to fetch and no torrent is being read (checked on each 30 s janitor tick).
  - While downloads are being fetched, `SetThreadExecutionState(ES_CONTINUOUS | ES_SYSTEM_REQUIRED)`, held by one goroutine locked to its OS thread (the state is per thread). The display may still turn off and lock; on other platforms this is a no-op.
- **Launcher:**
  - on shell exit with pending downloads, it turns exit-when-done on and leaves Rivulet running;
  - on start, it adopts our running Rivulet (exit-when-done off);
  - each launcher records its PID, so an older launcher woken by a newer one stopping its shell no longer kills the adopted server (seen live during a restart);
  - the previous server logs are kept as `.prev.log`.

## Bugs found live
- **The pending count was always 0, so the server was stopped instead of drained.** `Invoke-RestMethod` passes a JSON array down the pipeline as one object. Fixed by assigning before filtering.
- **The double-launcher kill** (above).

## Live check
- Shell closed with S02E05 at 1.67/1.86 GB. Rivulet kept running (same PID).
- Log: "keeping the system awake…", then "downloads idle; the system may sleep", "downloads done; exiting (exit when done)", "shutting down". It exited 42 s later.
- `powercfg /requests` needs admin, so the OS-level request wasn't inspected.
