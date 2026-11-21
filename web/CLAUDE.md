# Project directives

Personal fork of [Stremio/stremio-web](https://github.com/Stremio/stremio-web) with changes that make Stremio work better for its owner. It runs inside the official Windows shell (`stremio-shell-ng.exe --webui-url=<our build>`). It is not a product: keep things lean. Claude writes and checks all the code here and is responsible for it.

## Principles

- **Proper implementations.** No hacks, workarounds or half-fixes. Fix root causes. If doing it right means changing an existing system, change it.
- **Build on what exists.** Look at how the codebase already does things (core models and actions, `src/common` hooks, components, routes, i18n, styles) and reuse them instead of rebuilding.
- **Keep it simple.** Solve what's needed now; no over-engineering or production polish.
- **Keep upstream updates easy.** Prefer self-contained additions with small touch points in upstream files, so merging Stremio's `development` stays painless.
- **Own the code we change.** When a fix belongs in another component (the streaming server, core, shell), fork it and change its source properly, then merge its upstream ourselves when we choose to. No patch scripts, runtime monkey-patching or env-var workarounds standing in for a real change. Closed-source parts we can't own get replaced by open alternatives (that's why the server is our Rivulet fork).
- **Explore the whole flow, proactively.** Before fixing, measure what actually happens and trace it end to end across every piece involved (addon → web client → core → server → network). Question existing limits by finding out why they exist. Look for flow-level improvements beyond the direct fix: reuse information already gathered, cache it, do work early (before the user acts), pick better defaults. Bring these up unasked, with numbers.
- **Research before proposing.** Search the relevant code, docs and the web for existing options and how others solve the problem, then present the options with trade-offs.
- **Ask when unsure** about what the user wants.

## Conventions

- pnpm only. TypeScript for new files; existing files keep their style. Avoid new dependencies unless clearly worth it.
- User-facing text goes through the existing i18n setup, using the fork's own English strings.
- Repo uses LF line endings (`core.autocrlf=false`).

## Workflow

- Discuss and explore first, then write a plan; one part at a time, each with its own plan. For UI choices, present a few options (with previews) rather than picking one.
- Commit straight to `main` and push to `origin` (brunokinast/stremio-web) once a change works. Never push to Stremio's repositories or force-push.
- Before committing: `pnpm run lint` and `pnpm run build` pass, and the change has been tried in the real app.
- Pull upstream with `git fetch upstream && git merge upstream/development`.

## Testing

- The user watches **4K HDR**: test and judge playback changes against 4K HDR sources, not 1080p. Healthy lower qualities prove little.
- Reference test: **Peaky Blinders S06E01, 4K HDR** (`tt2442560:6:1`), the episode the user is watching. Rivulet keeps downloaded data on disk, so note when a source is already partly cached.
- Test playback **muted**, and **stop it** (pause and leave the player) as soon as the test is done, so it doesn't disturb the user.
- Restarting Stremio for testing (e.g. to install a new Rivulet build) is fine; no need to ask.

## The user's Stremio setup

- `scripts/start-stremio.ps1` starts our Rivulet build on `127.0.0.1:11470` and the official shell with `--development --webui-url=<fork>` (so the shell doesn't start Stremio's closed `server.js`), and stops Rivulet when the shell exits.
- Streaming server = our fork of Rivulet Engine (open-source `server.js` replacement, Go): github.com/brunokinast/rivulet-engine, cloned at `C:\Users\bruno\Documents\git\rivulet-engine`, `upstream` = rivulet-kodi/rivulet-engine. Server changes are made there, following its `AGENTS.md`.
- Back up before changing the user's account (add-ons, library) or server settings, and only do it when asked.
- Never commit or log auth keys or the server IPC key.
