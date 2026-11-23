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
            // A clean shutdown: killing Edge leaves its tabs registered with Windows, and they
            // linger in Alt+Tab as blank entries until Explorer restarts.
            const exited = new Promise((res) => proc.once('exit', res));
            await c.send('Browser.close').catch(() => {});
            await Promise.race([exited, sleep(5000)]);
            c.close();
            if (proc.exitCode === null) proc.kill();
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
            // The run whose speed is the median's: its MB on disk at each second, for the line
            // chart (samples.json beside results.json; only the byte counts are read).
            const mid = stat('mbps')?.median;
            const run = mid == null ? null : rs.reduce((a, r) => Math.abs(r.mbps - mid) < Math.abs(a.mbps - mid) ? r : a);
            let mb = null;
            const samples = run && path.join(dir, `r${run.round}-${name}`, 'samples.json');
            if (samples && fs.existsSync(samples)) {
                const s = JSON.parse(fs.readFileSync(samples, 'utf8')).samples.filter((x) => x.t != null && x.downloaded != null);
                mb = Array.from({ length: 61 }, (_, sec) => {
                    const i = s.findIndex((x) => x.t >= sec);
                    if (i <= 0) return i === 0 && s[0].t === sec ? +(s[0].downloaded / 1e6).toFixed(1) : (i < 0 ? +(s.at(-1).downloaded / 1e6).toFixed(1) : 0);
                    const [p, q] = [s[i - 1], s[i]];
                    return +((p.downloaded + (q.downloaded - p.downloaded) * (sec - p.t) / (q.t - p.t)) / 1e6).toFixed(1);
                });
            }
            out[name] = {
                runs: rs.length,
                firstFrameS: stat('firstFrameS'), started: rs.filter((r) => r.firstFrameS != null).length,
                mbps: stat('mbps'), peersMax: stat('peersMax'), stalls: stat('stalls'), stallSeconds: stat('stallSeconds'),
                mb,
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
// The window size captures use, and the streams panel on a detail page.
const WINDOW = [1280, 800];
const PANEL = '[class*="streams"][class*="panel"]';
// ffmpeg from FFMPEG or PATH, else the copy the app ships (older, but enough for gdigrab).
const FFMPEG = process.env.FFMPEG
    || spawnSync('where', ['ffmpeg'], { encoding: 'utf8' }).stdout?.split(/\r?\n/).find((l) => l.trim().endsWith('.exe'))?.trim()
    || path.join(process.env.LOCALAPPDATA, 'Programs', 'Stremio Turbo', 'ffmpeg.exe');

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
// With a size, the window is first restored from maximised and resized to it (outer pixels), so
// the UI reads larger in a small GIF; restoreWindow() puts it back as it was.
const WIN_API = `Add-Type -Name W -Namespace R -MemberDefinition '[DllImport("user32.dll")] public static extern bool GetWindowRect(System.IntPtr h, out RECT r); [DllImport("dwmapi.dll")] public static extern int DwmGetWindowAttribute(System.IntPtr h, int a, out RECT r, int s); [DllImport("user32.dll")] public static extern bool SetForegroundWindow(System.IntPtr h); [DllImport("user32.dll")] public static extern bool ShowWindow(System.IntPtr h, int c); [DllImport("user32.dll")] public static extern void keybd_event(byte k, byte s, int f, int e); [DllImport("user32.dll")] public static extern bool IsIconic(System.IntPtr h); [DllImport("user32.dll")] public static extern bool IsZoomed(System.IntPtr h); [DllImport("user32.dll")] public static extern bool SetWindowPos(System.IntPtr h, System.IntPtr a, int x, int y, int w, int t, uint f); public struct RECT { public int L, T, R, B; }';
    $h = (Get-Process stremio-turbo | Where-Object MainWindowHandle -ne 0 | Select-Object -First 1).MainWindowHandle`;
const ps = (body) => spawnSync('powershell', ['-NoProfile', '-Command', `${WIN_API}
${body}`], { encoding: 'utf8' }).stdout.trim();
let savedWindow = null;

function windowRect(size) {
    if (size && !savedWindow) {
        const [zoomed, l, t, r, b] = ps('$r = New-Object R.W+RECT; [void][R.W]::GetWindowRect($h, [ref]$r); "$([int][R.W]::IsZoomed($h)) $($r.L) $($r.T) $($r.R) $($r.B)"').split(/\s+/).map(Number);
        savedWindow = { zoomed: zoomed === 1, l, t, w: r - l, h: b - t };
    }
    const resize = size ? `if ([R.W]::IsZoomed($h)) { [void][R.W]::ShowWindow($h, 9); Start-Sleep -Milliseconds 300 }; [void][R.W]::SetWindowPos($h, [System.IntPtr]::Zero, 80, 60, ${size[0]}, ${size[1]}, 4); Start-Sleep -Milliseconds 800;` : '';
    const out = ps(`${resize} [R.W]::keybd_event(0x87, 0, 0, 0); [R.W]::keybd_event(0x87, 0, 2, 0); if ([R.W]::IsIconic($h)) { [void][R.W]::ShowWindow($h, 9) }; [void][R.W]::SetForegroundWindow($h); Start-Sleep -Milliseconds 300
        $r = New-Object R.W+RECT; [void][R.W]::DwmGetWindowAttribute($h, 9, [ref]$r, 16); "$($r.L) $($r.T) $($r.R) $($r.B)"`);
    const [l, t, r, b] = out.split(/\s+/).map(Number);
    if (![l, t, r, b].every(Number.isFinite)) throw new Error('app window not found: ' + out);
    return { x: l, y: t, w: (r - l) & ~1, h: (b - t) & ~1 };
}

function restoreWindow() {
    if (!savedWindow) return;
    const { zoomed, l, t, w, h } = savedWindow;
    ps(`[void][R.W]::SetWindowPos($h, [System.IntPtr]::Zero, ${l}, ${t}, ${w}, ${h}, 4); ${zoomed ? '[void][R.W]::ShowWindow($h, 3)' : ''}`);
    savedWindow = null;
}

// Records the app window's area of the screen while action runs (the player is mpv's own window
// behind the web view, so the DevTools screencast would miss the picture), then makes a GIF with
// Evenly's recipe: 12 fps, a 96-colour palette from the moving parts, Bayer dithering. The
// recording stops tail seconds after action finishes (a play's action ends at the first frame;
// footage costs a lot of GIF), or at secs at the latest.
// crop: a page rectangle (CSS pixels, from getBoundingClientRect) to keep, scaled by zoom.
// endAtCut: the GIF ends just after the cut to the player (a crop of the player is only its backdrop).
async function record(dir, name, secs, action, { width = 640, tail = 2.5, crop = null, zoom = 1.3, endAtCut = false } = {}) {
    const r = windowRect();
    const mp4 = path.join(dir, `${name}.mp4`);
    const ff = spawn(FFMPEG, ['-y', '-hide_banner', '-loglevel', 'error', '-f', 'gdigrab', '-framerate', '24', '-offset_x', r.x, '-offset_y', r.y,
        '-video_size', `${r.w}x${r.h}`, '-draw_mouse', '0', '-i', 'desktop', '-t', String(secs), '-pix_fmt', 'yuv420p', mp4].map(String), { stdio: ['pipe', 'inherit', 'inherit'] });
    const t0 = Date.now();
    const done = new Promise((res) => ff.on('exit', res));
    await sleep(400);
    const endAt = await action();
    const stop = setTimeout(() => ff.stdin.write('q'), tail * 1000);
    await done;
    clearTimeout(stop);
    const gif = path.join(dir, `${name}.gif`);
    if (crop) {
        // The web view fills the window below its title bar.
        const x = Math.round(crop.x + crop.border), y = Math.round(crop.y + crop.top);
        const cropF = `crop=${Math.round(crop.width) & ~1}:${Math.round(crop.height) & ~1}:${x}:${y}`;
        // endAt (from the action, epoch ms) is when the page left for the player. The cropped
        // panel then turns into the player's dark backdrop, too small a change for a fixed scene
        // threshold, so the page's time is snapped to the nearest faint cut in the crop.
        let trim = '';
        if (endAtCut && endAt) {
            const est = (endAt - t0) / 1000;
            const near = sceneCuts(mp4, cropF, 0.05).filter((c) => Math.abs(c - est) < 1).sort((a, b) => Math.abs(a - est) - Math.abs(b - est))[0];
            trim = `trim=end=${((near ?? est) - 0.05).toFixed(2)},`;
        }
        makeGif(mp4, gif, `${trim}${cropF},`, Math.round(crop.width * zoom) & ~1);
    } else {
        makeGif(mp4, gif, '', width);
    }
    console.log('wrote', path.relative(ROOT, gif), `${Math.round(fs.statSync(gif).size / 1024)} KB`);
}

// What each set of captures shows. "library": the owner's own titles (the next episodes they're
// on, so plays only move resume points they'd reach anyway). "open": public-domain films with
// healthy swarms (Night of the Living Dead 1968, Charade 1963, Bonanza's The Gunmen 1960).
const SETS = {
    library: {
        banner: '#/detail/series/tt2442560/tt2442560%3A6%3A5',
        race: { route: '#/detail/series/tt11198330/tt11198330%3A2%3A2', tier: '4k DV | HDR' }, // an episode not on disk
        engine: { route: '#/detail/series/tt11198330/tt11198330%3A2%3A8', tier: '4k DV | HDR' }, // the next, unplayed episode
        resume: { tile: 'tt15398776' }, // Oppenheimer: must be the first Continue Watching card (the one warmed up)
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
    // A smaller window: the UI reads larger in a GIF of the same size.
    const win = windowRect(WINDOW);
    const server = 'http://127.0.0.1:11470';
    // Where the page sits in the recorded window rectangle (title bar on top, borders around).
    const frame = await a.evaluate('[innerWidth, innerHeight]').then(([w, h]) => ({ border: (win.w - w) / 2, top: win.h - h - (win.w - w) / 2 }));
    const rectOf = async (sel, pad = 0) => ({ ...frame, ...(await a.evaluate(`(() => { const e = [...document.querySelectorAll(${JSON.stringify(sel)})].find((e) => e.getBoundingClientRect().width > 0); const r = e.getBoundingClientRect(); return { x: r.x - ${pad}, y: r.y - ${pad}, width: r.width + ${2 * pad}, height: r.height + ${2 * pad} }; })()`)) });
    // The player's first frame: its backdrop and loading layers, once seen, are gone.
    const watchFirstFrame = () => a.evaluate(`(() => {
        const f = window.__readmeFF = { seen: false, at: null, player: null };
        const shown = (sel) => [...document.querySelectorAll(sel)].some((e) => e.getBoundingClientRect().width > 0);
        const tick = () => {
            const player = location.hash.startsWith('#/player');
            if (player && f.player == null) f.player = performance.timeOrigin + performance.now();
            const loading = player && (shown('[class*="background-layer"]') || shown('[class*="buffering-layer"]'));
            if (loading) f.seen = true;
            if (f.seen && player && !loading) { f.at = performance.now(); return; }
            requestAnimationFrame(tick);
        };
        tick();
    })()`);
    const firstFrame = (secs = 45) => a.waitFor('window.__readmeFF?.at != null', secs);
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
            // A close-up of the quality picker with some of the show's art beside it, at 2x.
            await open(set.banner, `!!document.querySelector('[class*="quality-rows"] [class*="play-"]')`);
            await sleep(1000);
            const p = await rectOf(PANEL);
            const vw = await a.evaluate('innerWidth');
            const clip = { x: p.x - 300, y: p.y - 16, width: vw - (p.x - 300), height: 560, scale: 2 };
            const { data } = await a.c.send('Page.captureScreenshot', { format: 'png', clip });
            fs.writeFileSync(path.join(dir, 'banner-app.png'), Buffer.from(data, 'base64'));
            console.log('wrote', path.relative(ROOT, path.join(dir, 'banner-app.png')));
        },
        async race() {
            // Cropped to the panel's top: the quality rows, then the race in their place.
            await open(set.race.route, `!!(${tierButton(set.race.tier)})`);
            // Filmed from the page (DevTools screenshots of the panel), not the screen: the panel is
            // all web content, and a screen grab sometimes catches the web view mid-composition
            // over mpv's window, so the panel blinks out for a frame. Ends at the switch to the player.
            const p = await rectOf(PANEL, 8);
            const clip = { x: p.x, y: p.y, width: p.width, height: 440, scale: 2 };
            const frames = path.join(dir, 'race-frames');
            fs.rmSync(frames, { recursive: true, force: true });
            fs.mkdirSync(frames);
            await watchFirstFrame();
            const shots = [];
            let stop = false;
            const filming = (async () => {
                const t0 = Date.now();
                while (!stop && Date.now() - t0 < 40000) {
                    const at = Date.now();
                    const { data } = await a.c.send('Page.captureScreenshot', { format: 'png', clip });
                    if (stop) break;
                    const racing = await a.evaluate(`[...document.querySelectorAll('[class*="racing-cancel"]')].some((b) => b.getBoundingClientRect().width > 0)`);
                    const file = `f${String(shots.length).padStart(4, '0')}.png`;
                    fs.writeFileSync(path.join(frames, file), Buffer.from(data, 'base64'));
                    shots.push({ file, at, racing });
                    if (shots.length % 3 === 0 && await a.evaluate(`location.hash.startsWith('#/player')`)) break;
                    await sleep(Math.max(0, 70 - (Date.now() - at)));
                }
            })();
            await sleep(900);
            // Stop filming as soon as the page leaves for the player (the poll above is a backstop).
            const playing = play(`(${tierButton(set.race.tier)}).click()`);
            await a.waitFor(`location.hash.startsWith('#/player')`, 40);
            stop = true;
            await filming;
            await playing;
            await firstFrame();
            await leave();
            // The race panel closes a moment before the player opens: end on its last frame.
            if (shots.some((s) => s.racing)) while (!shots.at(-1).racing) shots.pop();
            // The frames with their real timing, then the clip at 12 fps.
            const list = shots.map((s, i) => `file ${s.file}\nduration ${(((shots[i + 1]?.at ?? s.at + 700) - s.at) / 1000).toFixed(3)}`).join('\n');
            fs.writeFileSync(path.join(frames, 'list.txt'), `ffconcat version 1.0\n${list}\nfile ${shots.at(-1).file}\n`);
            const mp4 = path.join(dir, 'feature-race.mp4');
            spawnSync(FFMPEG, ['-y', '-hide_banner', '-loglevel', 'error', '-f', 'concat', '-safe', '0', '-i', path.join(frames, 'list.txt'),
                '-vf', 'fps=12,scale=trunc(iw/2)*2:trunc(ih/2)*2', '-pix_fmt', 'yuv444p', '-crf', '8', mp4], { stdio: 'inherit' });
            makeGif(mp4, path.join(dir, 'feature-race.gif'), '', Math.round(clip.width * 1.3) & ~1);
            console.log('wrote', path.relative(ROOT, mp4), shots.length, 'frames');
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
            // The board warms up its first card in the background (the head, the tail and the
            // window at the resume point, findings/22): give it time, as a user reading the
            // board would.
            await sleep(25000);
            await record(dir, 'feature-resume', 25, async () => {
                await sleep(700);
                await watchFirstFrame();
                // A real mouse click: the icon reacts to pointer events, not to element.click().
                const at = await a.evaluate(`(() => { const r = (${card}).getBoundingClientRect(); return [r.x + r.width / 2, r.y + r.height / 2]; })()`);
                await play(`void 0`, async () => {
                    for (const type of ['mouseMoved', 'mousePressed', 'mouseReleased']) await a.c.send('Input.dispatchMouseEvent', { type, x: at[0], y: at[1], button: 'left', clickCount: 1 });
                });
                // Then the picture playing.
                await firstFrame();
            }, { width: 800, tail: 3.5 });
            await leave();
        },
        async download() {
            // The Downloads screen on mocked records (scripts/readme-fixtures/downloads.json):
            // DevTools answers the page's /downloads calls, so the server is never asked or
            // changed. Downloading rows advance at their rate, as on a real screen.
            const fx = JSON.parse(fs.readFileSync(path.join(ROOT, 'scripts', 'readme-fixtures', 'downloads.json'), 'utf8'));
            const t0 = Date.now();
            const records = () => fx.downloads.map((d) => {
                if (d.state !== 'downloading') return d;
                const done = Math.min(d.size - 1, d.bytesCompleted + d.rate * (Date.now() - t0) / 1000);
                return { ...d, bytesCompleted: done, bytesOnDisk: done };
            });
            const answer = async ({ params }) => {
                const u = new URL(params.request.url);
                const body = params.request.method !== 'GET' ? {} : u.pathname === '/downloads' ? records() : u.pathname === '/downloads/disk' ? fx.disk : null;
                if (body === null) return a.c.send('Fetch.continueRequest', { requestId: params.requestId });
                await a.c.send('Fetch.fulfillRequest', { requestId: params.requestId, responseCode: 200,
                    responseHeaders: [{ name: 'Content-Type', value: 'application/json' }, { name: 'Access-Control-Allow-Origin', value: '*' }],
                    body: Buffer.from(JSON.stringify(body)).toString('base64') });
            };
            const listener = (m) => { if (m.method === 'Fetch.requestPaused') answer(m).catch(() => {}); };
            a.c.on(listener);
            await a.c.send('Fetch.enable', { patterns: [{ urlPattern: 'http://127.0.0.1:11470/downloads*' }] });
            const chip = (label) => `[...document.querySelectorAll('[class*="downloads"] [class*="chip"], [class*="downloads"] [tabindex]')].find((b) => b.innerText.trim().startsWith(${JSON.stringify(label)}) && b.getBoundingClientRect().width > 0 && b.innerText.trim().length < 30)`;
            const list = `[...document.querySelectorAll('[class*="downloads"] *')].find((e) => e.scrollHeight > e.clientHeight + 40 && getComputedStyle(e).overflowY === 'auto' && e.getBoundingClientRect().width > 0)`;
            try {
                await a.evaluate(`location.hash = '#/'`);
                await sleep(1500);
                await a.evaluate(`location.hash = '#/downloads'`);
                await a.waitFor(`!!(${chip('Downloading')}) && !!(${list})`, 20);
                // A smaller window still, filmed near 1:1, for bigger text; the mocked file names
                // unblurred.
                windowRect([1040, 700]);
                await a.evaluate(`document.getElementById('readme-privacy')?.remove()`);
                await sleep(2500);
                await record(dir, 'feature-downloads', 30, async () => {
                    await sleep(1200);
                    // A slow scroll to the bottom and back up.
                    const glide = (to, ms) => a.evaluate(`new Promise((done) => { const e = ${list}; const from = e.scrollTop; const target = ${to} === -1 ? e.scrollHeight - e.clientHeight : ${to}; const t0 = performance.now();
                        const f = (now) => { const k = Math.min(1, (now - t0) / ${ms}); const ease = k < .5 ? 2 * k * k : 1 - (-2 * k + 2) ** 2 / 2; e.scrollTop = from + (target - from) * ease; k < 1 ? requestAnimationFrame(f) : done(); }; requestAnimationFrame(f); })`);
                    await glide(-1, 3500);
                    await sleep(900);
                    await glide(0, 1800);
                    await sleep(900);
                    await a.evaluate(`(${chip('Downloading')}).click()`);
                    await sleep(2600);
                    await a.evaluate(`(${chip('Stopped')}).click()`);
                    await sleep(1800);
                    await a.evaluate(`(${chip('All')}).click()`);
                }, { width: 800, tail: 1.5 });
            } finally {
                windowRect(WINDOW);
                await a.c.send('Fetch.disable');
            }
        },
    };
    try {
        for (const name of which.length ? which : Object.keys(steps)) {
            if (!steps[name]) throw new Error('unknown capture: ' + name);
            await steps[name]();
        }
    } finally {
        if (blur) await mpv('vf', '');
        restoreWindow();
        // Downloads the captures started (the download step, and the plays' own records) are
        // removed again, so the captures leave the owner's downloads as they were. A play's
        // record and a manual download's record appear seconds after the click (after the race),
        // so the list is read until it stops changing.
        const list = () => fetch(`${server}/downloads`).then((r) => r.json());
        // At least 15 s, and until two reads 5 s apart agree (a winner's record can take that long).
        let now = await list();
        for (let i = 0, still = 0; i < 12 && still < 3; i++) {
            await sleep(5000);
            const next = await list();
            still = next.length === now.length ? still + 1 : 0;
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

// Download speed as MB on disk over the first minute (the median run of each client) for one
// swarm. Lines are direct-labelled at their ends; Turbo carries the colour.
function speedPanelSvg(b, g, t, W = 360, H = 210, fs = 1) {
    const L = 44 * fs, R = 86 * fs, T = 12, B = 28 * fs;
    const short = { official: 'Official', turbo: 'Turbo', qbt: 'qBittorrent' };
    const series = CLIENTS.map(([c]) => ({ c, name: short[c], mb: b[g][c]?.mb })).filter((s) => s.mb);
    const max = Math.max(...series.flatMap((s) => s.mb));
    const step = [50, 100, 250, 500, 1000].find((s) => max / s <= 4) ?? 1000;
    const top = Math.ceil(max / step) * step;
    const x = (sec) => L + (W - L - R) * sec / 60;
    const y = (v) => T + (H - T - B) * (1 - v / top);
    const fmt = (v) => v >= 1000 ? `${+(v / 1000).toFixed(1)} GB` : `${v} MB`;
    const grid = Array.from({ length: top / step + 1 }, (_, i) => i * step).map((v) =>
        `<line x1="${L}" x2="${W - R}" y1="${y(v)}" y2="${y(v)}" stroke="${t.faint}" stroke-width="1"/>
         <text x="${L - 8}" y="${y(v) + 4}" text-anchor="end" fill="${t.muted}" font-size="${11 * fs}">${fmt(v)}</text>`).join('');
    const xs = [0, 15, 30, 45, 60].map((s) => `<text x="${x(s)}" y="${H - 8}" text-anchor="middle" fill="${t.muted}" font-size="${11 * fs}">${s} s</text>`).join('');
    // End labels, nudged apart when two lines finish close together.
    const ends = series.map((s) => ({ ...s, ly: y(s.mb[60]) })).sort((a, z) => a.ly - z.ly);
    for (let i = 1; i < ends.length; i++) ends[i].ly = Math.max(ends[i].ly, ends[i - 1].ly + 26 * fs);
    const lines = series.map((s) => `<polyline fill="none" stroke="${s.c === 'turbo' ? t.turbo : t.other}" stroke-width="${s.c === 'turbo' ? 3 : 2}" stroke-linejoin="round"
        ${s.c === 'qbt' ? 'stroke-dasharray="5 4"' : ''} points="${s.mb.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(' ')}"/>`).join('');
    const labels = ends.map((s) => `<circle cx="${x(60)}" cy="${y(s.mb[60])}" r="4" fill="${s.c === 'turbo' ? t.turbo : t.other}" stroke="${t.surface}" stroke-width="2"/>
        <text x="${x(60) + 10}" y="${s.ly - 1}" fill="${t.ink}" font-size="${12.5 * fs}" font-weight="${s.c === 'turbo' ? 700 : 400}">${s.name}</text>
        <text x="${x(60) + 10}" y="${s.ly + 13 * fs}" fill="${t.muted}" font-size="${11.5 * fs}">${b[g][s.c].mbps.median.toFixed(1)} MB/s</text>`).join('');
    return `<svg width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" font-family="${FONT.replace(/'/g, '')}">${grid}${xs}${lines}${labels}</svg>`;
}

// The headline numbers as three tiles, with the official app's figure under each.
function statsHtml(b, theme) {
    const t = THEMES[theme];
    const h = b.healthy, n = b.thin;
    const sec = (v) => v === 0 ? '0 s' : `${v.toFixed(1)} s`;
    const tiles = [
        [`${h.turbo.firstFrameS.median.toFixed(1)} s`, 'click to first frame', `official ${h.official.firstFrameS.median.toFixed(1)} s · thin swarm ${n.turbo.firstFrameS.median.toFixed(1)} s vs ${n.official.firstFrameS.median.toFixed(1)} s`],
        [`${Math.round(h.turbo.mbps.median / h.official.mbps.median)}×`, 'download speed', `${h.turbo.mbps.median.toFixed(1)} vs ${h.official.mbps.median.toFixed(1)} MB/s · thin swarm ${n.turbo.mbps.median.toFixed(1)} vs ${n.official.mbps.median.toFixed(1)}`],
        [`${h.turbo.stallSeconds.median.toFixed(1)} s`, 'frozen in the first minute', `official ${sec(h.official.stallSeconds.median)} · thin swarm ${sec(n.turbo.stallSeconds.median)} vs ${sec(n.official.stallSeconds.median)}`],
    ];
    return `<!doctype html><html><head><meta charset="utf-8"><style>
        * { box-sizing: border-box; margin: 0; }
        body { width: 800px; background: ${t.surface}; color: ${t.ink}; font: 14px/1.35 ${FONT}; padding: 6px 0; display: flex; gap: 14px; }
        .tile { flex: 1; border: 1px solid ${t.faint}; border-radius: 14px; padding: 18px 20px 16px; }
        .big { font-size: 40px; font-weight: 700; letter-spacing: -1px; color: ${t.turbo}; font-variant-numeric: tabular-nums; line-height: 1.1; }
        .what { font-size: 15px; font-weight: 600; margin-top: 2px; }
        .vs { color: ${t.muted}; font-size: 12.5px; margin-top: 8px; }
    </style></head><body>${tiles.map(([big, what, vs]) => `<div class="tile"><div class="big">${big}</div><div class="what">${what}</div><div class="vs">${vs}</div></div>`).join('')}</body></html>`;
}

// How it's built: every layer of Stremio, forked and improved, bundled by one installer.
const STACK = [
    // Top to bottom.
    { name: 'Windows app', fork: 'stremio-shell-ng', tags: ['ed25519-signed self-updates', 'starts and adopts the server', 'mpv player'] },
    { name: 'Web UI', fork: 'stremio-web', tags: ['races the sources', 'swaps a slow source mid-episode', 'downloads'] },
    { name: 'Streaming server', fork: 'rivulet-engine', tags: ['replaces the closed server.js', 'live source tests', 'downloads API'] },
    { name: 'Torrent engine', fork: 'anacrolix/torrent + go-utp', tags: ['6× download speed', 'uTP acks the whole reorder buffer', "player's bytes first, by deadline"] },
];

function stackHtml(theme, iconPng) {
    const dark = theme === 'dark';
    const t = THEMES[theme];
    const img = `data:image/png;base64,${fs.readFileSync(iconPng).toString('base64')}`;
    // Brand gradient steps, top (violet) to bottom (blue).
    const steps = ['#8460ff', '#6c62f2', '#4f5de8', '#2f57e0'];
    const slabs = STACK.map((l, i) => {
        const inset = (STACK.length - 1 - i) * 14;
        const bg = dark ? `linear-gradient(90deg, ${steps[i]}, ${steps[i]}cc)` : `linear-gradient(90deg, ${steps[i]}24, ${steps[i]}12)`;
        return `<div class="slab" style="margin: 0 ${inset}px; background: ${bg}; border-color: ${dark ? 'rgba(255,255,255,.14)' : steps[i] + '55'}">
            <div class="who"><div class="name">${l.name}</div><div class="fork">fork of ${l.fork}</div></div>
            <div class="tags">${l.tags.map((tag, j) => `<span class="tag${j === 0 ? ' lead' : ''}">${tag}</span>`).join('')}</div>
        </div>`;
    }).join('');
    return `<!doctype html><html><head><meta charset="utf-8"><style>
        * { box-sizing: border-box; margin: 0; }
        body { width: 960px; background: ${t.surface}; color: ${t.ink}; font: 14px/1.3 ${FONT}; padding: 18px 24px 22px; }
        .box { position: relative; border: 2px solid ${dark ? '#8476f7' : '#6b5cf0'}; border-radius: 22px; padding: 46px 22px 22px;
            background: ${dark ? 'rgba(132,118,247,.06)' : 'rgba(107,92,240,.035)'}; }
        .badge { position: absolute; top: -17px; left: 50%; transform: translateX(-50%); display: flex; align-items: center; gap: 10px;
            background: linear-gradient(135deg, ${BRAND.top}, ${BRAND.bottom}); color: #fff; border-radius: 999px; padding: 6px 18px 6px 8px;
            font-weight: 600; white-space: nowrap; box-shadow: 0 4px 14px rgba(40,30,120,.3); }
        .badge img { width: 22px; height: 22px; }
        .badge .sep { opacity: .55; }
        .badge code { font: 600 13px Consolas, 'Cascadia Mono', monospace; }
        .stack { display: flex; flex-direction: column; gap: 10px; }
        .slab { display: flex; align-items: center; gap: 18px; padding: 14px 18px; border-radius: 14px; border: 1px solid;
            color: ${dark ? '#fff' : t.ink}; box-shadow: ${dark ? '0 6px 16px rgba(0,0,0,.35)' : '0 3px 10px rgba(60,50,140,.08)'}; }
        .who { width: 300px; flex: none; }
        .name { font-size: 17px; font-weight: 700; }
        .fork { font: 12.5px Consolas, 'Cascadia Mono', monospace; opacity: .78; margin-top: 3px; }
        .tags { display: flex; flex-wrap: wrap; gap: 6px; justify-content: flex-end; flex: 1; }
        .tag { font: 12.5px Consolas, 'Cascadia Mono', monospace; padding: 4px 10px; border-radius: 999px; white-space: nowrap;
            background: ${dark ? 'rgba(255,255,255,.14)' : '#ffffff'}; border: 1px solid ${dark ? 'rgba(255,255,255,.18)' : 'rgba(80,70,180,.22)'}; }
        .tag.lead { font-weight: 700; background: ${dark ? 'rgba(255,255,255,.26)' : '#efeaff'}; }
        .sync { position: absolute; bottom: 8px; right: 22px; font: 12px Consolas, 'Cascadia Mono', monospace; color: ${t.muted}; }
        .box { padding-bottom: 34px; }
    </style></head><body><div class="box">
        <div class="badge"><img src="${img}">StremioTurbo-x64.exe <span class="sep">·</span> one installer <span class="sep">·</span> signed self-updates</div>
        <div class="stack">${slabs}</div>
        <div class="sync">↻ kept on every original project's latest code</div>
    </div></body></html>`;
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
        /* A close-up of the quality picker, with some of the show's art beside it. */
        .shot { height: 490px; overflow: hidden; position: relative; }
        .shot img { position: absolute; left: 0; top: 0; width: 100%; }
    </style></head><body><div class="glow"></div>
        <div class="left">
            <img class="mark" src="${img(iconPng)}">
            <h1>Stremio <span>Turbo</span></h1>
            <p>Stremio for Windows, rebuilt for speed.</p>
        </div>
        <div class="win"><div class="bar"><img src="${img(iconPng)}">Stremio Turbo<span class="ctl">–▢✕</span></div><div class="shot"><img src="${img(appPng)}"></div></div>
    </body></html>`;
}

// ---- feature cards: one per README row, the screen on alternating sides --------------------------
// Each card is a dark brand panel (like the banner) with the text and one key number on one side
// and a framed screen on the other, bleeding off the card's edge. Sizes are CSS pixels; cards
// render at 2x. Light and dark versions differ only in the page colour behind the rounded corners.
const CARD = { w: 640, h: 340, pad: 30, gap: 26, bleed: 26, radius: 18 };
const PAGE = { light: '#ffffff', dark: '#0d1117' };
const FEATURES = [
    { name: 'race', side: 'right', screen: [260, 286], bleed: 10, src: 'feature-race.mp4',
        eyebrow: 'Smart play', title: 'Races the sources',
        body: 'Press Play on a quality and its top three sources are tested live. The best one that keeps up plays; if none does, it steps down a quality and says why.',
        stat: '≥ 1.25×', statLabel: 'the speed the video needs, or it doesn’t play' },
    { name: 'engine', side: 'left', screen: [380, 252],
        eyebrow: 'Torrent engine', title: 'A faster engine',
        body: 'Fixes in the uTP transport, the request queue, storage and peer dialling, each measured before and after.',
        stat: '6×', statLabel: 'the official app’s download speed' },
    { name: 'resume', side: 'right', screen: [400, 240], cropTop: 31, src: 'feature-resume.mp4',
        eyebrow: 'Continue watching', title: 'Resume at once',
        body: 'The top of Continue Watching is prepared in the background. A source that falls behind is swapped mid-episode at the same second.',
        stat: '~1 s', statLabel: 'from Resume to the picture' },
    { name: 'downloads', side: 'left', screen: [400, 258], cropTop: 31, src: 'feature-downloads.mp4',
        eyebrow: 'Offline', title: 'Downloads',
        body: 'An episode or a whole season, each from its fastest source. The next episode downloads while you watch.',
        stat: 'Whole seasons', statLabel: 'and they finish after the app is closed' },
];

// The screen's rectangle in the card: vertically centred, past the card's edge by its bleed.
// cropTop: source pixels cut from the clip's top (the window's title bar).
const bleedOf = (f) => f.bleed ?? CARD.bleed;
function cardScreen(f) {
    const [w, h] = f.screen;
    const x = f.side === 'right' ? CARD.w - w + bleedOf(f) : -bleedOf(f);
    return { x, y: Math.round((CARD.h - h) / 2), w, h };
}

// The card with a hole where the screen goes (a clip is laid under it), or with inner HTML in it.
function cardHtml(f, theme, inner = null) {
    const r = cardScreen(f);
    const textW = CARD.w - (f.screen[0] - bleedOf(f)) - CARD.gap - CARD.pad;
    const textX = f.side === 'right' ? CARD.pad : CARD.w - CARD.pad - textW;
    const k = 10; // the screen's corner radius
    const hole = `M${r.x + k} ${r.y}H${r.x + r.w - k}A${k} ${k} 0 0 1 ${r.x + r.w} ${r.y + k}V${r.y + r.h - k}A${k} ${k} 0 0 1 ${r.x + r.w - k} ${r.y + r.h}H${r.x + k}A${k} ${k} 0 0 1 ${r.x} ${r.y + r.h - k}V${r.y + k}A${k} ${k} 0 0 1 ${r.x + k} ${r.y}Z`;
    const clip = inner === null ? `clip-path: path(evenodd, "M0 0H${CARD.w}V${CARD.h}H0Z ${hole}");` : '';
    const from = f.side === 'right' ? '0% 0%' : '100% 0%';
    return `<!doctype html><html><head><meta charset="utf-8"><style>
        * { margin: 0; box-sizing: border-box; }
        html, body { width: ${CARD.w}px; height: ${CARD.h}px; background: transparent; }
        /* The page colour behind the rounded corners, with the same hole as the card. */
        .page { position: absolute; inset: 0; background: ${PAGE[theme]}; ${clip} }
        .card { position: absolute; inset: 0; border-radius: ${CARD.radius}px; overflow: hidden; font-family: ${FONT}; color: #fff; ${clip}
            background: radial-gradient(120% 150% at ${from}, #3a2a9e 0%, #1b1550 48%, #0c0b11 100%);
            box-shadow: inset 0 0 0 1px rgba(255,255,255,.07); }
        .glow { position: absolute; width: 520px; height: 520px; border-radius: 50%; top: -90px; ${f.side === 'right' ? 'right: -170px' : 'left: -170px'};
            background: radial-gradient(circle, rgba(132,96,255,.38), rgba(32,84,224,0) 65%); }
        .frame { position: absolute; left: ${r.x - 1}px; top: ${r.y - 1}px; width: ${r.w + 2}px; height: ${r.h + 2}px; border-radius: ${k + 1}px;
            border: 1px solid rgba(255,255,255,.16); box-shadow: 0 18px 50px rgba(0,0,0,.55); background: ${inner === null ? 'transparent' : '#0d1117'}; overflow: hidden; }
        .text { position: absolute; left: ${textX}px; width: ${textW}px; top: 0; bottom: 0; display: flex; flex-direction: column; justify-content: center; }
        .eyebrow { font-size: 10.5px; font-weight: 700; letter-spacing: 1.6px; text-transform: uppercase; color: #b7a6ff; }
        h3 { font-size: 25px; font-weight: 700; letter-spacing: -.5px; margin-top: 6px; line-height: 1.1; }
        p { font-size: 12.5px; line-height: 1.5; color: #d9d4ff; margin-top: 10px; }
        .stat { margin-top: 16px; padding-top: 12px; border-top: 1px solid rgba(255,255,255,.12); }
        .big { font-size: 26px; font-weight: 700; letter-spacing: -.5px; line-height: 1.1; font-variant-numeric: tabular-nums;
            background: linear-gradient(90deg, #c9bcff, #7fa6ff); -webkit-background-clip: text; color: transparent; }
        .lbl { font-size: 11px; color: #a9a3c9; margin-top: 3px; }
    </style></head><body><div class="page"></div><div class="card"><div class="glow"></div>
        <div class="frame">${inner ?? ''}</div>
        <div class="text"><div class="eyebrow">${f.eyebrow}</div><h3>${f.title}</h3><p>${f.body}</p>
            <div class="stat"><div class="big">${f.stat}</div><div class="lbl">${f.statLabel}</div></div></div>
    </div></body></html>`;
}

// The engine card's chart: the healthy swarm's first minute, in the dark chart theme. The frame
// bleeds off the card's left edge, so the chart keeps clear of that part.
function engineChart(b) {
    const t = THEMES.dark;
    const f = FEATURES.find((x) => x.name === 'engine');
    const [w, h] = f.screen;
    return `<div style="padding: 12px 14px 0 ${14 + CARD.bleed}px; color: ${t.ink}; font: 12px/1.3 ${FONT}">
        <div style="font-weight: 600; font-size: 12.5px">Downloaded in the first minute</div>
        <div style="color: ${t.muted}; font-size: 10.5px; margin: 1px 0 4px">fresh 4K HDR episode · healthy swarm · median run</div>
        ${speedPanelSvg(b, 'healthy', t, w - 28 - CARD.bleed, h - 52, 0.95)}</div>`;
}

// Lays a clip under the card's hole and writes an animated GIF (2x, 10 fps).
function cardGif(topPng, clip, f, gif) {
    const r = cardScreen(f);
    const S = 2;
    const vf = `color=c=#0c0b11:s=${CARD.w * S}x${CARD.h * S}:r=10[bg];[1:v]${f.cropTop ? `crop=iw:ih-${f.cropTop}:0:${f.cropTop},` : ''}fps=10,scale=${r.w * S}:${r.h * S}:flags=lanczos[m];` +
        `[bg][m]overlay=${r.x * S}:${r.y * S}:shortest=1[v];[v][0:v]overlay=0:0:shortest=1,split[a][b];` +
        `[a]palettegen=max_colors=128:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle`;
    spawnSync(FFMPEG, ['-y', '-hide_banner', '-loglevel', 'error', '-loop', '1', '-i', topPng, '-i', clip, '-filter_complex', vf, gif], { stdio: 'inherit' });
}

// The time of the clip's last scene cut: where the page gives way to the player. With after, the
// first cut past that many seconds instead (a cropped clip also films the race box appearing, and
// the player's own scene changes after the cut).
// Scene cuts (seconds) after a filter, at a threshold.
function sceneCuts(mp4, pre, threshold) {
    const r = spawnSync(FFMPEG, ['-hide_banner', '-i', mp4, '-vf', `${pre},select='gt(scene,${threshold})',showinfo`, '-f', 'null', '-'], { encoding: 'utf8' });
    return [...r.stderr.matchAll(/pts_time:([0-9.]+)/g)].map((m) => Number(m[1]));
}

function playerCut(mp4, after = null) {
    const r = spawnSync(FFMPEG, ['-hide_banner', '-i', mp4, '-vf', "select='gt(scene,0.25)',showinfo", '-f', 'null', '-'], { encoding: 'utf8' });
    const cuts = [...r.stderr.matchAll(/pts_time:([0-9.]+)/g)].map((m) => Number(m[1]));
    if (after !== null) return cuts.find((c) => c > after) ?? null;
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
        const icon = path.join(ROOT, 'shell', 'images', 'stremio.png');
        for (const theme of ['light', 'dark']) {
            await e.render(statsHtml(b, theme), 800, null, path.join(DOCS, `stats-${theme}.png`));
            await e.render(stackHtml(theme, icon), 960, null, path.join(DOCS, `stack-${theme}.png`));
        }
        await e.render(buttonHtml(), 300, 64, path.join(DOCS, 'button-download.png'));
        // The feature cards: a still for the chart, GIFs with the clip under the card's hole.
        const tmp = path.join(RAW, 'cards');
        fs.mkdirSync(tmp, { recursive: true });
        for (const f of FEATURES) {
            for (const theme of ['light', 'dark']) {
                if (!f.src) {
                    await e.render(cardHtml(f, theme, engineChart(b)), CARD.w, CARD.h, path.join(DOCS, `card-${f.name}-${theme}.png`));
                    continue;
                }
                const clip = path.join(src, f.src);
                if (!fs.existsSync(clip)) {
                    console.log('skipped card', f.name, '(not captured)');
                    break;
                }
                const top = path.join(tmp, `${f.name}-${theme}.png`);
                await e.render(cardHtml(f, theme), CARD.w, CARD.h, top);
                const gif = path.join(DOCS, `card-${f.name}-${theme}.gif`);
                cardGif(top, clip, f, gif);
                console.log('wrote', path.relative(ROOT, gif), `${Math.round(fs.statSync(gif).size / 1024)} KB`);
            }
        }
        const app = path.join(src, 'banner-app.png');
        if (fs.existsSync(app)) await e.render(bannerHtml(app, fs.existsSync(icon) ? icon : app), 1280, 640, path.join(DOCS, 'banner.png'));
        else console.log('skipped banner.png: no banner-app.png in', path.relative(ROOT, src));
    } finally {
        await e.close();
    }
    const out = path.join(RAW, set);
    fs.mkdirSync(out, { recursive: true });
    for (const name of ['feature-race', 'feature-resume', 'feature-downloads']) {
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
        // The standalone GIFs stay in out/ (the README shows the feature cards).
        if (path.dirname(gif) !== out) fs.copyFileSync(gif, path.join(out, `${name}.gif`));
        console.log('wrote', path.relative(ROOT, path.join(out, `${name}.gif`)), `${Math.round(fs.statSync(gif).size / 1024)} KB`, cut !== null ? `(blurred from ${cut.toFixed(1)} s)` : '');
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
