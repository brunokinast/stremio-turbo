// README images (docs/readme/): rerun after UI changes or a new benchmark.
//
//   node scripts/readme-images.mjs bench <healthy bench dir> <thin bench dir>
//       Summarises two research/lab/bench/compare.py runs into docs/readme/bench.json: medians
//       and ranges only, no infohashes, addresses or release names.
//   node scripts/readme-images.mjs capture
//       Screenshots of the installed app over its DevTools port, into out/readme/ (ignored). Start
//       the app with WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--remote-debugging-port=9222 first.
//   node scripts/readme-images.mjs compose
//       Renders the banner, the download button and the benchmark chart (light and dark) with
//       headless Edge, from bench.json and the captures.
//
// No dependencies: Node 24 (fetch, WebSocket) and the Edge that ships with Windows.
import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const ROOT = path.join(import.meta.dirname, '..');
const DOCS = path.join(ROOT, 'docs', 'readme');
const RAW = path.join(ROOT, 'out', 'readme');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ---- DevTools protocol --------------------------------------------------------------------------

async function cdp(wsUrl) {
    const ws = new WebSocket(wsUrl);
    await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
    let nextId = 0;
    const pending = new Map();
    const listeners = new Set();
    ws.onmessage = (m) => {
        const msg = JSON.parse(m.data);
        if (msg.id && pending.has(msg.id)) {
            const { resolve, reject } = pending.get(msg.id);
            pending.delete(msg.id);
            msg.error ? reject(new Error(`${msg.error.message}`)) : resolve(msg.result);
        } else if (msg.method) {
            for (const l of listeners) l(msg);
        }
    };
    // A call that gets no answer in 30 s fails instead of hanging the run.
    const send = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
        const id = ++nextId;
        const timer = setTimeout(() => { pending.delete(id); reject(new Error(`DevTools ${method} got no answer in 30 s`)); }, 30000);
        pending.set(id, { resolve: (v) => { clearTimeout(timer); resolve(v); }, reject: (e) => { clearTimeout(timer); reject(e); } });
        ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
    });
    return { send, on: (f) => listeners.add(f), close: () => ws.close() };
}

// Headless Edge, its own throwaway profile, DevTools on a loopback port.
async function edge() {
    const exe = ['ProgramFiles(x86)', 'ProgramFiles'].map((v) => process.env[v] && path.join(process.env[v], 'Microsoft', 'Edge', 'Application', 'msedge.exe'))
        .find((p) => p && fs.existsSync(p));
    if (!exe) throw new Error('Microsoft Edge not found');
    const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'readme-edge-'));
    const port = 9333;
    const proc = spawn(exe, ['--headless=new', `--remote-debugging-port=${port}`, '--remote-debugging-address=127.0.0.1',
        `--user-data-dir=${profile}`, '--no-first-run', '--disable-extensions', '--hide-scrollbars', 'about:blank'], { stdio: 'ignore' });
    let ver;
    for (let i = 0; i < 50 && !ver; i++) {
        await sleep(200);
        ver = await fetch(`http://127.0.0.1:${port}/json/version`).then((r) => r.json()).catch(() => null);
    }
    if (!ver) throw new Error('Edge DevTools did not come up');
    const c = await cdp(ver.webSocketDebuggerUrl);
    return {
        // Renders HTML at a CSS size and saves a PNG at 2x, with a transparent background unless
        // the page paints one. A null height fits the page's content.
        async render(html, width, height, out) {
            const { targetId } = await c.send('Target.createTarget', { url: 'about:blank' });
            const { sessionId } = await c.send('Target.attachToTarget', { targetId, flatten: true });
            const s = (m, p) => c.send(m, p, sessionId);
            await s('Page.enable');
            await s('Emulation.setDeviceMetricsOverride', { width, height: height ?? 2000, deviceScaleFactor: 2, mobile: false });
            await s('Emulation.setDefaultBackgroundColorOverride', { color: { r: 0, g: 0, b: 0, a: 0 } });
            const { frameTree } = await s('Page.getFrameTree');
            await s('Page.setDocumentContent', { frameId: frameTree.frame.id, html });
            await s('Runtime.evaluate', { expression: 'document.fonts.ready.then(() => Promise.all([...document.images].map((i) => i.decode().catch(() => {}))))', awaitPromise: true });
            await sleep(150);
            if (height == null) {
                height = Math.ceil((await s('Runtime.evaluate', { expression: 'document.body.getBoundingClientRect().height', returnByValue: true })).result.value);
                await s('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 2, mobile: false });
                await sleep(100);
            }
            const { data } = await s('Page.captureScreenshot', { format: 'png', clip: { x: 0, y: 0, width, height, scale: 1 }, captureBeyondViewport: false });
            fs.writeFileSync(out, Buffer.from(data, 'base64'));
            await c.send('Target.closeTarget', { targetId });
            console.log('wrote', path.relative(ROOT, out));
        },
        async close() {
            c.close();
            proc.kill();
            await sleep(500);
            fs.rmSync(profile, { recursive: true, force: true });
        },
    };
}

// ---- bench: compare.py results -> docs/readme/bench.json ----------------------------------------

function bench(healthyDir, thinDir) {
    const summarise = (dir) => {
        const runs = JSON.parse(fs.readFileSync(path.join(dir, 'results.json'), 'utf8')).runs;
        const out = {};
        for (const name of [...new Set(runs.map((r) => r.client))]) {
            const rs = runs.filter((r) => r.client === name);
            const stat = (k) => {
                const v = rs.map((r) => r[k]).filter((x) => x != null).sort((a, b) => a - b);
                if (!v.length) return null;
                const mid = v.length / 2;
                const median = v.length % 2 ? v[Math.floor(mid)] : (v[mid - 1] + v[mid]) / 2;
                return { median: +median.toFixed(2), min: v[0], max: v.at(-1), n: v.length };
            };
            out[name] = {
                runs: rs.length,
                firstFrameS: stat('firstFrameS'), started: rs.filter((r) => r.firstFrameS != null).length,
                mbps: stat('mbps'), peersMax: stat('peersMax'), stalls: stat('stalls'),
            };
        }
        return out;
    };
    const json = { date: new Date().toISOString().slice(0, 10), secs: 60, healthy: summarise(healthyDir), thin: summarise(thinDir) };
    fs.mkdirSync(DOCS, { recursive: true });
    fs.writeFileSync(path.join(DOCS, 'bench.json'), JSON.stringify(json, null, 1) + '\n');
    console.log(JSON.stringify(json, null, 1));
}

// ---- capture: the installed app over its DevTools port ------------------------------------------

// Hidden before anything is captured: release names (the expanded source lists, the full
// stream list and the Downloads page's file names) are blurred, the account menu never opens.
const PRIVACY_CSS = `[class*="sources"], [class*="streams-list"], [class*="stream-container"], [class^="file-"], [class*=" file-"] { filter: blur(7px) !important; }`;
const FFMPEG = path.join(process.env.LOCALAPPDATA, 'Programs', 'Stremio Turbo', 'ffmpeg.exe');

async function app() {
    const port = process.env.CDP_PORT || 9222;
    const targets = await fetch(`http://127.0.0.1:${port}/json`).then((r) => r.json()).catch(() => null);
    const page = targets?.find((t) => t.type === 'page' && t.url.startsWith('http://127.0.0.1:11470/web/'));
    if (!page) throw new Error(`no app page on DevTools port ${port}: start the app with WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--remote-debugging-port=${port}`);
    const c = await cdp(page.webSocketDebuggerUrl);
    const evaluate = async (expression) => (await c.send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true })).result?.value;
    const privacy = () => evaluate(`(() => { let s = document.getElementById('readme-privacy'); if (!s) { s = document.createElement('style'); s.id = 'readme-privacy'; document.head.append(s); } s.textContent = ${JSON.stringify(PRIVACY_CSS)}; })()`);
    const waitFor = async (expr, secs = 60) => {
        for (let i = 0; i < secs * 4; i++) {
            if (await evaluate(expr)) return true;
            await sleep(250);
        }
        throw new Error('timed out waiting for ' + expr.slice(0, 120));
    };
    const go = async (hash) => {
        await evaluate(`location.hash = ${JSON.stringify(hash)}`);
        await privacy();
    };
    // Mutes the player the moment it can be (its mute button enables with the first frame) and
    // checks it took; throws, leaving the player, if it didn't. Never record with sound.
    const trDir = [ROOT, path.join(ROOT, '..', 'stremio-turbo')].map((r) => path.join(r, 'web', 'node_modules', 'stremio-translations')).find((d) => fs.existsSync(d));
    const locales = fs.readdirSync(trDir).filter((f) => f.endsWith('.json')).map((f) => JSON.parse(fs.readFileSync(path.join(trDir, f), 'utf8')));
    const unmute = locales.map((l) => l.PLAYER_UNMUTE).filter(Boolean);
    const labels = [...new Set([...unmute, ...locales.map((l) => l.PLAYER_MUTE).filter(Boolean)])];
    const muteTitle = () => evaluate(`(() => { const b = [...document.querySelectorAll('[class*="control-bar-button"]')].find((b) => ${JSON.stringify(labels)}.includes(b.title));
        return b && !b.className.includes('disabled') ? b.title : null; })()`);
    const mute = async (secs = 60) => {
        const end = Date.now() + secs * 1000;
        while (Date.now() < end) {
            await sleep(100);
            const title = await muteTitle();
            if (title === null) continue;
            if (unmute.includes(title)) return;
            for (const type of ['keyDown', 'keyUp']) await c.send('Input.dispatchKeyEvent', { type, key: 'm', code: 'KeyM', windowsVirtualKeyCode: 77 });
            await sleep(250);
            if (unmute.includes(await muteTitle())) return;
        }
        await evaluate('history.back()');
        throw new Error('could not mute the player: left it');
    };
    return { c, evaluate, privacy, waitFor, go, mute };
}

// The app window's rectangle on screen, in physical pixels. The window is brought to the front
// first (a key tap lets a background process take the foreground; F24, as Alt would put the
// window in menu mode and stall the web view), since the recording films whatever is on top.
function windowRect() {
    const ps = `Add-Type -Name W -Namespace R -MemberDefinition '[DllImport("user32.dll")] public static extern bool GetWindowRect(System.IntPtr h, out RECT r); [DllImport("dwmapi.dll")] public static extern int DwmGetWindowAttribute(System.IntPtr h, int a, out RECT r, int s); [DllImport("user32.dll")] public static extern bool SetForegroundWindow(System.IntPtr h); [DllImport("user32.dll")] public static extern bool ShowWindow(System.IntPtr h, int c); [DllImport("user32.dll")] public static extern void keybd_event(byte k, byte s, int f, int e); [DllImport("user32.dll")] public static extern bool IsIconic(System.IntPtr h); public struct RECT { public int L, T, R, B; }';
        $h = (Get-Process stremio-turbo | Where-Object MainWindowHandle -ne 0 | Select-Object -First 1).MainWindowHandle
        [R.W]::keybd_event(0x87, 0, 0, 0); [R.W]::keybd_event(0x87, 0, 2, 0); if ([R.W]::IsIconic($h)) { [void][R.W]::ShowWindow($h, 9) }; [void][R.W]::SetForegroundWindow($h); Start-Sleep -Milliseconds 300
        $r = New-Object R.W+RECT; [void][R.W]::DwmGetWindowAttribute($h, 9, [ref]$r, 16); "$($r.L) $($r.T) $($r.R) $($r.B)"`;
    const out = spawnSync('powershell', ['-NoProfile', '-Command', ps], { encoding: 'utf8' }).stdout.trim();
    const [l, t, r, b] = out.split(/\s+/).map(Number);
    if (![l, t, r, b].every(Number.isFinite)) throw new Error('app window not found: ' + out);
    return { x: l, y: t, w: (r - l) & ~1, h: (b - t) & ~1 };
}

// Records the app window's area of the screen while action runs (the player is mpv's own window
// behind the web view, so the DevTools screencast would miss the picture), then makes a GIF with
// Evenly's recipe: 12 fps, a 96-colour palette from the moving parts, Bayer dithering. The
// recording stops tail seconds after action finishes (a play's action ends at the first frame;
// footage costs a lot of GIF), or at secs at the latest.
async function record(dir, name, secs, action, { width = 640, tail = 2.5 } = {}) {
    const r = windowRect();
    const mp4 = path.join(dir, `${name}.mp4`);
    const ff = spawn(FFMPEG, ['-y', '-hide_banner', '-loglevel', 'error', '-f', 'gdigrab', '-framerate', '24', '-offset_x', r.x, '-offset_y', r.y,
        '-video_size', `${r.w}x${r.h}`, '-draw_mouse', '0', '-i', 'desktop', '-t', String(secs), '-pix_fmt', 'yuv420p', mp4].map(String), { stdio: ['pipe', 'inherit', 'inherit'] });
    const done = new Promise((res) => ff.on('exit', res));
    await sleep(400);
    await action();
    const stop = setTimeout(() => ff.stdin.write('q'), tail * 1000);
    await done;
    clearTimeout(stop);
    const gif = path.join(dir, `${name}.gif`);
    makeGif(mp4, gif, '', width);
    console.log('wrote', path.relative(ROOT, gif), `${Math.round(fs.statSync(gif).size / 1024)} KB`);
}

// What each set of captures shows. "library": the owner's own titles (the next episodes they're
// on, so plays only move resume points they'd reach anyway). "open": public-domain films with
// healthy swarms (Night of the Living Dead 1968, Charade 1963, Bonanza's The Gunmen 1960).
const SETS = {
    library: {
        banner: '#/detail/series/tt2442560/tt2442560%3A6%3A5',
        race: { route: '#/detail/series/tt11198330/tt11198330%3A2%3A6', tier: '4k DV | HDR' },
        engine: { route: '#/detail/series/tt11198330/tt11198330%3A2%3A8', tier: '4k DV | HDR' }, // the next, unplayed episode
        resume: { tile: 'tt15398776' }, // Oppenheimer, top of Continue Watching
        download: { route: '#/detail/series/tt11198330/tt11198330%3A2%3A7', tier: '4k DV | HDR' },
    },
    open: {
        banner: '#/detail/movie/tt0063350/tt0063350', // Night of the Living Dead
        race: { route: '#/detail/movie/tt0063350/tt0063350', tier: '1080p' }, // its 4k remuxes test at 1.0x
        engine: { route: '#/detail/movie/tt0056923/tt0056923', tier: '1080p' }, // Charade
        resume: { tile: 'tt0063350' }, // in Continue Watching after the race step
        download: { route: '#/detail/series/tt0052451/tt0052451%3A1%3A19', tier: 'TVRip' }, // Bonanza, The Gunmen (downloads are offered on episodes)
    },
};

// capture <set> [blur] [step ...]: blur softens mpv's picture (the UI stays sharp), for pages that
// shouldn't show the footage.
async function capture(args) {
    const set = SETS[args[0]];
    if (!set) throw new Error('capture <' + Object.keys(SETS).join('|') + '> [blur] [banner|race|engine|resume|download ...]');
    const blur = args[1] === 'blur';
    const which = args.slice(blur ? 2 : 1);
    const dir = path.join(RAW, args[0] + (blur ? '-blur' : ''));
    fs.mkdirSync(dir, { recursive: true });
    const a = await app();
    const server = 'http://127.0.0.1:11470';
    const before = new Set((await fetch(`${server}/downloads`).then((r) => r.json())).map((d) => `${d.infoHash}/${d.fileIdx}`));
    const tierButton = (label) => `[...document.querySelectorAll('[class*="quality-rows"] [title]')].find((b) => b.title === ${JSON.stringify(label)} && /(^|\\s)play-/.test(b.className))`;
    const open = async (route, ready) => {
        await a.go('#/');
        await sleep(1500);
        await a.go(route);
        await a.waitFor(ready);
        await sleep(2500);
    };
    const mpv = (prop, value) => a.evaluate(`chrome.webview.postMessage(JSON.stringify({ id: 0, type: 6, args: ['mpv-set-prop', [${JSON.stringify(prop)}, ${JSON.stringify(value)}]] }))`);
    // Plays from a click: mutes, and keeps the blur filter on until the first frame is past.
    const play = async (click, input) => {
        await a.evaluate(click);
        if (input) await input();
        if (blur) {
            for (let i = 0; i < 20; i++) {
                await mpv('vf', 'lavfi=[gblur=sigma=40]');
                await sleep(150);
            }
        }
        await a.mute();
    };
    const leave = async () => {
        await a.evaluate('history.back()');
        await sleep(2500);
        if (blur) await mpv('vf', '');
    };
    const steps = {
        async banner() {
            await open(set.banner, `!!document.querySelector('[class*="quality-rows"] [class*="play-"]')`);
            await sleep(1000);
            const { data } = await a.c.send('Page.captureScreenshot', { format: 'png' });
            fs.writeFileSync(path.join(dir, 'banner-app.png'), Buffer.from(data, 'base64'));
            console.log('wrote', path.relative(ROOT, path.join(dir, 'banner-app.png')));
        },
        async race() {
            await open(set.race.route, `!!(${tierButton(set.race.tier)})`);
            await record(dir, 'feature-race', 40, async () => {
                await sleep(700);
                await play(`(${tierButton(set.race.tier)}).click()`);
            });
            await leave();
        },
        async engine() {
            // A single source from the full list, with a stopwatch from the click to the first
            // frame (when the player's backdrop and loading layers go).
            // A quality row's Play (the race, then the stream), or one source from the full list.
            let link;
            if (set.engine.tier) {
                link = tierButton(set.engine.tier);
                await open(set.engine.route, `!!(${link})`);
            } else {
                await open(set.engine.route, `!!document.querySelector('[class*="all-streams"]')`);
                await a.evaluate(`document.querySelector('[class*="all-streams"]').click()`);
                link = `[...document.querySelectorAll('a')].filter((x) => x.innerText.includes(${JSON.stringify(set.engine.stream)}) && x.getBoundingClientRect().width > 0)[0]`;
                await a.waitFor(`!!(${link})`, 30);
                await a.privacy();
                await sleep(1500);
            }
            await record(dir, 'feature-engine', 45, async () => {
                await sleep(700);
                await a.evaluate(`(() => {
                    const d = document.createElement('div'); d.id = 'readme-clock';
                    d.style.cssText = 'position:fixed;left:24px;bottom:24px;z-index:99999;padding:10px 16px;border-radius:12px;background:rgba(20,16,40,.82);color:#fff;font:600 30px/1 Segoe UI,sans-serif;font-variant-numeric:tabular-nums;pointer-events:none';
                    document.documentElement.append(d);
                    const t0 = performance.now(); let stopped = false; let seen = false;
                    const tick = () => {
                        // The first frame: the player's backdrop and loading layers, seen once, are gone.
                        const shown = (sel) => [...document.querySelectorAll(sel)].some((e) => e.getBoundingClientRect().width > 0);
                        const loading = location.hash.startsWith('#/player') && (shown('[class*="background-layer"]') || shown('[class*="buffering-layer"]'));
                        if (loading) seen = true;
                        const b = seen && location.hash.startsWith('#/player') && !loading;
                        if (!stopped) d.textContent = ((performance.now() - t0) / 1000).toFixed(1) + ' s';
                        if (b && !stopped) { stopped = true; d.textContent += ' · first frame'; }
                        requestAnimationFrame(tick);
                    };
                    tick();
                })()`);
                await play(`(${link}).click()`);
                await a.waitFor(`!!document.getElementById('readme-clock')?.textContent.includes('first frame')`, 40);
            });
            await a.evaluate(`document.getElementById('readme-clock')?.remove()`);
            await leave();
        },
        async resume() {
            // The play icon on the board's Continue Watching card.
            // The visible copy: routes left behind stay in the page, hidden.
            const card = `[...document.querySelectorAll('[class*="continue"] [data-board-tile="${set.resume.tile}"] [class*="play-icon-layer"]')].find((e) => e.getBoundingClientRect().width > 0)`;
            await open('#/', `!!(${card})`);
            await record(dir, 'feature-resume', 20, async () => {
                await sleep(700);
                // A real mouse click: the icon reacts to pointer events, not to element.click().
                const at = await a.evaluate(`(() => { const r = (${card}).getBoundingClientRect(); return [r.x + r.width / 2, r.y + r.height / 2]; })()`);
                await play(`void 0`, async () => {
                    for (const type of ['mouseMoved', 'mousePressed', 'mouseReleased']) await a.c.send('Input.dispatchMouseEvent', { type, x: at[0], y: at[1], button: 'left', clickCount: 1 });
                });
            });
            await leave();
        },
        async download() {
            const dl = `[...document.querySelectorAll('[class*="quality-rows"] [class*="tier"]')].find((t) => [...t.querySelectorAll('[title]')].some((b) => b.title === ${JSON.stringify(set.download.tier)}))?.querySelector('[class*="download"]')`;
            const downloading = `[...document.querySelectorAll('[class*="downloads"] [tabindex], [class*="downloads"] button')].find((b) => /^Downloading/.test(b.innerText.trim()) && b.getBoundingClientRect().width > 0)`;
            await open(set.download.route, `!!(${dl})`);
            await record(dir, 'feature-downloads', 20, async () => {
                await sleep(800);
                await a.evaluate(`(${dl}).click()`);
                await sleep(4500);
                await a.go('#/downloads');
                // Only what is downloading now, so the new download is in view.
                await a.waitFor(`!!${downloading}`, 10);
                await sleep(1200);
                await a.evaluate(`${downloading}.click()`);
            }, { tail: 4 });
        },
    };
    try {
        for (const name of which.length ? which : Object.keys(steps)) {
            if (!steps[name]) throw new Error('unknown capture: ' + name);
            await steps[name]();
        }
    } finally {
        if (blur) await mpv('vf', '');
        // Downloads the captures started (the download step, and the plays' own records) are
        // removed again, so the captures leave the owner's downloads as they were. A play's
        // record and a manual download's record appear seconds after the click (after the race),
        // so the list is read until it stops changing.
        const list = () => fetch(`${server}/downloads`).then((r) => r.json());
        let now = await list();
        for (let i = 0; i < 6; i++) {
            await sleep(5000);
            const next = await list();
            if (next.length === now.length) break;
            now = next;
        }
        const owned = new Set([...before].map((k) => k.split('/')[0]));
        const added = now.filter((d) => !before.has(`${d.infoHash}/${d.fileIdx}`));
        for (const ih of new Set(added.filter((d) => !owned.has(d.infoHash)).map((d) => d.infoHash))) {
            await fetch(`${server}/downloads/${ih}`, { method: 'DELETE' });
            console.log('removed a torrent the capture added:', ih.slice(0, 8));
        }
        // A torrent that already held one of the owner's files (a season pack's other episode)
        // keeps it: only the capture's own files go, one by one.
        for (const d of added.filter((d) => owned.has(d.infoHash) && d.kind === 'kept')) {
            await fetch(`${server}/downloads/${d.infoHash}/${d.fileIdx}`, { method: 'DELETE' });
            console.log('removed a file the capture kept:', d.infoHash.slice(0, 8), d.fileIdx);
        }
        a.c.close();
    }
}

// ---- compose ------------------------------------------------------------------------------------

const BRAND = { top: '#8460ff', bottom: '#2054e0', badge: '#6b5cf0' };
const FONT = `'Segoe UI Variable Text', 'Segoe UI', system-ui, sans-serif`;
// Chart colours: Turbo carries the emphasis, the other clients one neutral. Checked with the
// dataviz validator: both marks >= 3:1 against GitHub's light and dark page backgrounds.
const THEMES = {
    light: { surface: '#ffffff', ink: '#1f2328', muted: '#59636e', faint: '#d1d9e0', turbo: '#6b5cf0', other: '#8c959f' },
    dark: { surface: '#0d1117', ink: '#f0f6fc', muted: '#9198a1', faint: '#3d444d', turbo: '#8476f7', other: '#6e7681' },
};
const CLIENTS = [['official', 'Official Stremio'], ['turbo', 'Stremio Turbo'], ['qbt', 'qBittorrent*']];

function chartHtml(b, theme) {
    const t = THEMES[theme];
    const panel = (title, unit, key, fmt, noValue) => {
        const groups = [['healthy', `Healthy swarm · about 95 seeders`], ['thin', `Thin swarm · about 14 seeders`]];
        const all = groups.flatMap(([g]) => CLIENTS.map(([c]) => b[g][c]?.[key]?.median)).filter((v) => v != null);
        const max = Math.max(...all);
        const rows = groups.map(([g, label]) => `
            <div class="group">${label}</div>
            ${CLIENTS.map(([c, name]) => {
                const s = b[g][c];
                const v = s?.[key]?.median;
                const missing = v == null || (key === 'firstFrameS' && s.started < s.runs / 2);
                const f = missing ? 0 : Math.max(0.005, v / max);
                return `<div class="row"><div class="name${c === 'turbo' ? ' strong' : ''}">${name}</div>
                    <div class="track">${f ? `<div class="bar" style="width:calc((100% - 96px) * ${f.toFixed(4)});background:${c === 'turbo' ? t.turbo : t.other}"></div>` : ''}
                    <div class="val${c === 'turbo' ? ' strong' : ''}">${missing ? (c === 'qbt' && key === 'firstFrameS' ? 'not ready in 60 s' : noValue) : fmt(v)}${!missing && key === 'firstFrameS' && s.started < s.runs ? `<span class="note"> · ${s.started} of ${s.runs} started</span>` : ''}</div></div></div>`;
            }).join('')}`).join('');
        return `<div class="panel"><div class="title">${title}</div><div class="unit">${unit}</div>${rows}</div>`;
    };
    return `<!doctype html><html><head><meta charset="utf-8"><style>
        * { box-sizing: border-box; margin: 0; }
        body { width: 800px; background: ${t.surface}; color: ${t.ink}; font: 15px/1.35 ${FONT}; padding: 28px 32px; }
        .panels { display: flex; gap: 48px; }
        .panel { flex: 1; }
        .title { font-size: 19px; font-weight: 600; }
        .unit { color: ${t.muted}; margin-bottom: 14px; }
        .group { color: ${t.muted}; font-size: 13px; margin: 14px 0 6px; padding-bottom: 4px; border-bottom: 1px solid ${t.faint}; }
        .row { display: flex; align-items: center; height: 34px; }
        .name { width: 128px; flex: none; }
        .strong { font-weight: 600; }
        .track { flex: 1; display: flex; align-items: center; gap: 8px; height: 100%; }
        .bar { height: 20px; border-radius: 0 4px 4px 0; flex: none; }
        .val { white-space: nowrap; font-variant-numeric: tabular-nums; }
        .note { color: ${t.muted}; font-size: 13px; }
        .foot { color: ${t.muted}; font-size: 12.5px; margin-top: 22px; }
    </style></head><body>
        <div class="panels">
            ${panel('Click → first frame', 'seconds, median · lower is better', 'firstFrameS', (v) => `${v.toFixed(1)} s`, 'no frame in 60 s')}
            ${panel('Download speed', 'MB/s over the first minute, median', 'mbps', (v) => v.toFixed(1), '—')}
        </div>
        <div class="foot">A fresh 4K HDR episode, nothing cached, ${b.healthy.turbo?.runs ?? 3} runs per client in rotating order (${b.date}).
            * qBittorrent has no player: its time is until the file's first and last pieces are on disk.</div>
    </body></html>`;
}

function buttonHtml() {
    return `<!doctype html><html><head><meta charset="utf-8"><style>
        * { margin: 0; box-sizing: border-box; }
        body { width: 300px; height: 64px; display: flex; }
        a { flex: 1; margin: 4px; border-radius: 14px; display: flex; align-items: center; justify-content: center; gap: 12px;
            background: linear-gradient(135deg, ${BRAND.top}, ${BRAND.bottom}); color: #fff; font: 600 19px ${FONT};
            box-shadow: 0 2px 8px rgba(40, 30, 120, .35); text-decoration: none; }
        svg { width: 22px; height: 22px; }
    </style></head><body><a>
        <svg viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12m0 0-5-5m5 5 5-5M4 20h16"/></svg>
        Download for Windows</a></body></html>`;
}

// The banner: the brand on the left, a framed app window on the right (the window frame is CSS,
// the content is a capture).
function bannerHtml(appPng, iconPng) {
    const img = (p) => `data:image/png;base64,${fs.readFileSync(p).toString('base64')}`;
    return `<!doctype html><html><head><meta charset="utf-8"><style>
        * { margin: 0; box-sizing: border-box; }
        body { width: 1280px; height: 640px; overflow: hidden; font-family: ${FONT}; color: #fff;
            background: radial-gradient(120% 140% at 0% 0%, #3a2a9e 0%, #1b1550 45%, #0c0b11 100%); position: relative; }
        .glow { position: absolute; right: -120px; top: -160px; width: 760px; height: 760px; border-radius: 50%;
            background: radial-gradient(circle, rgba(132,96,255,.45), rgba(32,84,224,0) 65%); }
        .left { position: absolute; left: 72px; top: 0; bottom: 0; width: 420px; display: flex; flex-direction: column; justify-content: center; }
        .mark { width: 84px; height: 84px; margin-bottom: 26px; }
        h1 { font-size: 58px; font-weight: 700; letter-spacing: -1.5px; line-height: 1; }
        h1 span { background: linear-gradient(90deg, #b7a6ff, #6f9bff); -webkit-background-clip: text; color: transparent; }
        p { margin-top: 20px; font-size: 25px; line-height: 1.35; color: #d9d4ff; }
        .win { position: absolute; left: 530px; top: 58px; width: 700px; border-radius: 12px; overflow: hidden;
            box-shadow: 0 30px 80px rgba(0,0,0,.55), 0 0 0 1px rgba(255,255,255,.08); background: #0c0b11; }
        .bar { height: 34px; background: #17151f; display: flex; align-items: center; padding: 0 14px; gap: 10px; font-size: 13px; color: #b9b5c9; }
        .bar img { width: 16px; height: 16px; }
        .bar .ctl { margin-left: auto; letter-spacing: 18px; color: #8d8a99; }
        /* The right side of the page: the cast art and the quality rows. */
        .shot { height: 490px; overflow: hidden; position: relative; }
        .shot img { position: absolute; right: 0; top: 0; height: 100%; }
    </style></head><body><div class="glow"></div>
        <div class="left">
            <img class="mark" src="${img(iconPng)}">
            <h1>Stremio <span>Turbo</span></h1>
            <p>Stremio for Windows, rebuilt for speed. 4K HDR starts in seconds and doesn't buffer.</p>
        </div>
        <div class="win"><div class="bar"><img src="${img(iconPng)}">Stremio Turbo<span class="ctl">–▢✕</span></div><div class="shot"><img src="${img(appPng)}"></div></div>
    </body></html>`;
}

// The time of the clip's last scene cut: where the page gives way to the player.
function playerCut(mp4) {
    const r = spawnSync(FFMPEG, ['-hide_banner', '-i', mp4, '-vf', "select='gt(scene,0.25)',showinfo", '-f', 'null', '-'], { encoding: 'utf8' });
    const cuts = [...r.stderr.matchAll(/pts_time:([0-9.]+)/g)].map((m) => Number(m[1]));
    return cuts.length ? cuts[cuts.length - 1] : null;
}

function makeGif(mp4, gif, pre = '', width = 640) {
    const vf = `${pre}fps=12,scale=${width}:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=96:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle`;
    spawnSync(FFMPEG, ['-y', '-hide_banner', '-loglevel', 'error', '-i', mp4, '-vf', vf, gif], { stdio: 'inherit' });
}

// compose [set]: the chart, the button, and the set's banner and GIFs into docs/readme. A set
// named "<set>-blur" is made from <set>'s clips with the picture blurred from the cut to the
// player on (the pages stay sharp, and so does the engine clip's stopwatch).
async function compose(set = 'library') {
    fs.mkdirSync(DOCS, { recursive: true });
    const blur = set.endsWith('-blur');
    const src = path.join(RAW, blur ? set.slice(0, -5) : set);
    if (!fs.existsSync(src)) throw new Error(`no captures in ${path.relative(ROOT, src)}: run capture first`);
    const b = JSON.parse(fs.readFileSync(path.join(DOCS, 'bench.json'), 'utf8'));
    const e = await edge();
    try {
        for (const theme of ['light', 'dark']) await e.render(chartHtml(b, theme), 800, null, path.join(DOCS, `bench-${theme}.png`));
        await e.render(buttonHtml(), 300, 64, path.join(DOCS, 'button-download.png'));
        const app = path.join(src, 'banner-app.png');
        const icon = path.join(ROOT, 'shell', 'images', 'stremio.png');
        if (fs.existsSync(app)) await e.render(bannerHtml(app, fs.existsSync(icon) ? icon : app), 1280, 640, path.join(DOCS, 'banner.png'));
        else console.log('skipped banner.png: no banner-app.png in', path.relative(ROOT, src));
    } finally {
        await e.close();
    }
    const out = path.join(RAW, set);
    fs.mkdirSync(out, { recursive: true });
    for (const name of ['feature-race', 'feature-engine', 'feature-resume', 'feature-downloads']) {
        let gif = path.join(src, `${name}.gif`);
        const cut = blur && name !== 'feature-downloads' ? playerCut(path.join(src, `${name}.mp4`)) : null;
        if (cut !== null) {
            const on = `enable='gte(t,${cut})'`;
            const pre = name === 'feature-engine'
                ? `split[s][c];[s]gblur=sigma=70:steps=3:${on}[b];[c]crop=iw*0.32:ih*0.09:0:ih*0.91[k];[b][k]overlay=0:H*0.91:${on},`
                : `gblur=sigma=70:steps=3:${on},`;
            gif = path.join(out, `${name}.gif`);
            makeGif(path.join(src, `${name}.mp4`), gif, pre);
        }
        if (!fs.existsSync(gif)) {
            console.log('skipped', name, '(not captured)');
            continue;
        }
        fs.copyFileSync(gif, path.join(DOCS, `${name}.gif`));
        console.log('wrote', `docs/readme/${name}.gif`, `${Math.round(fs.statSync(gif).size / 1024)} KB`, cut !== null ? `(blurred from ${cut.toFixed(1)} s)` : '');
    }
}

const [mode, ...args] = process.argv.slice(2);
if (mode === 'bench' && args.length === 2) bench(...args);
else if (mode === 'compose') await compose(...args);
else if (mode === 'capture') await capture(args);
else {
    console.error('usage: node scripts/readme-images.mjs bench <healthy dir> <thin dir> | capture <set> [blur] [step ...] | compose [library|library-blur|open]');
    process.exit(2);
}
