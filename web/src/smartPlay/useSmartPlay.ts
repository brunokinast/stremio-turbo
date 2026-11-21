// Copyright (C) 2017-2026 Smart code 203358507

// Streams-list side of smart play (the Now slot): groups sources by
// quality, ranks them by live seeders, and picks the source to play with the
// shared picker (picker.ts): a copy on disk plays directly; otherwise the
// quality's top sources are tested start-only and the healthiest healthy one
// plays, stepping down the quality order when none keeps up.
//
// The pick for the likely quality starts in the background when the page
// opens, so Play is usually instant; leaving the page deletes its tests.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { toPath } from 'stremio-router';
import { useCore } from 'stremio/core';
import useStreamingServer from 'stremio/common/useStreamingServer';
import useToast from 'stremio/common/Toast/useToast';
import type { StreamGroup } from 'stremio/routes/Details/Streams/useStreamsByAddon';
import { fallbackTier, groupByQuality, offersQuality } from './groupByQuality';
import useLiveSeeders from './useLiveSeeders';
import { parseRuntime, RateMeter } from './supply';
import { getLastQuality, getQuality, qualityLabel, setQuality } from './qualityMemory';
import { setState, useSmartPlayState } from './session';
import { addDownload, removeDownload, useDownloads } from './downloads';
import { qualitySteps } from './candidates';
import { withDisk } from './withDisk';
import { pick } from './picker';
import type { PickResult, Progress } from './picker';
import { need } from './health';
import { remember } from './rateMemory';
import type { RankedSource } from './groupByQuality';
import { gb, times } from './format';
import debugTrace from './debugTrace';

type Options = {
    groups: StreamGroup[],
    // Addons still loading streams.
    streamsLoading: boolean,
    metaId: string | null,
    videoId: string | null,
    libraryItem: LibraryItem | null,
    // The show's runtime text from its metadata (e.g. "60 min"), for bitrates.
    runtime: string | null,
    enabled: boolean,
};

// What the loader shows while a pick the user waits on runs.
type Picking = {
    tierKey: string,
    label: string,
    progress: Progress | null,
};

// The running or finished pick, keyed by the quality it started from.
type Run = {
    key: string,
    controller: AbortController,
    progress: Progress | null,
    result: PickResult | null,
    done: Promise<PickResult | null>,
};

// How long the ranking must hold still before the background pick starts
// (live counts can still reorder it).
const PICK_DEBOUNCE = 2000;
// How often the records are read for the on-disk marks.
const DISK_POLL = 5000;
// The background winner's test record is renewed while the page stays open.
const RENEW = 30000;

const useSmartPlay = ({ groups, streamsLoading, metaId, videoId, libraryItem, runtime, enabled }: Options) => {
    const core = useCore();
    const navigate = useNavigate();
    // The URL the player streams from locally (baseUrl is the LAN address used for casting).
    const baseUrl = useStreamingServer().selected?.transportUrl ?? null;
    const grouped = useMemo(() => enabled && videoId !== null ? groupByQuality(groups, videoId) : [], [enabled, groups, videoId]);
    const { tiers: liveTiers, settled } = useLiveSeeders(grouped, baseUrl);
    // Which sources are all on disk, from the server's records (the shared
    // poller): they play from disk, so they show "On disk" and play directly.
    const { downloads: records } = useDownloads(DISK_POLL, enabled && videoId !== null);
    const tiers = useMemo(() => withDisk(liveTiers, records, videoId), [liveTiers, records, videoId]);
    const recordsRef = useRef(records);
    recordsRef.current = records;
    const { autoplay } = useSmartPlayState();
    const { t } = useTranslation();
    const toast = useToast();
    const [picking, setPicking] = useState<Picking | null>(null);
    const meter = useRef(new RateMeter());
    // A play started from this page: the background pick no longer applies.
    const playStarted = useRef(false);
    const run = useRef<Run | null>(null);

    const remembered = metaId !== null ? getQuality(metaId) : null;

    // Where the player will start, as a fraction of the file (resume point).
    const startFraction = libraryItem !== null && libraryItem.state.video_id === videoId && libraryItem.state.duration > 0 ?
        Math.min(1, libraryItem.state.timeOffset / libraryItem.state.duration)
        :
        0;

    // The episode's runtime in seconds: the show's last played video's
    // duration, else its metadata's runtime.
    const runtimeSeconds = libraryItem !== null && libraryItem.state.duration > 0 ? libraryItem.state.duration / 1000 : parseRuntime(runtime);

    // The likely quality: the show's remembered one, or the last played
    // anywhere, or else the closest offered (fallbackTier).
    const likelyKey = remembered ?? getLastQuality();
    const likelyTier = tiers.find((tier) => tier.key === likelyKey) ?? fallbackTier(tiers, likelyKey);

    // A quality chosen for the user is said out loud: once every addon has
    // answered, if the remembered (or last) quality isn't offered for this
    // episode, a toast names the quality used instead. Once per episode.
    const fallbackKey = !streamsLoading && likelyKey !== null && likelyTier !== undefined && likelyTier.key !== likelyKey ? likelyTier.key : null;
    const warned = useRef<string | null>(null);
    useEffect(() => {
        if (fallbackKey === null || likelyTier === undefined || likelyKey === null || warned.current === videoId) {
            return;
        }
        warned.current = videoId;
        toast.show({
            type: 'info',
            title: t('SMART_PLAY_QUALITY_CHANGED', { quality: qualityLabel(likelyKey), fallback: likelyTier.label }),
            timeout: 8000,
        });
        // likelyTier changes identity on every count update; fallbackKey captures it.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [fallbackKey, videoId]);

    const meta = useMemo(() => metaId !== null && videoId !== null ? { metaId, videoId } : undefined, [metaId, videoId]);

    const startPick = useCallback((tierKey: string): Run | null => {
        if (baseUrl === null) {
            return null;
        }
        const steps = qualitySteps(tiers, tierKey);
        if (steps.length === 0) {
            return null;
        }
        run.current?.controller.abort();
        const controller = new AbortController();
        const current: Run = { key: tierKey, controller, progress: null, result: null, done: Promise.resolve(null) };
        current.done = pick({
            baseUrl,
            steps,
            startFraction,
            runtime: runtimeSeconds,
            records: recordsRef.current,
            meta,
            meter: meter.current,
            signal: controller.signal,
            keep: () => current.progress?.leader?.source ?? null,
            onProgress: (progress) => {
                current.progress = progress;
                setPicking((p) => p !== null && p.tierKey === tierKey ? { ...p, progress } : p);
            },
            onStepDown: (from, best, to) => {
                toast.show({ type: 'info', title: t('SMART_PLAY_STEP_DOWN', { from: from.label, best: times(best), to: to.label }), timeout: 6000 });
            },
        }).then((result) => {
            current.result = result;
            return result;
        }, () => null);
        run.current = current;
        return current;
    }, [baseUrl, tiers, startFraction, runtimeSeconds, meta, toast, t]);

    // Background pick for the likely quality once the ranking holds still.
    const likelyReady = likelyTier !== undefined && (likelyTier.best !== null || settled || likelyTier.disk !== null);
    const likelyTierKey = likelyTier?.key ?? null;
    useEffect(() => {
        debugTrace('pick', { videoId, enabled, server: baseUrl !== null, likelyReady, likelyTierKey, playStarted: playStarted.current, run: run.current?.key ?? null });
        if (!enabled || baseUrl === null || !likelyReady || likelyTierKey === null || playStarted.current) {
            return;
        }
        const timer = setTimeout(() => {
            if (!playStarted.current && (run.current === null || run.current.key !== likelyTierKey)) {
                startPick(likelyTierKey);
            }
        }, PICK_DEBOUNCE);
        return () => clearTimeout(timer);
        // startPick changes identity with every count update; the key and start point decide.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [enabled, baseUrl, likelyReady, likelyTierKey, startFraction]);

    // The background winner stays primed while the page is open (its test
    // record is renewed); leaving the page deletes it and cancels the pick.
    useEffect(() => {
        const timer = setInterval(() => {
            const result = run.current?.result;
            if (baseUrl !== null && result?.owned === true && result.source.fileIdx !== null && !playStarted.current) {
                addDownload(baseUrl, result.source.infoHash, result.source.fileIdx, result.source.trackers, { kind: 'temporary', origin: 'picker', startFraction, meta }).catch(() => undefined);
            }
        }, RENEW);
        return () => clearInterval(timer);
    }, [baseUrl, startFraction, meta]);
    useEffect(() => {
        playStarted.current = false;
        return () => {
            const current = run.current;
            run.current = null;
            current?.controller.abort();
            const result = current?.result;
            if (!playStarted.current && baseUrl !== null && result?.owned === true && result.source.fileIdx !== null) {
                removeDownload(baseUrl, result.source.infoHash, result.source.fileIdx);
            }
        };
        // Per episode.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [metaId, videoId]);

    const playSource = useCallback((source: RankedSource, tierKey: string | null, rememberQuality: boolean, result: PickResult | null) => {
        if (typeof source.stream.deepLinks?.player !== 'string') {
            return;
        }
        playStarted.current = true;
        if (rememberQuality && metaId !== null && tierKey !== null) {
            setQuality(metaId, tierKey, tiers.find((tier) => tier.key === tierKey)?.label ?? null);
        }
        // Said only when it differs from a plain play of the chosen quality
        // (a step down was already said when it happened).
        const size = source.onDisk ?? source.size;
        if (source.onDisk !== null) {
            toast.show({ type: 'info', title: t('SMART_PLAY_DOWNLOADED_COPY', { size: gb(size) }), timeout: 6000 });
        } else if (result !== null && result.weak) {
            toast.show({ type: 'info', title: t('SMART_PLAY_NONE_HEALTHY', { size: gb(size) }), message: t('SMART_PLAY_NONE_HEALTHY_WHY', { ratio: times(result.ratio) }), timeout: 8000 });
        }
        // The rate measured feeds the 24 h memory and the player's start hold.
        const rate = meter.current.rate(source.infoHash);
        const needed = need({ size: source.size, completed: 0, runtime: runtimeSeconds, startFraction });
        if (rate !== null && needed !== null && needed > 0) {
            remember(source.infoHash, rate / needed);
        }
        core.transport.analytics({ event: 'StreamClicked', args: { stream: source.stream } });
        setState({ handoff: source.onDisk === null && rate !== null && needed !== null && needed > 0 ? { infoHash: source.infoHash, rate, needed } : null });
        setPicking(null);
        navigate(toPath(source.stream.deepLinks.player));
    }, [metaId, tiers, toast, t, runtimeSeconds, startFraction, core.transport, navigate]);

    const cancel = useCallback(() => {
        run.current?.controller.abort();
        run.current = null;
        playStarted.current = false;
        setPicking(null);
    }, []);

    // Plays the healthiest source the picker found so far, without waiting.
    const playLeader = useCallback(() => {
        const current = run.current;
        const leader = current?.progress?.leader?.source ?? null;
        if (current === null || leader === null) {
            return;
        }
        current.controller.abort();
        playSource(leader, current.key, true, null);
    }, [playSource]);

    // Plays a quality: its copy on disk directly; else the pick for it (the
    // background one when it started from this quality, waiting on it with
    // the loader while it runs).
    const play = useCallback((tierKey: string, rememberQuality = true) => {
        const tier = tiers.find((tier) => tier.key === tierKey);
        if (tier === undefined || tier.sources.length === 0) {
            return;
        }
        if (tier.disk) {
            playSource(tier.disk, tierKey, rememberQuality, null);
            return;
        }
        playStarted.current = true;
        const current = run.current !== null && run.current.key === tierKey ? run.current : startPick(tierKey);
        if (current === null) {
            playStarted.current = false;
            return;
        }
        // A step down plays without replacing the remembered quality.
        const finish = (result: PickResult) => playSource(result.source, result.step.key, rememberQuality && result.step.match === 'exact', result);
        if (current.result !== null) {
            finish(current.result);
            return;
        }
        setPicking({ tierKey, label: tier.label, progress: current.progress });
        current.done.then((result) => {
            if (result !== null && run.current === current && !current.controller.signal.aborted) {
                finish(result);
            }
        });
    }, [tiers, startPick, playSource]);

    // Resume / next episode asked to autoplay this video: once the remembered
    // quality has a live count (or nothing more is coming), play it. If the
    // episode doesn't offer it, the closest quality plays (fallbackTier); the
    // quality-change toast above says so, and the remembered one stays.
    useEffect(() => {
        if (autoplay === null || autoplay.metaId !== metaId || autoplay.videoId !== videoId) {
            return;
        }
        const tier = remembered !== null ? tiers.find((tier) => tier.key === remembered) : undefined;
        if (tier !== undefined && (tier.best !== null || tier.disk !== null || (settled && !streamsLoading))) {
            setState({ autoplay: null });
            play(tier.key, true);
        } else if (tier === undefined && !streamsLoading) {
            const fallback = remembered !== null ? fallbackTier(tiers, remembered) : undefined;
            if (fallback !== undefined && (fallback.best !== null || settled)) {
                setState({ autoplay: null });
                play(fallback.key, false);
            } else if (fallback === undefined || settled) {
                setState({ autoplay: null });
            }
        }
    }, [autoplay, metaId, videoId, remembered, tiers, settled, streamsLoading, play]);

    return { tiers, settled, picking, remembered, play, playLeader, cancel };
};

// Asks the streams list of a video to autoplay its remembered quality.
const requestAutoplay = (metaId: string, videoId: string): boolean => {
    if (getQuality(metaId) === null) {
        return false;
    }
    setState({ autoplay: { metaId, videoId } });
    return true;
};

const hasRememberedQuality = (metaId: string) => getQuality(metaId) !== null;

const offersRememberedQuality = (metaId: string, videoId: string, streams: MetaDetails['streams']) => {
    const quality = getQuality(metaId);
    return quality !== null && offersQuality(streams, videoId, quality);
};

export type { Picking };
export { requestAutoplay, hasRememberedQuality, offersRememberedQuality };
export default useSmartPlay;
