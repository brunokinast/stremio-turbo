# Project directives

Personal fork of [Stremio/stremio-web](https://github.com/Stremio/stremio-web) with changes that make Stremio work better for its owner. It runs inside the official Windows shell (`stremio-shell-ng.exe --webui-url=<our build>`). It is not a product: keep things lean. Claude writes and checks all the code here and is responsible for it.

## Principles

- **Proper implementations.** No hacks, workarounds or half-fixes. Fix root causes. If doing it right means changing an existing system, change it.
- **Build on what exists.** Look at how the codebase already does things (core models and actions, `src/common` hooks, components, routes, i18n, styles) and reuse them instead of rebuilding.
- **Keep it simple.** Solve what's needed now; no over-engineering or production polish.
- **Keep upstream updates easy.** Prefer self-contained additions with small touch points in upstream files, so merging Stremio's `development` stays painless.
- **Ask when unsure** about what the user wants.

## Conventions

- pnpm only. TypeScript for new files; existing files keep their style. Avoid new dependencies unless clearly worth it.
- User-facing text goes through the existing i18n setup, using the fork's own English strings.
- Repo uses LF line endings (`core.autocrlf=false`).

## Workflow

- Commit straight to `main` and push to `origin` (brunokinast/stremio-web) once a change works. Never push to Stremio's repositories or force-push.
- Before committing: `pnpm run lint` and `pnpm run build` pass, and the change has been tried in the real app.
- Pull upstream with `git fetch upstream && git merge upstream/development`.

## The user's Stremio setup

- `server.js` in `%LOCALAPPDATA%\Programs\Stremio` is closed source and replaced on every update. Any server fix goes in a re-runnable patch script, never a hand edit.
- Back up before changing the user's account (add-ons, library) or server settings, and only do it when asked.
- Never commit or log auth keys or the server IPC key.
