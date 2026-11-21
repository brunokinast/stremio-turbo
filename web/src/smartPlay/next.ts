// Copyright (C) 2017-2026 Smart code 203358507

// The Next slot: while an episode plays, the next one is downloaded
// in the background (a kept record, origin "next"), so it later plays from
// disk without buffering.
//
// It is chosen by the shared picker (picker.ts) with the shared health rule
// (health.ts): the time it has is what's left of this episode plus its own
// runtime (it may keep downloading while it plays). The watch then checks it
// every 10 s; a download unhealthy for 30 s in a row is replaced by a new
// pick that skips it, and each decision is said in a toast (notify.ts).

import { useCallback, useEffect, useRef, useState } from 'react';
import { getEpisodeLabel } from 'stremio/routes/Details/format';
import { RateMeter } from './supply';
import { addDownload, isAutomatic, listDownloads, markWatched, removeDownload } from './downloads';
import type { Download } from './downloads';
import { qualitySteps } from './candidates';
import { episodeTiers, startQuality } from './addonStreams';
import { pick } from './picker';
import { healthRatio, isHealthy, need } from './health';
import { HealthWatch, WATCH_EVERY, WindowRate } from './watch';
import { useNotify } from './notify';
import debugTrace from './debugTrace';
import useSteadyPlayback from './useSteadyPlayback';

const SETTING_KEY = 'smartPlay.downloadAhead';
const MEMORY_KEY = 'smartPlay.ahead';
// Seconds of steady playback before the next episode is picked.
const PLAN_AFTER = 60;
// Share of an episode after which its download counts as watched.
const WATCHED_AT = 0.9;
// Next-slot choices remembered (episode -> source) this long.
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
    at: number,
    // Sources found too slow for this episode, not chosen again.
    slow?: string[],
    // The user deleted this episode's download (Downloads screen): it isn't
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
        // Storage unavailable: the record still carries the episode.
    }
};

const aheadSource = (metaId: string, videoId: string): AheadEntry | null => loadMemory()[memoryKey(metaId, videoId)] ?? null;

// Marks an episode's download cancelled by the user, so it isn't started
// again by itself.
const cancelAhead = (metaId: string, videoId: string) => {
    rememberAhead(metaId, videoId, { infoHash: '', fileIdx: -1, slow: aheadSource(metaId, videoId)?.slow, cancelled: true });
};

// The source an episode was downloaded from (not a placeholder).
const downloadedSource = (metaId: string, videoId: string): AheadEntry | null => {
    const entry = aheadSource(metaId, videoId);
    return entry !== null && entry.infoHash !== '' ? entry : null;
};

// ---- the slot ---------------------------------------------------------------

const trace = (entry: object) => debugTrace('next', entry);

type Meta = { id: string, type: string, videos: Video[], name?: string, poster?: string | null };

const episodeOrder = (a: Video, b: Video) => (a.season ?? 0) - (b.season ?? 0) || (a.episode ?? 0) - (b.episode ?? 0);

// The episode after the current one (specials only after a special).
const nextEpisode = (videos: Video[], videoId: string): Video | null => {
    const current = videos.find((video) => video.id === videoId);
    if (current === undefined) {
        return null;
    }
    return videos
        .filter((video) => (current.season === 0 || video.season !== 0) && episodeOrder(video, current) > 0)
        .sort(episodeOrder)[0] ?? null;
};

const episodeLabel = (video: Video) => getEpisodeLabel(video) ?? video.title;

// The Next record of an episode: the kept record named for it, else the one
// remembered for it.
const slotRecord = (downloads: Download[], metaId: string, videoId: string): Download | null => {
    const named = downloads.find((d) => d.kind === 'kept' && d.meta?.metaId === metaId && d.meta?.videoId === videoId);
    if (named !== undefined) {
        return named;
    }
    const known = downloadedSource(metaId, videoId);
    return known !== null ? downloads.find((d) => d.kind === 'kept' && d.infoHash === known.infoHash && d.fileIdx === known.fileIdx) ?? null : null;
};

type HookOptions = {
    baseUrl: string | null,
    addons: Addon[],
    meta: (Meta & { runtime?: string | null }) | null,
    videoId: string | null,
    playingInfoHash: string | null,
    playingFileIdx: number | null,
    // Player time and duration in milliseconds.
    time: number | null,
    duration: number | null,
    paused: boolean | null,
};

// Player: after PLAN_AFTER seconds of steady playback, fills the Next slot,
// then watches it every WATCH_EVERY.
const useNextSlot = ({ baseUrl, addons, meta, videoId, playingInfoHash, playingFileIdx, time, duration, paused }: HookOptions) => {
    const notify = useNotify();
    const played = useSteadyPlayback(time, paused, videoId ?? '');
    const marked = useRef(false);
    const busy = useRef<AbortController | null>(null);
    // The watched record (`infoHash:fileIdx`) and its rate and health.
    const watched = useRef<{ key: string, rate: WindowRate, health: HealthWatch } | null>(null);
    const live = useRef({ baseUrl, addons, meta, videoId, playingInfoHash, time, duration, paused });
    live.current = { baseUrl, addons, meta, videoId, playingInfoHash, time, duration, paused };

    useEffect(() => {
        marked.current = false;
        watched.current = null;
        return () => {
            busy.current?.abort();
            busy.current = null;
        };
    }, [videoId]);

    // An episode played past WATCHED_AT: its record (if it has one) is marked
    // watched, so it is deleted first when space is needed.
    useEffect(() => {
        if (!marked.current && baseUrl !== null && playingInfoHash !== null && playingFileIdx !== null && time !== null && duration !== null && duration > 0 && time / duration >= WATCHED_AT) {
            marked.current = true;
            markWatched(baseUrl, playingInfoHash, playingFileIdx);
        }
    }, [baseUrl, playingInfoHash, playingFileIdx, time, duration]);

    // Picks a source for the next episode and keeps it; `replacing` is the
    // record found too slow (deleted once its replacement is kept).
    const fill = useCallback(async (video: Video, downloads: Download[], replacing: { record: Download, ratio: number | null } | null) => {
        const ctx = live.current;
        if (ctx.baseUrl === null || ctx.meta === null || ctx.time === null || ctx.duration === null || ctx.videoId === null) {
            return;
        }
        const { baseUrl: base, meta: show } = ctx;
        const controller = new AbortController();
        busy.current = controller;
        const episode = episodeLabel(video);
        try {
            const known = aheadSource(show.id, video.id);
            const slow = new Set([...(known?.slow ?? []), ...(replacing !== null ? [replacing.record.infoHash] : [])]);
            const tiers = await episodeTiers(ctx.addons, show.type, video.id, downloads, slow);
            const steps = qualitySteps(tiers, startQuality(show.id));
            if (steps.length === 0) {
                if (replacing === null) {
                    notify.next({ kind: 'none', episode });
                }
                return;
            }
            // The time it has: what's left of this episode plus its own runtime.
            const seconds = Math.max(0, (ctx.duration - ctx.time) / 1000) + ctx.duration / 1000;
            const result = await pick({
                baseUrl: base,
                steps,
                startFraction: 0,
                runtime: seconds,
                records: downloads,
                meter: new RateMeter(),
                signal: controller.signal,
                trusted: (source) => source.infoHash === ctx.playingInfoHash,
            });
            const source = result.source;
            const quality = result.step.label;
            const gb = (source.onDisk ?? source.size ?? 0) / 1e9;
            const replacingKey = replacing !== null ? `${replacing.record.infoHash}:${replacing.record.fileIdx}` : null;
            // A replacement that isn't healthy either only goes in when it's faster.
            if (replacing !== null && result.weak && (result.ratio ?? 0) <= (replacing.ratio ?? 0)) {
                if (result.owned && source.fileIdx !== null && `${source.infoHash}:${source.fileIdx}` !== replacingKey) {
                    removeDownload(base, source.infoHash, source.fileIdx);
                }
                return;
            }
            if (source.fileIdx === null) {
                return;
            }
            await addDownload(base, source.infoHash, source.fileIdx, source.trackers.length > 0 ? source.trackers : [`dht:${source.infoHash}`], {
                kind: 'kept',
                origin: 'next',
                meta: {
                    metaId: show.id,
                    videoId: video.id,
                    type: show.type,
                    name: show.name,
                    poster: show.poster ?? undefined,
                    season: video.season ?? undefined,
                    episode: video.episode ?? undefined,
                    title: video.title ?? undefined,
                },
            });
            rememberAhead(show.id, video.id, { infoHash: source.infoHash, fileIdx: source.fileIdx, slow: Array.from(slow) });
            watched.current = null;
            if (replacing !== null) {
                removeDownload(base, replacing.record.infoHash, replacing.record.fileIdx);
                notify.next({ kind: 'replaced', episode, quality, gb, ratio: result.ratio, oldGb: replacing.record.size / 1e9, oldRatio: replacing.ratio });
            } else if (source.onDisk !== null) {
                notify.next({ kind: 'on-disk', episode, quality, gb });
            } else if (result.weak) {
                notify.next({ kind: 'weak', episode, quality, gb, ratio: result.ratio });
            } else {
                notify.next({ kind: 'queued', episode, quality, gb, ratio: result.ratio, lower: result.step.match === 'lower' });
            }
        } catch (e) {
            if (!controller.signal.aborted) {
                notify.next({ kind: 'failed', episode, reason: e instanceof Error ? e.message : String(e) });
            }
        } finally {
            if (busy.current === controller) {
                busy.current = null;
            }
        }
    }, [notify]);

    const fillRef = useRef(fill);
    fillRef.current = fill;

    // Every WATCH_EVERY: fill the slot once playback has settled, then watch it.
    useEffect(() => {
        const timer = setInterval(async () => {
            const ctx = live.current;
            if (!isAheadEnabled() || busy.current !== null || ctx.baseUrl === null || ctx.meta === null || ctx.meta.type !== 'series' || ctx.videoId === null || ctx.time === null || ctx.duration === null || ctx.duration <= 0 || played.current < PLAN_AFTER) {
                trace({ skip: 'waiting', enabled: isAheadEnabled(), busy: busy.current !== null, type: ctx.meta?.type ?? null, videoId: ctx.videoId, time: ctx.time, duration: ctx.duration, played: Math.round(played.current) });
                return;
            }
            const video = nextEpisode(ctx.meta.videos, ctx.videoId);
            if (video === null || aheadSource(ctx.meta.id, video.id)?.cancelled === true) {
                trace({ skip: video === null ? 'no next episode' : 'cancelled', videoId: ctx.videoId });
                return;
            }
            const downloads = await listDownloads(ctx.baseUrl);
            if (downloads === null || busy.current !== null) {
                return;
            }
            const record = slotRecord(downloads, ctx.meta.id, video.id);
            if (record === null) {
                trace({ fill: video.id });
                fillRef.current(video, downloads, null);
                return;
            }
            // On disk, paused, or the user's own download (never replaced):
            // nothing to watch.
            if (record.state === 'complete' || record.state === 'paused' || !isAutomatic(record)) {
                trace({ skip: !isAutomatic(record) ? 'user download' : record.state, videoId: video.id, record: `${record.infoHash}:${record.fileIdx}` });
                watched.current = null;
                return;
            }
            const key = `${record.infoHash}:${record.fileIdx}`;
            if (watched.current === null || watched.current.key !== key) {
                watched.current = { key, rate: new WindowRate(), health: new HealthWatch() };
            }
            const now = Date.now();
            watched.current.rate.add(now, record.bytesCompleted);
            const rate = watched.current.rate.rate();
            const seconds = Math.max(0, (ctx.duration - ctx.time) / 1000) + ctx.duration / 1000;
            const needed = need({ size: record.size, completed: record.bytesCompleted, runtime: seconds, startFraction: 0 });
            const healthy = rate === null || needed === null ? null : isHealthy(rate, needed);
            trace({ watch: key, video: video.id, rate, needed, healthy });
            if (watched.current.health.observe(now, healthy)) {
                fillRef.current(video, downloads, { record, ratio: healthRatio(rate, needed) });
            }
        }, WATCH_EVERY);
        return () => clearInterval(timer);
    }, [played]);
};

export { cancelAhead, downloadedSource, isAheadEnabled, useAheadToggle, useNextSlot };
