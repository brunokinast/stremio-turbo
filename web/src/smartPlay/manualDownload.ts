// Copyright (C) 2017-2026 Smart code 203358507

// Manual downloads: one episode from its streams page, or a season from the
// episode list. Each job races every episode's top sources (temporary
// records, as the play picker tests them; a season pack tested once for all
// its episodes), then keeps each episode's fastest as a kept record with
// origin "manual" and the episode's order. Once started, a job runs to its
// end; the Downloads screen is where downloads are managed.
//
// Jobs live in this module, not in a component: a race keeps going when the
// chooser closes, and ManualDownloadToasts (mounted in App) says how it ended.

import { useSyncExternalStore } from 'react';
import { addDownload, hasKept, listDownloads, refreshDownloads, removeDownload } from './downloads';
import type { Download, DownloadMeta } from './downloads';
import { episodeTiers, DEFAULT_QUALITY } from './addonStreams';
import { stats } from './serverApi';
import { RateMeter } from './supply';
import { STEP_TIMEOUT } from './picker';
import { chooseWinner, orderOf, planEpisode, testWaves } from './seasonPlan';
import type { PlannedEpisode, Test } from './seasonPlan';
import type { RankedSource } from './groupByQuality';

const POLL = 1000;
// Temporary records are renewed this often (their lease is 2 min).
const RENEW = 30000;

type Show = { id: string, type: string, name: string, poster: string | null };

type JobEpisode = {
    planned: PlannedEpisode,
    state: 'racing' | 'queued' | 'failed',
    // Measured rates of the tested torrents (bytes/s), while racing.
    rates: Record<string, number | null>,
    winner: RankedSource | null,
};

type Job = {
    id: number,
    metaId: string,
    kind: 'episode' | 'season',
    // The row clicked (a quality row's key) and the quality it stands for.
    tierKey: string,
    quality: string,
    season: number,
    state: 'racing' | 'done',
    episodes: JobEpisode[],
    bytes: number,
};

type Listener = () => void;

const jobs = new Map<number, Job>();
const listeners = new Set<Listener>();
// Jobs that ended since the toasts last looked.
const ended: number[] = [];
let nextId = 1;
// The jobs as one array, replaced on every change (useSyncExternalStore's
// snapshot).
let snapshot: Job[] = [];

const emit = () => {
    snapshot = Array.from(jobs.values());
    listeners.forEach((listener) => listener());
};

const update = (id: number, change: (job: Job) => Job) => {
    const job = jobs.get(id);
    if (job !== undefined) {
        jobs.set(id, change(job));
        emit();
    }
};

const finish = (id: number, change: (job: Job) => Job) => {
    update(id, (job) => ({ ...change(job), state: 'done' }));
    ended.push(id);
    emit();
    refreshDownloads();
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

const trackersOf = (source: RankedSource) => source.trackers.length > 0 ? source.trackers : [`dht:${source.infoHash}`];

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

// One wave of the race: each tested torrent's start is primed as a temporary
// record and its download rate sampled, until each has a rate or the
// picker's step timeout passes. Rates are written into `rates`. Returns a
// wait for the wave's adds still in flight, so the caller's removals can't
// be overtaken by a late add that recreates a record.
const raceWave = async (baseUrl: string, id: number, show: Show, wave: Test[], byVideo: Map<string, PlannedEpisode>, rates: Map<string, number | null>) => {
    const meter = new RateMeter();
    const pending: Promise<unknown>[] = [];
    const register = () => wave.forEach(({ source, videoId }) => {
        pending.push(addDownload(baseUrl, source.infoHash, source.fileIdx!, source.trackers, { kind: 'temporary', origin: 'picker', startFraction: 0, meta: metaOf(show, byVideo.get(videoId)!) }).catch(() => undefined));
    });
    wave.forEach(({ source }) => rates.set(source.infoHash, null));
    register();
    const renew = setInterval(register, RENEW);
    const started = Date.now();
    try {
        for (;;) {
            const now = Date.now();
            await Promise.all(wave.map(({ source }) => stats(baseUrl, source.infoHash).then((s) => {
                if (s !== null) {
                    meter.add(source.infoHash, now, s.downloaded);
                }
            })));
            wave.forEach(({ source }) => rates.set(source.infoHash, meter.rate(source.infoHash)));
            const snapshot = Object.fromEntries(rates);
            update(id, (job) => ({ ...job, episodes: job.episodes.map((ep) => ({ ...ep, rates: snapshot })) }));
            if (wave.every(({ source }) => rates.get(source.infoHash) !== null) || Date.now() - started >= STEP_TIMEOUT) {
                break;
            }
            await sleep(POLL);
        }
    } finally {
        clearInterval(renew);
    }
    return () => Promise.all(pending);
};

// Starts a job: races the episodes' sources wave by wave, then keeps each
// one's winner. `episodes` come from seasonPlan (a season row, or one
// episode's tier).
const startDownload = (baseUrl: string, show: Show, kind: Job['kind'], tierKey: string, quality: string, episodes: PlannedEpisode[]): number => {
    const id = nextId++;
    jobs.set(id, {
        id,
        metaId: show.id,
        kind,
        tierKey,
        quality,
        season: episodes[0]?.season ?? 0,
        state: 'racing',
        episodes: episodes.map((planned) => ({ planned, state: 'racing', rates: {}, winner: null })),
        bytes: 0,
    });
    emit();
    (async () => {
        const before = await listDownloads(baseUrl);
        // Only tests this job adds are removed: never a copy on disk or a
        // record that was already kept.
        const owns = (source: RankedSource) => source.onDisk === null && source.fileIdx !== null && !hasKept(before, source);
        const byVideo = new Map(episodes.map((planned) => [planned.videoId, planned]));
        const rates = new Map<string, number | null>();
        const tested: Test[] = [];
        const settled: (() => Promise<unknown>)[] = [];
        for (const wave of testWaves(episodes)) {
            const owned = Array.from(wave.values()).filter(({ source }) => owns(source));
            tested.push(...owned);
            settled.push(await raceWave(baseUrl, id, show, owned, byVideo, rates));
        }
        const results = await Promise.all(episodes.map(async (planned) => {
            const winner = chooseWinner(planned, rates);
            if (winner === null || winner.fileIdx === null) {
                return { planned, winner: null };
            }
            try {
                await addDownload(baseUrl, winner.infoHash, winner.fileIdx, trackersOf(winner), {
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
        await Promise.all(settled.map((wait) => wait()));
        const kept = new Set(results.filter((r) => r.winner !== null).map((r) => `${r.winner!.infoHash}:${r.winner!.fileIdx}`));
        tested.forEach(({ source }) => {
            if (!kept.has(`${source.infoHash}:${source.fileIdx}`)) {
                removeDownload(baseUrl, source.infoHash, source.fileIdx!);
            }
        });
        finish(id, (job) => ({
            ...job,
            bytes: results.reduce((sum, r) => sum + (r.winner?.size ?? 0), 0),
            episodes: job.episodes.map((ep) => {
                const winner = results.find((r) => r.planned.videoId === ep.planned.videoId)?.winner ?? null;
                return { ...ep, state: winner !== null ? 'queued' : 'failed', winner };
            }),
        }));
    })().catch(() => {
        finish(id, (job) => ({ ...job, episodes: job.episodes.map((ep) => ({ ...ep, state: 'failed' })) }));
    });
    return id;
};

// Keeps one chosen source of an episode, with no race (a source picked from
// the expanded list).
const keepSource = (baseUrl: string, show: Show, tierKey: string, planned: PlannedEpisode, source: RankedSource): number => {
    const id = nextId++;
    jobs.set(id, {
        id,
        metaId: show.id,
        kind: 'episode',
        tierKey,
        quality: planned.label,
        season: planned.season,
        state: 'racing',
        episodes: [{ planned, state: 'racing', rates: {}, winner: source }],
        bytes: 0,
    });
    emit();
    addDownload(baseUrl, source.infoHash, source.fileIdx!, trackersOf(source), {
        kind: 'kept',
        origin: 'manual',
        order: orderOf(planned.season, planned.episode),
        meta: metaOf(show, planned),
    }).then(() => {
        finish(id, (job) => ({ ...job, bytes: source.size ?? 0, episodes: job.episodes.map((ep) => ({ ...ep, state: 'queued' })) }));
    }, () => {
        finish(id, (job) => ({ ...job, episodes: job.episodes.map((ep) => ({ ...ep, state: 'failed', winner: null })) }));
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
    const records = await listDownloads(baseUrl);
    const tiers = await episodeTiers(addons, meta.type, videoId, records);
    const current = tiers.find((tier) => tier.sources.some((source) => source.infoHash === record.infoHash && source.fileIdx === record.fileIdx));
    const others = tiers.map((tier) => ({ ...tier, sources: tier.sources.filter((source) => source.infoHash !== record.infoHash) }));
    const offer = { videoId, season: meta.season ?? 0, episode: meta.episode ?? 0, title: meta.title ?? null, unaired: false, downloading: false, tiers: others };
    const planned = planEpisode(offer, current?.key ?? tiers[0]?.key ?? DEFAULT_QUALITY);
    if (planned === null || planned.sources.length === 0) {
        return false;
    }
    const show: Show = { id: meta.metaId, type: meta.type, name: meta.name ?? '', poster: meta.poster ?? null };
    const id = startDownload(baseUrl, show, 'episode', current?.key ?? planned.label, planned.label, [planned]);
    const done = await new Promise<Job | undefined>((resolve) => {
        const check = () => {
            const job = jobs.get(id);
            if (job === undefined || job.state === 'done') {
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

const subscribe = (listener: Listener) => {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
};

// Every job, re-rendered on any change.
const useManualDownloads = (): Job[] => useSyncExternalStore(subscribe, () => snapshot);

// Jobs that ended since the last call (for their toast).
const takeEnded = (): Job[] => ended.splice(0).map((id) => jobs.get(id)).filter((job): job is Job => job !== undefined);

// The racing job for an episode, if any.
const racingFor = (all: Job[], videoId: string) => all.find((job) => job.state === 'racing' && job.episodes.some((ep) => ep.planned.videoId === videoId)) ?? null;

export type { Job, JobEpisode, Show };
export { startDownload, keepSource, retryElsewhere, useManualDownloads, takeEnded, racingFor };
