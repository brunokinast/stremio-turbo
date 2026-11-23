# 46. Velopack instead of Inno Setup: measured, not adopted (yet)

2026-11-23, plan 21. Today the app updates by downloading the whole Inno Setup installer (~69 MB) through a descriptor signed with our ed25519 key (`shell/src/stremio_app/updater.rs`, upstream Stremio code). [Velopack](https://docs.velopack.io) is an installer and update framework with a Rust crate and delta updates from GitHub Releases. Question: what would it save, and what would it cost?

## How it was measured
- **Two real app stages from `mise run build`, about one release apart:** A at 0e5952bde, B at 4fdd54b05 (a web upstream merge, a rebuilt engine and shell). Each is 249 MB in 105 files.
- **Both packed with `vpk pack` 1.2.161** (`--packId StremioTurbo --mainExe stremio-turbo.exe`) into one output folder, so the second pack also builds a delta.

## Results

| | Inno Setup (today) | Velopack |
|---|---:|---:|
| First install download | 69.1 MB | 105 MB (`Setup.exe`), or a 97.9 MB full package |
| **Update download, A → B** | 69.1 MB (the whole installer) | **12.7 MB** (delta) |
| Pack time | ~10 s (ISCC) | 9 s full, 12 s with the delta |

- **What changed between A and B:** 17 files, 69.5 MB uncompressed, mostly the engine (24.1 MB) and the web bundle.
- **How the delta handles them:** it patched 6 files, kept 89, and saw 13 removed and 13 new. The web bundle sits in a folder named after the commit (`web/<sha>/`), so every release renames all its files, and a delta can't patch a renamed file.
- **The delta is 82% smaller per update.** Inno's solid LZMA compression keeps the first install about a third smaller than Velopack's.

## What adopting it would cost
- **The shell would diverge further from upstream.** The updater thread in `app.rs` and all of `updater.rs` are upstream Stremio code that our forks keep close. Velopack replaces them: `VelopackApp::build().run()` at startup and an `UpdateManager` with a GitHub source. Upstream merges into `app.rs` would conflict more often.
- **Signing gets weaker.** Our updater accepts only a descriptor signed with the key in `shell/update-key.pub`, so a compromised GitHub account alone can't push an update. Velopack's feed (`releases.win.json`) is not signed: its signing covers Authenticode of the binaries, and we have no certificate (SignPath's free signing is blocked until `shell/` states a licence). We would lose the update signature or have to bolt our own on.
- **Existing installs have to migrate.** They live in `%LOCALAPPDATA%\Programs\Stremio Turbo` (Inno); Velopack installs to `%LOCALAPPDATA%\StremioTurbo`. One last Inno update would have to run Velopack's setup and remove itself. Settings and the engine's data live outside the install folder, so they would survive.
- **The first install is larger:** 105 MB against 69 MB.

## Recommendation
- **Not now.** The gain, 57 MB less per update, is real but modest for an app whose own streams move gigabytes. The costs land on the two things this repo guards most: easy upstream merges and the signed update path.
- **Revisit** if update size starts to matter (for example metered users), or once there is a code-signing certificate.
- **Cheaper wins found while measuring:**
  - **The installer ships web source maps:** 6 `.map` files, 16.9 MB of the 45.8 MB web folder, that the app never uses.
  - **`web/<sha>/` renames every web file each release.** It only matters for a delta updater, so it can wait for Velopack, if ever.

Sources: [Velopack docs](https://docs.velopack.io/distributing/overview), [update sources](https://docs.velopack.io/integrating/update-sources), [code signing](https://docs.velopack.io/packaging/signing), [velopack crate](https://docs.rs/crate/velopack/latest).
