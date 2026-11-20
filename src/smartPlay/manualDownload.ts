// Manual downloads (plan 12): one episode from its streams page, or a season
// from the episode list. Each job races every episode's top sources at once
// (temporary records, as the play picker tests them; a season pack tested
// once for all its episodes), keeps each episode's fastest as a kept record
// with origin "manual" and the episode's order, and can be undone.
//
// Jobs live in this module, not in a component: a race keeps going when the
// chooser closes, and ManualDownloadToasts (mounted in App) says how it ended.

import { useEffect, useState } from 'react';
import { addDownload, listDownloads, refreshDownloads, removeDownload } from './downloads';
import type { Download, DownloadMeta } from './downloads';
import { fetchStreams } from './next';
import { groupByQuality, rankTiers } from './groupByQuality';
import { withDisk } from './withDisk';
import { stats } from './serverApi';
import { RateMeter } from './supply';
import { STEP_TIMEOUT } from './picker';
import { chooseWinner, orderOf, planEpisode, testSet } from './seasonPlan';
import type { PlannedEpisode } from './seasonPlan';
import type { RankedSource } from './groupByQuality';

const POLL = 1000;
// Temporary records are renewed this often (their lease is 2 min).
const RENEW = 30000;

type Show = { id: string, type: string, name: string, poster: string | null };

type JobEpisode = {
    planned: PlannedEpisode,
    state: 'racing' | 'queued' | 'failed',
    // Measured rates of its sources' torrents (bytes/s), while racing.
    rates: Record<string, number | null>,
    winner: RankedSource | null,
};

type JobState = 'racing' | 'done' | 'undone';

type Job = {
    id: number,
    metaId: string,
    kind: 'episode' | 'season',
    // The quality chosen (a season: the row's label).
    quality: string,
    season: number,
    state: JobState,
    episodes: JobEpisode[],
    // Kept records this job created (undo deletes them).
    created: { infoHash: string, fileIdx: number }[],
    bytes: number,
};

type Listener = () => void;

const jobs = new Map<number, Job>();
const controllers = new Map<number, AbortController>();
const listeners = new Set<Listener>();
// Jobs that ended since the toasts last looked.
const ended: number[] = [];
let nextId = 1;
let version = 0;

const emit = () => {
    version++;
    listeners.forEach((listener) => listener());
};

const update = (id: number, change: (job: Job) => Job) => {
    const job = jobs.get(id);
    if (job !== undefined) {
        jobs.set(id, change(job));
        emit();
    }
};

const metaOf = (show: Show, planned: PlannedEpisode): DownloadMeta => ({
    metaId: show.id,
    videoId: planned.videoId,
    type: show.type,
    name: show.name,
    poster: show.poster ?? undefined,
    season: planned.season,
    episode: planned.episode,
    title: planned.title ?? undefined,
});

const sleep = (ms: number, signal: AbortSignal) => new Promise<void>((resolve) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener('abort', () => {
        clearTimeout(timer);
        resolve();
    }, { once: true });
});

// The race: every tested torrent's start is primed as a temporary record and
// its download rate sampled, until each has a rate or the picker's step
// timeout passes. Returns the rates by torrent.
const race = async (baseUrl: string, id: number, show: Show, episodes: PlannedEpisode[], signal: AbortSignal): Promise<Map<string, number | null>> => {
    const tests = testSet(episodes);
    const tested = Array.from(tests.values()).filter(({ source }) => source.onDisk === null && source.fileIdx !== null);
    const byVideo = new Map(episodes.map((planned) => [planned.videoId, planned]));
    const register = () => tested.forEach(({ source, videoId }) => {
        const planned = byVideo.get(videoId)!;
        addDownload(baseUrl, source.infoHash, source.fileIdx!, source.trackers, { kind: 'temporary', origin: 'picker', startFraction: 0, meta: metaOf(show, planned) }).catch(() => undefined);
    });
    const meter = new RateMeter();
    const rates = new Map<string, number | null>(tested.map(({ source }) => [source.infoHash, null]));
    register();
    const renew = setInterval(register, RENEW);
    const started = Date.now();
    try {
        while (!signal.aborted) {
            const now = Date.now();
            await Promise.all(tested.map(({ source }) => stats(baseUrl, source.infoHash).then((s) => {
                if (s !== null) {
                    meter.add(source.infoHash, now, s.downloaded);
                }
            })));
            tested.forEach(({ source }) => rates.set(source.infoHash, meter.rate(source.infoHash)));
            const snapshot = Object.fromEntries(rates);
            update(id, (job) => ({ ...job, episodes: job.episodes.map((ep) => ({ ...ep, rates: snapshot })) }));
            if (Array.from(rates.values()).every((rate) => rate !== null) || Date.now() - started >= STEP_TIMEOUT) {
                break;
            }
            await sleep(POLL, signal);
        }
    } finally {
        clearInterval(renew);
    }
    return rates;
};

// Starts a job: races the episodes' sources, then keeps each one's winner.
// `episodes` come from seasonPlan (a season row, or one episode's tier).
const startDownload = (baseUrl: string, show: Show, kind: Job['kind'], quality: string, episodes: PlannedEpisode[]): number => {
    const id = nextId++;
    const controller = new AbortController();
    controllers.set(id, controller);
    jobs.set(id, {
        id,
        metaId: show.id,
        kind,
        quality,
        season: episodes[0]?.season ?? 0,
        state: 'racing',
        episodes: episodes.map((planned) => ({ planned, state: 'racing', rates: {}, winner: null })),
        created: [],
        bytes: 0,
    });
    emit();
    (async () => {
        const before = await listDownloads(baseUrl);
        const existing = new Set((before ?? []).filter((d) => d.kind === 'kept').map((d) => `${d.infoHash}:${d.fileIdx}`));
        const rates = await race(baseUrl, id, show, episodes, controller.signal);
        const tested = testSet(episodes);
        if (controller.signal.aborted) {
            tested.forEach(({ source }) => removeDownload(baseUrl, source.infoHash, source.fileIdx!));
            return;
        }
        const winners = episodes.map((planned) => ({ planned, winner: chooseWinner(planned, rates) }));
        const results = await Promise.all(winners.map(async ({ planned, winner }) => {
            if (winner === null || winner.fileIdx === null) {
                return { planned, winner: null };
            }
            try {
                await addDownload(baseUrl, winner.infoHash, winner.fileIdx, winner.trackers.length > 0 ? winner.trackers : [`dht:${winner.infoHash}`], {
                    kind: 'kept',
                    origin: 'manual',
                    order: orderOf(planned.season, planned.episode),
                    meta: metaOf(show, planned),
                });
                return { planned, winner };
            } catch (_e) {
                return { planned, winner: null };
            }
        }));
        // Tests that didn't win go now, not when their lease lapses.
        const kept = new Set(results.filter((r) => r.winner !== null).map((r) => `${r.winner!.infoHash}:${r.winner!.fileIdx}`));
        tested.forEach(({ source }) => {
            if (!kept.has(`${source.infoHash}:${source.fileIdx}`)) {
                removeDownload(baseUrl, source.infoHash, source.fileIdx!);
            }
        });
        const created = results
            .filter((r) => r.winner !== null && !existing.has(`${r.winner.infoHash}:${r.winner.fileIdx}`))
            .map((r) => ({ infoHash: r.winner!.infoHash, fileIdx: r.winner!.fileIdx! }));
        const bytes = results.reduce((sum, r) => sum + (r.winner?.size ?? 0), 0);
        update(id, (job) => job.state === 'undone' ? job : ({
            ...job,
            state: 'done',
            created,
            bytes,
            episodes: job.episodes.map((ep) => {
                const result = results.find((r) => r.planned.videoId === ep.planned.videoId);
                return { ...ep, state: result?.winner !== null && result?.winner !== undefined ? 'queued' : 'failed', winner: result?.winner ?? null };
            }),
        }));
        ended.push(id);
        emit();
        refreshDownloads();
    })().catch(() => {
        update(id, (job) => ({ ...job, state: 'done', episodes: job.episodes.map((ep) => ({ ...ep, state: 'failed' })) }));
        ended.push(id);
        emit();
    }).finally(() => controllers.delete(id));
    return id;
};

// Keeps one chosen source of an episode, with no race (a source picked from
// the expanded list).
const keepSource = (baseUrl: string, show: Show, planned: PlannedEpisode, source: RankedSource): number => {
    const id = nextId++;
    jobs.set(id, {
        id,
        metaId: show.id,
        kind: 'episode',
        quality: planned.label,
        season: planned.season,
        state: 'racing',
        episodes: [{ planned, state: 'racing', rates: {}, winner: source }],
        created: [],
        bytes: 0,
    });
    emit();
    (async () => {
        const before = await listDownloads(baseUrl);
        const existed = (before ?? []).some((d) => d.kind === 'kept' && d.infoHash === source.infoHash && d.fileIdx === source.fileIdx);
        await addDownload(baseUrl, source.infoHash, source.fileIdx!, source.trackers.length > 0 ? source.trackers : [`dht:${source.infoHash}`], {
            kind: 'kept',
            origin: 'manual',
            order: orderOf(planned.season, planned.episode),
            meta: metaOf(show, planned),
        });
        update(id, (job) => ({
            ...job,
            state: 'done',
            created: existed ? [] : [{ infoHash: source.infoHash, fileIdx: source.fileIdx! }],
            bytes: source.size ?? 0,
            episodes: job.episodes.map((ep) => ({ ...ep, state: 'queued' })),
        }));
    })().catch(() => {
        update(id, (job) => ({ ...job, state: 'done', episodes: job.episodes.map((ep) => ({ ...ep, state: 'failed', winner: null })) }));
    }).finally(() => {
        ended.push(id);
        emit();
        refreshDownloads();
    });
    return id;
};

// "Try another source" for a stalled kept file: its episode's streams again,
// the same quality's sources without that torrent, raced; the winner is kept
// and the stalled record deleted (when it's still incomplete).
const retryElsewhere = async (baseUrl: string, addons: Addon[], record: Download): Promise<boolean> => {
    const meta = record.meta;
    if (meta?.metaId === undefined || meta.videoId === undefined || meta.type === undefined) {
        return false;
    }
    const videoId = meta.videoId;
    const groups = await fetchStreams(addons, meta.type, videoId);
    const records = await listDownloads(baseUrl);
    const tiers = withDisk(rankTiers(groupByQuality(groups, null), new Map()), records, videoId);
    const current = tiers.find((tier) => tier.sources.some((source) => source.infoHash === record.infoHash && source.fileIdx === record.fileIdx));
    const others = tiers.map((tier) => ({ ...tier, sources: tier.sources.filter((source) => source.infoHash !== record.infoHash) }));
    const offer = { videoId, season: meta.season ?? 0, episode: meta.episode ?? 0, title: meta.title ?? null, unaired: false, downloading: false, tiers: others };
    const planned = planEpisode(offer, current?.key ?? tiers[0]?.key ?? '4k');
    if (planned === null || planned.sources.length === 0) {
        return false;
    }
    const show: Show = { id: meta.metaId, type: meta.type, name: meta.name ?? '', poster: meta.poster ?? null };
    const id = startDownload(baseUrl, show, 'episode', planned.label, [planned]);
    const done = await new Promise<Job | undefined>((resolve) => {
        const check = () => {
            const job = jobs.get(id);
            if (job === undefined || job.state !== 'racing') {
                listeners.delete(check);
                resolve(job);
            }
        };
        listeners.add(check);
        check();
    });
    if (done?.state === 'done' && done.episodes.some((ep) => ep.state === 'queued')) {
        const latest = (await listDownloads(baseUrl))?.find((d) => d.infoHash === record.infoHash && d.fileIdx === record.fileIdx);
        if (latest !== undefined && latest.state !== 'complete') {
            await removeDownload(baseUrl, record.infoHash, record.fileIdx);
        }
        refreshDownloads();
        return true;
    }
    return false;
};

// Cancels a race, or deletes what a finished job created.
const undoDownload = (baseUrl: string, id: number) => {
    const job = jobs.get(id);
    if (job === undefined || job.state === 'undone') {
        return;
    }
    controllers.get(id)?.abort();
    job.created.forEach(({ infoHash, fileIdx }) => removeDownload(baseUrl, infoHash, fileIdx));
    update(id, (current) => ({ ...current, state: 'undone' }));
    setTimeout(refreshDownloads, 500);
};

// Every job, re-rendered on any change.
const useManualDownloads = (): Job[] => {
    const [, setVersion] = useState(version);
    useEffect(() => {
        const listener = () => setVersion(version);
        listeners.add(listener);
        return () => {
            listeners.delete(listener);
        };
    }, []);
    return Array.from(jobs.values());
};

// Jobs that ended since the last call (for their toast).
const takeEnded = (): Job[] => ended.splice(0).map((id) => jobs.get(id)).filter((job): job is Job => job !== undefined && job.state === 'done');

// The racing job for an episode, if any.
const racingFor = (all: Job[], videoId: string) => all.find((job) => job.state === 'racing' && job.episodes.some((ep) => ep.planned.videoId === videoId)) ?? null;

export type { Job, JobEpisode, Show };
export { startDownload, keepSource, retryElsewhere, undoDownload, useManualDownloads, takeEnded, racingFor };
