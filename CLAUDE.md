# Stremio Turbo: project directives

One repository holding the owner's Stremio: `web/` (stremio-web fork), `server/` (Rivulet Engine fork, the streaming server), `shell/` (stremio-shell-ng fork, the Windows app), `deps/` (anacrolix torrent and go-utp forks), `research/` (findings and plans; start at `research/plans/00-START-HERE.md`). Each folder keeps its own `CLAUDE.md`/`AGENTS.md` for project-specific rules; this file holds what applies everywhere.

## Principles
- **Proper implementations.** Fix root causes where they belong, in whichever folder that is. No patch scripts, runtime monkey-patching or env-var workarounds standing in for a real change.
- **Keep upstream merges easy.** Self-contained additions with small touch points in upstream files. `shell/` files keep upstream's line endings.
- **Explore the whole flow, proactively.** Measure first, trace end to end (addon → web → core → server → swarm → player), question limits by finding out why they exist, and bring flow-level improvements unasked, with numbers.
- **Research before proposing**, present options with trade-offs, ask when unsure. Discuss, then plan, then build, one part at a time. For UI choices, show a few options with previews.

## Workflow
- Commit straight to `main` (LF line endings, `core.autocrlf=false`). Push to `origin` (brunokinast/stremio-turbo) once a change works. Never push to the original projects or force-push.
- Before committing, the touched folder's checks pass: web `corepack pnpm run lint`, `pnpm test`, `pnpm run build`; Go `gofmt -s -l`, `go vet`, `go test -p 1` with `GOWORK=off` (the owner's `GOFLAGS=-exec=…goexec.exe` runs test binaries from a firewall-allowed slot; never run binaries from other paths); shell `cargo test`. `Invoke-AllTests` in `scripts/Upstreams.psm1` runs them all.
- Original projects' updates: `scripts/sync-upstreams.ps1`. Releases: `scripts/release.ps1` (refuses while any upstream is unmerged), then the Release workflow publishes; installed apps update themselves.
- User-facing text in the web UI goes through `web/src/smartPlay/i18n.en.json`.
- README images come from `node scripts/readme-images.mjs` (bench, capture, compose): rerun it after UI changes or a new benchmark.

## Testing
- The owner watches **4K HDR**: judge playback changes against 4K HDR sources. Test **muted** and **stop** playback when done.
- Restarting the app for tests is fine. Back up before changing the owner's Stremio account or server settings, and only when asked.
- Never print, commit or log the Stremio auth key, the server IPC key or the update signing key (`~/.stremio-turbo/update-signing-key.pem`, Actions secret `UPDATE_SIGNING_KEY`). Bind services to 127.0.0.1. `research/data/` stays local.
