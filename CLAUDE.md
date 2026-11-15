# Project directives

This is a personal fork of [Stremio/stremio-web](https://github.com/Stremio/stremio-web), run inside the official Windows shell (`stremio-shell-ng.exe --webui-url=<our build>`). Claude writes, verifies and maintains all code here, with no human review. That makes Claude fully responsible for its quality. These directives always apply; do not drift from them.

## Engineering principles

- **Proper implementations only.** No workarounds, hacks, half-fixes, or "good enough for now" code. If doing it right means changing an existing system, change the system.
- **Integrate, don't rebuild.** Before writing anything, explore how the codebase already solves related problems (core models and actions, hooks in `src/common`, components, routes, i18n, styles) and build on those systems. Duplicating existing behaviour is a defect.
- **Fix root causes.** Understand why something fails before changing it; never mask symptoms.
- **Architecture and maintainability first.** Clear module boundaries, single responsibility, explicit data flow, no hidden coupling. New code should read as if it belonged upstream.
- **Keep upstream merges easy.** Put our features in self-contained modules with small, deliberate touch points in upstream files. When an upstream system has to change, change it cleanly and record why in `FORK.md`.
- **No speculative code.** Build what the current feature needs, designed so it can be extended later.
- **Ask when unsure.** If a requirement, product decision or trade-off is ambiguous, ask the user instead of guessing. State assumptions explicitly.

## Conventions (inherited from upstream `CONTRIBUTING.md`, adapted)

- pnpm only (`packageManager` in `package.json`); never add npm or yarn lockfiles.
- TypeScript for new files; keep CommonJS in existing JavaScript files.
- Match the surrounding style; reuse existing components, hooks, helpers, path aliases and local LESS modules.
- Avoid new dependencies and new effects when existing code can solve the problem. A new dependency needs a clear justification.
- No hardcoded user-facing text. Our strings go in the fork's own English translation resources, wired into the existing i18next setup (we cannot publish to `stremio-translations`).
- Unlike upstream, add tests for our own non-trivial logic (pure functions, state machines, parsers).
- File headers: upstream files keep theirs. If a test enforces upstream headers on files we create, update the test to accept the fork's header rather than misattributing our code.

## Workflow

- `main` is our trunk. Each piece of work happens on a short-lived branch (`feature/...`, `fix/...`, `chore/...`), merged into `main` once complete and verified.
- Upstream: `upstream` remote = `Stremio/stremio-web`, branch `development`. Merge it into `main` deliberately, resolve conflicts properly, and re-verify everything afterwards.
- Commits: one logical change each, concise subject-only messages in upstream style.
- Commit and push to `origin` (brunokinast/stremio-web) freely once a change is verified. Never push to Stremio's repositories, never force-push `main`, never open upstream PRs without asking.
- Keep `FORK.md` current: every divergence from upstream (features, changed systems, server patches) with a one-line reason.

## Verification (required before every commit)

1. `pnpm run lint`, `pnpm test`, `pnpm run build`, `git diff --check`, all clean. Do not raise lint limits to pass.
2. Run the build in the real app (the installed shell via `--webui-url`) and exercise the change end to end, including edge cases and failure paths.
3. Re-read the full diff as a reviewer would: correctness, integration with existing systems, naming, dead code, leftovers.
4. Only call something done when it actually is. Report failures and anything unverified plainly.

## The user's live Stremio setup

- The streaming server (`%LOCALAPPDATA%\Programs\Stremio\server.js`) is closed source and replaced on every app update. Server fixes go in a separate, idempotent, version-checked patch script that refuses unknown server versions and can be reverted; never hand-edit the installed file.
- Changes to the user's Stremio account (add-ons, library) or server settings: back up first, and do them only when the user asked or agreed.
- Never expose services beyond `127.0.0.1`, and never log or commit auth keys or the server IPC key.
