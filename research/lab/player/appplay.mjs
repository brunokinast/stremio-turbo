// Real-app play: drive the Stremio shell's web UI over the WebView2 DevTools protocol.
// usage: node appplay.mjs <detail hash route> <text that identifies the stream> [secs=120] [dwell=0]
//   e.g. node appplay.mjs "#/detail/series/tt11198330/tt11198330%3A2%3A1" "RGzsRutracker" 120
//   "tier:<label>" (e.g. "tier:4k DV | HDR") presses that quality row's smart-play button instead
//   ("tier:*": the first row, the best quality offered);
//   dwell waits that many seconds on the page, once the target is shown, before pressing it.
//   "resume" (step 6g): waits dwell seconds on the board instead, then opens the route (the
//   details page a continue-watching tile opens) and presses its Resume button (smart play's
//   autoplay of the remembered quality).
// The shell must have been started with
//   WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--remote-debugging-port=9222
// Opens the episode, clicks the first stream whose text contains every '|'-separated part of the
// identifier, mutes with the player's M shortcut (checked against the mute button's title), plays
// for secs counted from the click, then leaves the player so playback stops. Prints a JSON log.
import fs from 'node:fs';
import path from 'node:path';

const [route, ident, secsArg, dwellArg] = process.argv.slice(2);
if (!route || !ident) {
    console.error('usage: node appplay.mjs <route> <stream text> [secs]');
    process.exit(2);
}
const secs = Number(secsArg || 120);
const dwell = Number(dwellArg || 0);
const tier = ident.startsWith('tier:') ? ident.slice(5) : null;
const resume = ident === 'resume';
const t0 = Date.now();
const log = [];
const note = (name, extra = {}) => {
    const e = { t: (Date.now() - t0) / 1000, name, ...extra };
    log.push(e);
    console.error(JSON.stringify(e));
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// Every locale's "unmute" label: once muted, the mute button's title is one of these.
const trDir = path.join(import.meta.dirname, '..', '..', '..', 'stremio-web', 'node_modules', 'stremio-translations');
const locales = fs.readdirSync(trDir).filter((f) => f.endsWith('.json'))
    .map((f) => JSON.parse(fs.readFileSync(path.join(trDir, f), 'utf8')));
const unmuteLabels = new Set(locales.map((l) => l.PLAYER_UNMUTE).filter(Boolean));
const volumeLabels = [...new Set([...unmuteLabels, ...locales.map((l) => l.PLAYER_MUTE).filter(Boolean)])];

const targets = await (await fetch('http://127.0.0.1:9222/json')).json();
const page = targets.find((t) => t.type === 'page' && t.url.startsWith('http://127.0.0.1:8080'));
if (!page) throw new Error('no web UI page on the DevTools port: ' + targets.map((t) => t.url).join(', '));
const ws = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
let nextId = 0;
const pending = new Map();
ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && pending.has(msg.id)) { pending.get(msg.id)(msg); pending.delete(msg.id); }
};
const send = (method, params = {}) => new Promise((r) => {
    const id = ++nextId;
    pending.set(id, r);
    ws.send(JSON.stringify({ id, method, params }));
});
const evaluate = async (expr) => {
    const r = await send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true });
    if (r.result?.exceptionDetails) throw new Error(JSON.stringify(r.result.exceptionDetails));
    return r.result?.result?.value;
};
// The mute button (found by its title: the icon's markup has no name) and its title, or null while the player is not up or the video state is not known yet
// (the button is disabled then, and the shortcut does nothing).
const muteTitle = () => evaluate(`(() => { const b = [...document.querySelectorAll('[class*="control-bar-button"]')]
    .find((b) => ${JSON.stringify(volumeLabels)}.includes(b.title));
    return b && !b.className.includes('disabled') ? b.title : null; })()`);

// Via the board: re-entering the route the page is already on leaves the streams unrequested.
await evaluate(`performance.clearResourceTimings(); performance.setResourceTimingBufferSize(2000); location.hash = '#/'`);
note('board');
await sleep(resume ? dwell * 1000 : 2000);
await evaluate(`window.__appplayOpenedAll = false; location.hash = ${JSON.stringify(route)}`);
note('opened-detail', { route });
const parts = ident.split('|');
let clicked = null;
if (resume) {
    const resumeLabel = locales.map((l) => l.LIBRARY_RESUME).filter(Boolean);
    // The innermost element whose text starts with Resume (its wrapper holds Trailer and Share too).
    const find = `[...document.querySelectorAll('[class*="action"], button, a, [tabindex]')].filter((b) => ${JSON.stringify(resumeLabel)}.some((l) => b.innerText.trim().startsWith(l)) && !/Trailer|Share/.test(b.innerText) && !b.closest('[class*="board"]')).sort((a, b) => a.innerText.length - b.innerText.length)[0]`;
    for (let i = 0; i < 120 && !clicked; i++) {
        await sleep(500);
        clicked = await evaluate(`(() => { if (location.hash !== ${JSON.stringify(route)}) return null; const b = ${find}; if (!b) return null; b.click(); return b.innerText.slice(0, 100); })()`);
    }
    if (!clicked) throw new Error('Resume button not found');
}
if (tier !== null) {
    // The smart-play row's button carries the tier label as its title.
    // Only inside the quality rows: the board's "Continue Watching" button also has a play class.
    const find = `[...document.querySelectorAll('[class*="quality-rows"] [title]')].find((b) => (${JSON.stringify(tier)} === '*' || b.title === ${JSON.stringify(tier)}) && /(^|\s)play-/.test(b.className))`;
    let shown = false;
    for (let i = 0; i < 120 && !shown; i++) {
        await sleep(500);
        shown = await evaluate(`location.hash === ${JSON.stringify(route)} && !!(${find})`);
    }
    if (!shown) throw new Error('quality row not found: ' + tier);
    note('row-shown', { tier });
    await sleep(dwell * 1000);
    clicked = await evaluate(`(() => { const b = ${find}; if (!b || location.hash !== ${JSON.stringify(route)}) return null; b.click(); return b.innerText.slice(0, 300) || b.title; })()`);
    // Never fall through to the stream-list search with a tier target.
    if (!clicked) throw new Error('quality row gone before the click: ' + tier);
}
for (let i = 0; i < 120 && !clicked && !resume; i++) {
    await sleep(500);
    clicked = await evaluate(`(() => {
        const parts = ${JSON.stringify(parts)};
        const a = [...document.querySelectorAll('a')].find((a) => parts.every((p) => a.innerText.includes(p)));
        if (!a) {
            // The fork groups streams by quality; open the full list (once: it toggles).
            const all = document.querySelector('[class*="all-streams"]');
            if (all && !window.__appplayOpenedAll) { window.__appplayOpenedAll = true; all.click(); }
            return null;
        }
        a.click();
        return a.innerText.slice(0, 300);
    })()`);
}
if (!clicked) throw new Error('stream not found in the list');
const tClick = Date.now();
note('clicked-stream', { text: clicked });

let muted = false;
// Sound can only start with the first frame, which is also when the button enables; poll fast.
while (!muted && Date.now() - tClick < 60000) {
    await sleep(100);
    const title = await muteTitle();
    if (title === null) continue; // player not up yet
    if (unmuteLabels.has(title)) { muted = true; break; }
    for (const type of ['keyDown', 'keyUp']) {
        await send('Input.dispatchKeyEvent', { type, key: 'm', code: 'KeyM', windowsVirtualKeyCode: 77 });
    }
    await sleep(250);
    muted = unmuteLabels.has(await muteTitle());
}
note(muted ? 'muted' : 'mute-failed');
if (!muted) { // never play with sound: leave at once
    await evaluate(`history.back()`);
    ws.close();
    console.log(JSON.stringify({ route, ident, secs, clickedAt: tClick, muted, log }, null, 1));
    process.exit(1);
}

while (Date.now() - tClick < secs * 1000) await sleep(1000);
await evaluate(`history.back()`); // leave the player: the shell stops mpv
await sleep(2000);
note('left-player', { hash: await evaluate('location.hash') });
ws.close();
console.log(JSON.stringify({ route, ident, secs, clickedAt: tClick, muted, log }, null, 1));
