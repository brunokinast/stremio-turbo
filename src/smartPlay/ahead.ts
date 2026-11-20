// Download ahead: while an episode plays, the next one is downloaded in the
// background (the server's /downloads), so it later plays from disk without
// buffering, in the best picture that can make it in time.
//
// The rule (the user's): a source fits when it finishes before playback would
// catch it. With L seconds left of the current episode, D the next one's
// runtime and S its file size, a source downloading at r stays ahead of the
// playhead for the whole next episode when r × (L + D) ≥ S: at constant rates
// the closest point is the episode's end, so the download may keep going while
// the next episode plays. Only AHEAD_MARGIN of a measured rate is counted, as
// a swarm can slow down. The highest-bitrate source that fits in the
// remembered quality is chosen; when none does, the next resolution down is
// tried, and so on.

import { useCallback, useEffect, useRef, useState } from 'react';
import type { StreamGroup } from 'stremio/routes/Details/Streams/useStreamsByAddon';
import { groupByQuality, rankTiers, resolutionOf, fallbackTier } from './groupByQuality';
import type { RankedSource, RankedTier } from './groupByQuality';
import { preload, remove, stats } from './serverApi';
import { knownRate, RateMeter } from './supply';
import { getLastQuality, getQuality } from './qualityMemory';
import { addDownload, listDownloads, markWatched, removeDownload } from './downloads';
import type { Download } from './downloads';

const SETTING_KEY = 'smartPlay.downloadAhead';
const MEMORY_KEY = 'smartPlay.ahead';
// Share of a measured rate counted on.
const AHEAD_MARGIN = 0.8;
// Seconds of steady playback before the next episode is planned.
const PLAN_AFTER = 60;
// How often planning runs again while playing (to queue the episode after
// the next once the next one is on disk).
const REPLAN_EVERY = 5 * 60 * 1000;
// Share of an episode after which its download counts as watched.
const WATCHED_AT = 0.9;
// How long a download runs before its actual average rate is checked.
const CHECK_AFTER = 4 * 60 * 1000;
// The same for a download queued on trial (an episode of the torrent being
// played), whose own progress is its only measurement.
const CHECK_TRIAL = 60 * 1000;
// Episodes ahead downloaded at most.
const MAX_AHEAD = 2;
// Sources whose rate is measured when none is remembered, and for how long.
const MEASURE_SOURCES = 3;
const MEASURE_FOR = 30 * 1000;
const MEASURE_POLL = 2000;
// Where in the file the measurement downloads (fraction).
const MEASURE_AT = 0.5;
// Downloaded-ahead episodes remembered (videos -> source) this long.
const MEMORY_TTL = 30 * 24 * 60 * 60 * 1000;

// ---- setting --------------------------------------------------------------

const isAheadEnabled = (): boolean => {
    try {
        return localStorage.getItem(SETTING_KEY) !== 'off';
    } catch (_e) {
        return true;
    }
};

const useAheadToggle = () => {
    const [checked, setChecked] = useState(isAheadEnabled);
    const onClick = useCallback(() => {
        setChecked((current) => {
            try {
                localStorage.setItem(SETTING_KEY, current ? 'off' : 'on');
            } catch (_e) {
                // Storage unavailable: the default (on) stays.
            }
            return !current;
        });
    }, []);
    return { checked, onClick };
};

// ---- which source each episode was downloaded from --------------------------

type AheadEntry = {
    infoHash: string,
    fileIdx: number,
    // When queued, and the bytes then on disk: the download's actual average
    // rate since is checked against what it needs (plan).
    at: number,
    bytes?: number,
    // Queued without a measurement (same torrent as the one playing): checked
    // after CHECK_TRIAL instead of CHECK_AFTER.
    trial?: boolean,
    // Sources found too slow for this episode, not chosen again.
    slow?: string[],
    // The user cancelled this episode's download (Downloads screen): it isn't
    // started again by itself.
    cancelled?: boolean,
};

const memoryKey = (metaId: string, videoId: string) => `${metaId} ${videoId}`;

const loadMemory = (): Record<string, AheadEntry> => {
    try {
        const parsed = JSON.parse(localStorage.getItem(MEMORY_KEY) ?? '{}');
        return parsed !== null && typeof parsed === 'object' ? parsed : {};
    } catch (_e) {
        return {};
    }
};

const rememberAhead = (metaId: string, videoId: string, entry: Omit<AheadEntry, 'at'>) => {
    const now = Date.now();
    const kept = Object.entries(loadMemory()).filter(([key, e]) => now - e.at < MEMORY_TTL && key !== memoryKey(metaId, videoId));
    kept.push([memoryKey(metaId, videoId), { ...entry, at: now }]);
    try {
        localStorage.setItem(MEMORY_KEY, JSON.stringify(Object.fromEntries(kept)));
    } catch (_e) {
        // Storage unavailable: the copy still plays through the race.
    }
};

// The source an episode was downloaded ahead from, if any.
const aheadSource = (metaId: string, videoId: string): AheadEntry | null => {
    return loadMemory()[memoryKey(metaId, videoId)] ?? null;
};

// Marks an episode's download cancelled by the user, so it isn't started
// again by itself.
const cancelAhead = (metaId: string, videoId: string) => {
    const known = aheadSource(metaId, videoId);
    rememberAhead(metaId, videoId, { infoHash: '', fileIdx: -1, slow: known?.slow, cancelled: true });
};

// The downloaded source of an episode (not a placeholder holding only slow sources).
const downloadedSource = (metaId: string, videoId: string): AheadEntry | null => {
    const entry = aheadSource(metaId, videoId);
    return entry !== null && entry.infoHash !== '' ? entry : null;
};

// ---- the rule -------------------------------------------------------------

type AheadChoice = {
    source: RankedSource,
    tierLabel: string,
    // Seconds the download is expected to take at the full measured rate (0:
    // already on disk).
    eta: number,
    // Queued on trial: its rate is unknown until its first check.
    trial: boolean,
};

// Whether a source of `size` bytes downloading at `rate` finishes ahead of
// playback within a window of `windowSeconds` (time left + next runtime).
const fitsWindow = (size: number, rate: number, windowSeconds: number, margin = AHEAD_MARGIN): boolean => {
    return rate > 0 && windowSeconds > 0 && rate * margin * windowSeconds >= size;
};

// Source groups to try, best first: the remembered quality's tier (or the
// closest offered), then each lower resolution in turn (all its tiers).
const aheadGroups = (tiers: RankedTier[], startKey: string | null): { label: string, sources: RankedSource[] }[] => {
    const start = tiers.find((tier) => tier.key === startKey) ?? fallbackTier(tiers, startKey);
    if (start === undefined) {
        return [];
    }
    const startResolution = resolutionOf(start.key);
    const lower = Array.from(new Set(tiers
        .map((tier) => resolutionOf(tier.key))
        .filter((resolution): resolution is number => resolution !== null && startResolution !== null && resolution < startResolution)))
        .sort((a, b) => b - a);
    return [
        { label: start.label, sources: start.sources },
        ...lower.map((resolution) => {
            const group = tiers.filter((tier) => resolutionOf(tier.key) === resolution);
            return { label: group[0].label, sources: group.flatMap((tier) => tier.sources) };
        }),
    ];
};

// The highest-bitrate (largest) source of a group that fits the window.
const chooseFitting = (sources: RankedSource[], rateOf: (source: RankedSource) => number | null, windowSeconds: number): { source: RankedSource, eta: number } | null => {
    let best: { source: RankedSource, eta: number } | null = null;
    sources.forEach((source) => {
        const rate = rateOf(source);
        if (source.size === null || rate === null || source.fileIdx === null || !fitsWindow(source.size, rate, windowSeconds)) {
            return;
        }
        if (best === null || source.size > best.source.size!) {
            best = { source, eta: source.size / rate };
        }
    });
    return best;
};

// ---- fetching an episode's sources ------------------------------------------

type ManifestResource = string | { name: string, types?: string[], idPrefixes?: string[] };

// The installed addons that serve streams for this type and id.
const streamAddons = (addons: Addon[], type: string, id: string): Addon[] => {
    return addons.filter(({ manifest }) => {
        const m = manifest as AddonManifest & { resources?: ManifestResource[], idPrefixes?: string[] };
        return (m.resources ?? []).some((resource) => {
            const name = typeof resource === 'string' ? resource : resource.name;
            const types = typeof resource === 'string' ? m.types : resource.types ?? m.types;
            const prefixes = typeof resource === 'string' ? m.idPrefixes : resource.idPrefixes ?? m.idPrefixes;
            return name === 'stream' && (types ?? []).includes(type) && (prefixes === undefined || prefixes.some((prefix) => id.startsWith(prefix)));
        });
    });
};

// Asks the addons for a video's streams, as core does when the episode is
// opened. Core renames two fields of the addon protocol, and groupByQuality
// reads core's names: `title` (where Torrentio puts the size and seeders) is
// `description`, and torrent `sources` are `announce`.
const fetchStreams = async (addons: Addon[], type: string, videoId: string): Promise<StreamGroup[]> => {
    const results = await Promise.all(streamAddons(addons, type, videoId).map(async (addon) => {
        const base = addon.transportUrl.replace(/\/manifest\.json$/, '');
        try {
            const response = await fetch(`${base}/stream/${encodeURIComponent(type)}/${encodeURIComponent(videoId)}.json`);
            const body = response.ok ? await response.json() : null;
            const streams = Array.isArray(body?.streams) ? body.streams as (Stream & { title?: string, sources?: string[] })[] : [];
            return {
                addon,
                streams: streams.map((stream) => ({
                    ...stream,
                    description: stream.description ?? stream.title ?? '',
                    announce: stream.announce ?? stream.sources ?? [],
                })),
            };
        } catch (_e) {
            return { addon, streams: [] };
        }
    }));
    return results;
};

// Measures sources' download rates by opening them for MEASURE_FOR (the
// server fetches their first pieces).
const measure = async (baseUrl: string, sources: RankedSource[], meter: RateMeter): Promise<void> => {
    // A baseline first: a fast swarm fetches the first pieces within one poll,
    // and the meter needs a sample from before (a torrent not open yet starts
    // from 0).
    const start = Date.now();
    await Promise.all(sources.map(({ infoHash }) => stats(baseUrl, infoHash).then((s) => meter.add(infoHash, start, s?.downloaded ?? 0))));
    // Primed around the middle of the file: its first and last pieces may be
    // on disk already (an earlier preload or probe), and then nothing would
    // download to measure.
    sources.forEach(({ infoHash, fileIdx, trackers }) => preload(baseUrl, infoHash, fileIdx, trackers.length > 0 ? trackers : [`dht:${infoHash}`], MEASURE_AT));
    const until = Date.now() + MEASURE_FOR;
    while (Date.now() < until) {
        await new Promise((resolve) => setTimeout(resolve, MEASURE_POLL));
        await Promise.all(sources.map(({ infoHash }) => stats(baseUrl, infoHash).then((s) => {
            if (s !== null) {
                meter.add(infoHash, Date.now(), s.downloaded);
            }
        })));
    }
};

// ---- planning -------------------------------------------------------------

type PlanContext = {
    baseUrl: string,
    addons: Addon[],
    meta: { id: string, type: string, videos: Video[], name?: string, poster?: string | null },
    videoId: string,
    // The torrent being played.
    playingInfoHash: string | null,
    // Seconds left of the current episode, and an episode's runtime.
    left: number,
    runtime: number,
};

type PlanResult =
    | { kind: 'queued', video: Video, choice: AheadChoice, stepped: boolean, replaced: { gb: number, rate: number, needed: number } | null }
    | { kind: 'none', video: Video }
    | { kind: 'full', video: Video, reason: string }
    | null;

const episodeOrder = (a: Video, b: Video) => (a.season ?? 0) - (b.season ?? 0) || (a.episode ?? 0) - (b.episode ?? 0);

// The episodes after the current one, in order (specials only after a special).
const nextEpisodes = (videos: Video[], videoId: string): Video[] => {
    const current = videos.find((video) => video.id === videoId);
    if (current === undefined) {
        return [];
    }
    return videos
        .filter((video) => (current.season === 0 || video.season !== 0) && episodeOrder(video, current) > 0)
        .sort(episodeOrder);
};

const episodeLabel = (video: Video) => {
    const pad = (n: number | undefined) => String(n ?? 0).padStart(2, '0');
    return video.season !== undefined && video.episode !== undefined ? `S${pad(video.season)}E${pad(video.episode)}` : video.title;
};

let planning = false;

// The last decision's inputs (sessionStorage smartPlay.aheadTrace), for
// diagnosis: each group's sources with size, rate and whether they fit.
const trace = (entry: object) => {
    try {
        sessionStorage.setItem('smartPlay.aheadTrace', JSON.stringify({ at: new Date().toISOString(), ...entry }));
    } catch (_e) {
        // Storage unavailable: no trace.
    }
};

const describe = (sources: RankedSource[], rateOf: (source: RankedSource) => number | null, windowSeconds: number, basis: (source: RankedSource) => string) => sources.map((source) => {
    const rate = rateOf(source);
    return {
        infoHash: source.infoHash.slice(0, 8),
        fileIdx: source.fileIdx,
        gb: source.size !== null ? Math.round(source.size / 1e8) / 10 : null,
        mbps: rate !== null && Number.isFinite(rate) ? Math.round(rate / 1e5) / 10 : null,
        basis: basis(source),
        fits: source.size !== null && rate !== null && fitsWindow(source.size, rate, windowSeconds),
    };
});

// A download's actual average rate since it was queued, once CHECK_AFTER has
// passed, else null.
const averageRate = (known: AheadEntry, entry: Download): number | null => {
    const seconds = (Date.now() - known.at) / 1000;
    return known.bytes !== undefined && seconds * 1000 >= (known.trial ? CHECK_TRIAL : CHECK_AFTER) ? Math.max(0, entry.bytesCompleted - known.bytes) / seconds : null;
};

// Queues the first of the next MAX_AHEAD episodes not downloaded yet, one at
// a time (an earlier one still downloading waits). A running download whose
// actual average rate can't finish it in time is replaced by a smaller source
// that can (a short measurement can overrate a swarm: the first pieces come
// in a burst from fresh peers).
const plan = async (ctx: PlanContext): Promise<PlanResult> => {
    const downloads = await listDownloads(ctx.baseUrl);
    if (downloads === null) {
        return null;
    }
    const upcoming = nextEpisodes(ctx.meta.videos, ctx.videoId).slice(0, MAX_AHEAD);
    for (let k = 0; k < upcoming.length; k++) {
        const video = upcoming[k];
        // Time until this episode has finished playing.
        const windowSeconds = ctx.left + (k + 1) * ctx.runtime;
        if (aheadSource(ctx.meta.id, video.id)?.cancelled === true) {
            continue;
        }
        const known = downloadedSource(ctx.meta.id, video.id);
        const entry: Download | undefined = known !== null ? downloads.find((d) => d.infoHash === known.infoHash && d.fileIdx === known.fileIdx) : undefined;
        let replaced: { gb: number, rate: number, needed: number } | null = null;
        if (known !== null && entry !== undefined) {
            if (entry.state === 'complete') {
                continue;
            }
            const rate = averageRate(known, entry);
            const left = Math.max(0, entry.size - entry.bytesCompleted);
            trace({ video: video.id, check: true, windowSeconds: Math.round(windowSeconds), left: Math.round(ctx.left), runtime: Math.round(ctx.runtime), k, gbLeft: Math.round(left / 1e8) / 10, avgMbps: rate !== null ? Math.round(rate / 1e5) / 10 : null });
            if (rate === null || entry.state !== 'downloading' || fitsWindow(left, rate, windowSeconds)) {
                return null;
            }
            // Too slow: replaced below, and not chosen again for this episode.
            await removeDownload(ctx.baseUrl, entry.infoHash, entry.fileIdx);
            rememberAhead(ctx.meta.id, video.id, { infoHash: '', fileIdx: -1, slow: [...(known.slow ?? []), known.infoHash] });
            replaced = { gb: entry.size / 1e9, rate, needed: left / (AHEAD_MARGIN * windowSeconds) };
        }
        const slow = new Set(aheadSource(ctx.meta.id, video.id)?.slow ?? []);
        const groups = await fetchStreams(ctx.addons, ctx.meta.type, video.id);
        const tiers = rankTiers(groupByQuality(groups, null), new Map());
        const startKey = getQuality(ctx.meta.id) ?? getLastQuality();
        const meter = new RateMeter();
        const keep = new Set<string>(downloads.map((d) => d.infoHash));
        if (ctx.playingInfoHash !== null) {
            keep.add(ctx.playingInfoHash);
        }
        // A copy already complete on disk fits. A source in the torrent being
        // played can't be measured by opening it (the server holds a playing
        // torrent's other files back, so a probe gets almost nothing): it
        // counts as fitting on trial, and its own download progress is
        // checked after CHECK_TRIAL.
        const onDisk = new Set(downloads.filter((d) => d.state === 'complete').map((d) => `${d.infoHash} ${d.fileIdx}`));
        const isOnDisk = (source: RankedSource) => onDisk.has(`${source.infoHash} ${source.fileIdx}`);
        const isTrial = (source: RankedSource) => source.infoHash === ctx.playingInfoHash && !isOnDisk(source);
        // Otherwise only a rate measured now qualifies a source; a remembered
        // one can only rule it out (one learnt from a read of data already on
        // disk is far above the swarm's rate). Sources found too slow are
        // skipped.
        const rateOf = (source: RankedSource) => slow.has(source.infoHash) ? null :
            isOnDisk(source) || isTrial(source) ? Infinity : meter.rate(source.infoHash);
        const basis = (source: RankedSource) => slow.has(source.infoHash) ? 'slow' : isOnDisk(source) ? 'disk' : isTrial(source) ? 'trial' : 'measured';
        const ruledOut = (source: RankedSource) => {
            const remembered = knownRate(new RateMeter(), source, ctx.runtime);
            return slow.has(source.infoHash) || (remembered !== null && source.size !== null && !fitsWindow(source.size, remembered.rate, windowSeconds));
        };
        const order = aheadGroups(tiers, startKey);
        // Opened only to measure: closed once the choice is made, except the
        // torrent being played, existing downloads and the one chosen.
        const measured = new Set<string>();
        const closeMeasured = (chosen: string | null) => measured.forEach((infoHash) => {
            if (infoHash !== chosen && !keep.has(infoHash)) {
                remove(ctx.baseUrl, infoHash);
            }
        });
        for (let g = 0; g < order.length; g++) {
            const group = order[g];
            const unknown = group.sources
                .filter((source) => source.size !== null && source.fileIdx !== null && rateOf(source) === null && !ruledOut(source))
                .sort((a, b) => b.size! - a.size!)
                .slice(0, MEASURE_SOURCES);
            if (unknown.length > 0) {
                unknown.forEach(({ infoHash }) => measured.add(infoHash));
                await measure(ctx.baseUrl, unknown, meter);
            }
            const fit = chooseFitting(group.sources, rateOf, windowSeconds);
            trace({ video: video.id, windowSeconds: Math.round(windowSeconds), left: Math.round(ctx.left), runtime: Math.round(ctx.runtime), k, group: group.label, sources: describe(group.sources, rateOf, windowSeconds, basis), chosen: fit?.source.infoHash.slice(0, 8) ?? null });
            if (fit === null) {
                continue;
            }
            const { source } = fit;
            let added: Download;
            try {
                added = await addDownload(ctx.baseUrl, source.infoHash, source.fileIdx!, source.trackers.length > 0 ? source.trackers : [`dht:${source.infoHash}`], {
                    origin: 'next',
                    meta: {
                        metaId: ctx.meta.id,
                        videoId: video.id,
                        type: ctx.meta.type,
                        name: ctx.meta.name,
                        poster: ctx.meta.poster ?? undefined,
                        season: video.season ?? undefined,
                        episode: video.episode ?? undefined,
                        title: video.title ?? undefined,
                    },
                });
            } catch (e) {
                closeMeasured(null);
                return { kind: 'full', video, reason: e instanceof Error ? e.message : String(e) };
            }
            closeMeasured(source.infoHash);
            // What's left to fetch (part may be on disk already), at the full rate.
            const left = Math.max(0, added.size - added.bytesCompleted);
            const done = added.state === 'complete' || left === 0;
            const trial = !done && isTrial(source);
            rememberAhead(ctx.meta.id, video.id, { infoHash: source.infoHash, fileIdx: source.fileIdx!, bytes: added.bytesCompleted, trial, slow: Array.from(slow) });
            const rate = rateOf(source);
            const eta = done ? 0 : rate !== null && Number.isFinite(rate) && rate > 0 ? left / rate : fit.eta;
            return { kind: 'queued', video, choice: { source, tierLabel: group.label, eta, trial }, stepped: g > 0, replaced };
        }
        closeMeasured(null);
        if (order.length === 0) {
            trace({ video: video.id, windowSeconds: Math.round(windowSeconds), tiers: tiers.length, groups: 0 });
        }
        return { kind: 'none', video };
    }
    return null;
};

type Notify = (result: Exclude<PlanResult, null>, label: string) => void;

const runPlan = (ctx: PlanContext, notify: Notify, done?: (result: PlanResult) => void) => {
    if (planning || !isAheadEnabled()) {
        return;
    }
    planning = true;
    plan(ctx)
        .then((result) => {
            if (result !== null) {
                notify(result, episodeLabel(result.video));
            }
            done?.(result);
        })
        .catch(() => undefined)
        .finally(() => {
            planning = false;
        });
};

type HookOptions = {
    baseUrl: string | null,
    addons: Addon[],
    meta: { id: string, type: string, videos: Video[], runtime?: string | null, name?: string, poster?: string | null } | null,
    videoId: string | null,
    playingInfoHash: string | null,
    playingFileIdx: number | null,
    // Player time and duration in milliseconds.
    time: number | null,
    duration: number | null,
    paused: boolean | null,
    notify: Notify,
};

// Player: plans the next episodes after PLAN_AFTER seconds of steady playback,
// again every REPLAN_EVERY, and on leaving an episode played for a while.
const useDownloadAhead = ({ baseUrl, addons, meta, videoId, playingInfoHash, playingFileIdx, time, duration, paused, notify }: HookOptions) => {
    const played = useRef(0);
    const marked = useRef(false);
    const lastTime = useRef<number | null>(null);
    const lastAt = useRef<number | null>(null);
    const lastPlan = useRef<number | null>(null);
    // Until the next plan: REPLAN_EVERY, or just past CHECK_TRIAL after a
    // download was queued on trial (counted from when it was queued).
    const replanAfter = useRef(REPLAN_EVERY);
    const ctxRef = useRef<PlanContext | null>(null);
    const notifyRef = useRef(notify);
    notifyRef.current = notify;

    useEffect(() => {
        played.current = 0;
        lastTime.current = null;
        lastAt.current = null;
        lastPlan.current = null;
        marked.current = false;
    }, [videoId]);

    // An episode played past WATCHED_AT: its download (if it is one) is
    // deleted first when space is needed.
    useEffect(() => {
        if (!marked.current && baseUrl !== null && playingInfoHash !== null && playingFileIdx !== null && time !== null && duration !== null && duration > 0 && time / duration >= WATCHED_AT) {
            marked.current = true;
            markWatched(baseUrl, playingInfoHash, playingFileIdx);
        }
    }, [baseUrl, playingInfoHash, playingFileIdx, time, duration]);

    ctxRef.current = baseUrl !== null && meta !== null && meta.type === 'series' && videoId !== null && time !== null && duration !== null && duration > 0 ?
        { baseUrl, addons, meta, videoId, playingInfoHash, left: Math.max(0, (duration - time) / 1000), runtime: duration / 1000 }
        :
        null;

    useEffect(() => {
        if (time === null) {
            return;
        }
        const now = Date.now();
        const prev = lastTime.current;
        const prevAt = lastAt.current;
        lastTime.current = time;
        lastAt.current = now;
        // Steady playback only: the player's time moving forward while playing,
        // by about the wall time passed (its updates come at uneven intervals;
        // a seek jumps further).
        if (prev !== null && prevAt !== null && paused === false && time > prev && time - prev <= (now - prevAt) * 2 + 1000) {
            played.current += Math.min(time - prev, now - prevAt) / 1000;
        }
        const due = lastPlan.current === null ? played.current >= PLAN_AFTER : now - lastPlan.current >= replanAfter.current && paused === false;
        if (due && ctxRef.current !== null) {
            lastPlan.current = now;
            replanAfter.current = REPLAN_EVERY;
            runPlan(ctxRef.current, notifyRef.current, (result) => {
                if (result?.kind === 'queued' && result.choice.trial) {
                    lastPlan.current = Date.now();
                    replanAfter.current = CHECK_TRIAL + 5000;
                }
            });
        }
    }, [time, paused]);

    // Leaving an episode played for a while (stop before PLAN_AFTER): plan for
    // the rest of it anyway.
    useEffect(() => () => {
        if (lastPlan.current === null && played.current >= 10 && ctxRef.current !== null) {
            runPlan(ctxRef.current, notifyRef.current);
        }
    }, [videoId]);
};

export type { AheadChoice, PlanResult };
export { fitsWindow, aheadGroups, chooseFitting, cancelAhead, downloadedSource, isAheadEnabled, useAheadToggle, useDownloadAhead, AHEAD_MARGIN };
