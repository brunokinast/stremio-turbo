# web/: stremio-web fork

The UI of Stremio Turbo, forked from [Stremio/stremio-web](https://github.com/Stremio/stremio-web). The root `CLAUDE.md` holds the principles, workflow and testing rules; these are the web-specific ones.

- **Build on what exists.** Reuse core models and actions, `src/common` hooks, components, routes, i18n and styles instead of rebuilding them. Our features live in self-contained folders (`src/smartPlay/`, `src/routes/Downloads/`) with small touch points in upstream files.
- pnpm only (`corepack pnpm`). TypeScript for new files; existing files keep their style. Avoid new dependencies unless clearly worth it.
- User-facing text: our English strings in `src/smartPlay/i18n.en.json`, through the existing i18n setup.
- Checks before committing: `corepack pnpm run lint`, `pnpm test`, `pnpm run build`, and the change tried in the real app.
- The installed app (`%LOCALAPPDATA%\Programs\Stremio Turbo`) runs the bundled server, which serves this UI's production build at `http://127.0.0.1:11470/web/`. For development, the root's `scripts/dev.ps1` runs the app against the dev server (`webpack.local.config.js`, a git-excluded local file).
