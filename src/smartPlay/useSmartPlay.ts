// Streams-list side of smart play: groups sources by quality, ranks them by
// live seeders, and plays a quality: its copy on disk when it has one, else
// by racing its best sources.

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
import race from './race';
import type { RacerProgress } from './race';
import { preload, stats } from './serverApi';
import { neededRate, parseRuntime, RateMeter } from './supply';
import { getLastQuality, getQuality, setQuality } from './qualityMemory';
import { setState, useSmartPlayState } from './session';
import { claim } from './warmup';
import { downloadedSource } from './ahead';
import { isComplete, listDownloads, useDownloads } from './downloads';
import type { Download } from './downloads';
import type { RankedSource, RankedTier } from './groupByQuality';

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

type Racing = {
    tierKey: string,
    label: string,
    weak: boolean,
    racers: RacerProgress[],
};

// Sources of the likely quality opened ahead of a click, and how long the
// ranking must hold still first (live counts can still reorder it).
const PRELOAD_SOURCES = 3;
const PRELOAD_DEBOUNCE = 2000;

// How often the preloaded sources' download is sampled while on the page.
const DWELL_POLL = 2000;

// How often the records are read for the on-disk marks.
const DISK_POLL = 5000;

// Marks each source whose file is all on disk, and each tier's best such
// source (the largest). A source without a file index matches the record
// named for this episode.
const withDisk = (tiers: RankedTier[], records: Download[] | null, videoId: string | null): RankedTier[] => {
    const complete = (records ?? []).filter(isComplete);
    if (complete.length === 0) {
        return tiers;
    }
    const byFile = new Map(complete.map((d) => [`${d.infoHash}:${d.fileIdx}`, d]));
    const sizeOnDisk = (source: RankedSource) => {
        const record = source.fileIdx !== null ?
            byFile.get(`${source.infoHash}:${source.fileIdx}`)
            :
            complete.find((d) => d.infoHash === source.infoHash && d.meta?.videoId === videoId);
        return record !== undefined ? record.size : null;
    };
    return tiers.map((tier) => {
        const sources = tier.sources.map((source) => ({ ...source, onDisk: sizeOnDisk(source) }));
        const disk = sources.reduce<RankedSource | null>((best, source) => source.onDisk !== null && (best === null || source.onDisk > best.onDisk!) ? source : best, null);
        return { ...tier, sources, disk };
    });
};

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
    const { autoplay } = useSmartPlayState();
    const { t } = useTranslation();
    const toast = useToast();
    const [racing, setRacing] = useState<Racing | null>(null);
    const raceController = useRef<AbortController | null>(null);
    // A play started from this page: the preload no longer applies (it would
    // re-add the sources the race just removed, as autoplay can race before
    // the preload's debounce runs).
    const playStarted = useRef(false);
    useEffect(() => {
        playStarted.current = false;
    }, [metaId, videoId]);

    const remembered = metaId !== null ? getQuality(metaId) : null;

    // Where the player will start, as a fraction of the file (resume point).
    const startFraction = libraryItem !== null && libraryItem.state.video_id === videoId && libraryItem.state.duration > 0 ?
        Math.min(1, libraryItem.state.timeOffset / libraryItem.state.duration)
        :
        0;

    // Preload: open the likely quality's best sources before any click, so
    // their peers, metadata and the pieces at the start position are ready
    // when the race starts. The likely quality is the show's remembered one,
    // or the last played anywhere, or else the closest offered (fallbackTier):
    // without a match nothing was preloaded and a first play took 12.5 s
    // instead of under 1 s (streaming-research findings/17).
    const preloaded = useRef(new Set<string>());
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
            title: t('SMART_PLAY_QUALITY_CHANGED', { quality: likelyKey, fallback: likelyTier.label }),
            timeout: 8000,
        });
        // likelyTier changes identity on every count update; fallbackKey captures it.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [fallbackKey, videoId]);
    const preloadSources = useMemo(() => likelyTier !== undefined && (likelyTier.best !== null || settled) ? likelyTier.sources.slice(0, PRELOAD_SOURCES) : [], [likelyTier, settled]);
    const preloadKey = preloadSources.map(({ infoHash }) => infoHash).join(',');

    // The episode's runtime in seconds: the show's last played video's
    // duration, else its metadata's runtime. With file sizes it gives each
    // source's bitrate, which a source's rate must cover (supply.ts).
    const runtimeSeconds = libraryItem !== null && libraryItem.state.duration > 0 ? libraryItem.state.duration / 1000 : parseRuntime(runtime);

    // Download rates of the preloaded sources, sampled while on the page: the
    // race decides from them when the user presses Play.
    const meter = useRef(new RateMeter());
    useEffect(() => {
        if (baseUrl === null || preloadSources.length === 0) {
            return;
        }
        const timer = setInterval(() => {
            preloadSources.forEach(({ infoHash }) => {
                stats(baseUrl, infoHash).then((s) => {
                    if (s !== null) {
                        meter.current.add(infoHash, Date.now(), s.downloaded);
                    }
                });
            });
        }, DWELL_POLL);
        return () => clearInterval(timer);
        // preloadKey captures the source set.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [baseUrl, preloadKey]);
    useEffect(() => {
        if (baseUrl === null || preloadSources.length === 0) {
            return;
        }
        const timer = setTimeout(() => {
            if (playStarted.current) {
                return;
            }
            // Keyed by start position too: a resume point known later moves
            // the server's primed pieces there.
            preloadSources
                .filter(({ infoHash }) => !preloaded.current.has(`${infoHash}@${startFraction}`))
                .forEach(({ infoHash, fileIdx, trackers }) => {
                    preloaded.current.add(`${infoHash}@${startFraction}`);
                    preload(baseUrl, infoHash, fileIdx, trackers, startFraction);
                });
        }, PRELOAD_DEBOUNCE);
        return () => clearTimeout(timer);
        // preloadKey captures the source set; preloadSources changes identity on every count update.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [baseUrl, preloadKey, startFraction]);

    const cancel = useCallback(() => {
        raceController.current?.abort();
        raceController.current = null;
        playStarted.current = false;
        setRacing(null);
    }, []);

    // The copy of this episode downloaded ahead (ahead.ts), when it is still
    // on the server and among the listed sources: it was chosen to finish
    // before playback could catch it, so it plays directly, without a race.
    const downloadedCopy = useCallback(async (): Promise<RankedSource | null> => {
        const known = metaId !== null && videoId !== null ? downloadedSource(metaId, videoId) : null;
        if (known === null || baseUrl === null) {
            return null;
        }
        const source = tiers.flatMap((tier) => tier.sources).find((s) => s.infoHash === known.infoHash && s.fileIdx === known.fileIdx);
        if (source === undefined) {
            return null;
        }
        const downloads = await listDownloads(baseUrl);
        return downloads !== null && downloads.some((d) => d.infoHash === known.infoHash && d.fileIdx === known.fileIdx) ? source : null;
    }, [metaId, videoId, baseUrl, tiers]);

    const playDirect = useCallback((source: RankedSource) => {
        if (typeof source.stream.deepLinks?.player !== 'string') {
            return;
        }
        const size = source.onDisk ?? source.size;
        toast.show({ type: 'info', title: t('SMART_PLAY_DOWNLOADED_COPY', { size: size !== null ? (size / 1e9).toFixed(1) : '?' }), timeout: 6000 });
        core.transport.analytics({ event: 'StreamClicked', args: { stream: source.stream } });
        claim(source.infoHash);
        setState({ handoff: null });
        navigate(toPath(source.stream.deepLinks.player));
    }, [core.transport, navigate, toast, t]);

    // remember: an explicit choice becomes the show's quality; a fallback
    // chosen for the user (autoplay) doesn't replace it.
    const raceTier = useCallback((tierKey: string, remember = true) => {
        const tier = tiers.find((tier) => tier.key === tierKey);
        if (baseUrl === null || metaId === null || videoId === null || tier === undefined || tier.sources.length === 0) {
            return;
        }
        if (remember) {
            setQuality(metaId, tierKey);
        }
        raceController.current?.abort();
        playStarted.current = true;
        const controller = new AbortController();
        raceController.current = controller;
        setRacing({ tierKey, label: tier.label, weak: tier.weak, racers: [] });
        const onProgress = (racers: RacerProgress[]) => {
            setRacing((current) => current !== null && current.tierKey === tierKey ? { ...current, racers } : current);
        };
        race({ baseUrl, sources: tier.sources, startFraction, runtime: runtimeSeconds, meter: meter.current, signal: controller.signal, onProgress })
            .then(({ source: winner, passed }) => {
                if (controller.signal.aborted || typeof winner.stream.deepLinks?.player !== 'string') {
                    return;
                }
                // A larger file passed over is a choice made for the user: say so.
                const largest = passed.reduce<typeof passed[number] | null>((top, item) => top === null || item.needed > top.needed ? item : top, null);
                if (largest !== null) {
                    const gb = (bytes: number | null) => bytes !== null ? (bytes / 1e9).toFixed(1) : '?';
                    toast.show({
                        type: 'info',
                        title: t(largest.remembered ? 'SMART_PLAY_PASSED_SLOW_EARLIER' : 'SMART_PLAY_PASSED_SLOW', {
                            size: gb(winner.size),
                            passedSize: gb(largest.source.size),
                            rate: (largest.rate / 1e6).toFixed(1),
                            needed: (largest.needed / 1e6).toFixed(1),
                        }),
                        timeout: 8000,
                    });
                }
                core.transport.analytics({ event: 'StreamClicked', args: { stream: winner.stream } });
                setRacing(null);
                raceController.current = null;
                const rate = meter.current.rate(winner.infoHash);
                const needed = neededRate(winner, runtimeSeconds);
                claim(winner.infoHash);
                setState({ handoff: rate !== null && needed !== null ? { infoHash: winner.infoHash, rate, needed } : null });
                navigate(toPath(winner.stream.deepLinks.player));
            })
            .catch(() => undefined);
    }, [tiers, baseUrl, metaId, videoId, startFraction, runtimeSeconds, core.transport, navigate, toast, t]);

    // Plays a quality: its copy that is all on disk when it has one (no race:
    // it starts at once and downloads nothing; the toast says so); else the
    // copy downloaded ahead (any quality for autoplay, which follows the copy
    // chosen for the user; only the clicked quality's otherwise); else the race.
    const play = useCallback((tierKey: string, remember = true, auto = false) => {
        playStarted.current = true;
        const tier = tiers.find((tier) => tier.key === tierKey);
        const direct = (source: RankedSource) => {
            if (remember && metaId !== null) {
                setQuality(metaId, tierKey);
            }
            playDirect(source);
        };
        if (tier?.disk) {
            direct(tier.disk);
            return;
        }
        downloadedCopy().then((copy) => {
            const inTier = copy !== null && tier?.sources.some((s) => s.infoHash === copy.infoHash && s.fileIdx === copy.fileIdx) === true;
            if (copy !== null && (auto || inTier)) {
                direct(copy);
            } else {
                raceTier(tierKey, remember);
            }
        });
    }, [downloadedCopy, playDirect, raceTier, tiers, metaId]);

    // Resume / next episode asked to autoplay this video: once the remembered
    // quality has a live count (or nothing more is coming), play it. If the
    // episode doesn't offer it, the closest quality plays (fallbackTier); the
    // quality-change toast above says so, and the remembered one stays.
    useEffect(() => {
        if (autoplay === null || autoplay.metaId !== metaId || autoplay.videoId !== videoId) {
            return;
        }
        const tier = remembered !== null ? tiers.find((tier) => tier.key === remembered) : undefined;
        if (tier !== undefined && (tier.best !== null || (settled && !streamsLoading))) {
            setState({ autoplay: null });
            play(tier.key, true, true);
        } else if (tier === undefined && !streamsLoading) {
            const fallback = remembered !== null ? fallbackTier(tiers, remembered) : undefined;
            if (fallback !== undefined && (fallback.best !== null || settled)) {
                setState({ autoplay: null });
                play(fallback.key, false, true);
            } else if (fallback === undefined || settled) {
                setState({ autoplay: null });
            }
        }
    }, [autoplay, metaId, videoId, remembered, tiers, settled, streamsLoading, play]);

    useEffect(() => () => raceController.current?.abort(), []);

    return { tiers, settled, racing, remembered, play, cancel };
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

export { requestAutoplay, hasRememberedQuality, offersRememberedQuality };
export default useSmartPlay;
