// Streams-list side of smart play: groups sources by quality, ranks them by
// live seeders, and plays a quality by racing its best sources.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router';
import { toPath } from 'stremio-router';
import { useCore } from 'stremio/core';
import useStreamingServer from 'stremio/common/useStreamingServer';
import type { StreamGroup } from 'stremio/routes/Details/Streams/useStreamsByAddon';
import { groupByQuality, offersQuality } from './groupByQuality';
import useLiveSeeders from './useLiveSeeders';
import race from './race';
import type { RacerProgress } from './race';
import { preload } from './serverApi';
import { getLastQuality, getQuality, setQuality } from './qualityMemory';
import { setState, useSmartPlayState } from './session';

type Options = {
    groups: StreamGroup[],
    // Addons still loading streams.
    streamsLoading: boolean,
    metaId: string | null,
    videoId: string | null,
    libraryItem: LibraryItem | null,
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

const useSmartPlay = ({ groups, streamsLoading, metaId, videoId, libraryItem, enabled }: Options) => {
    const core = useCore();
    const navigate = useNavigate();
    // The URL the player streams from locally (baseUrl is the LAN address used for casting).
    const baseUrl = useStreamingServer().selected?.transportUrl ?? null;
    const grouped = useMemo(() => enabled && videoId !== null ? groupByQuality(groups, videoId) : [], [enabled, groups, videoId]);
    const { tiers, settled } = useLiveSeeders(grouped, baseUrl);
    const { autoplay } = useSmartPlayState();
    const [racing, setRacing] = useState<Racing | null>(null);
    const raceController = useRef<AbortController | null>(null);

    const remembered = metaId !== null ? getQuality(metaId) : null;

    // Preload: open the likely quality's best sources before any click, so
    // their peers and metadata are ready when the race starts. The likely
    // quality is the show's remembered one, or the last played anywhere.
    const preloaded = useRef(new Set<string>());
    const likelyTier = tiers.find((tier) => tier.key === (remembered ?? getLastQuality()));
    const preloadSources = useMemo(() => likelyTier !== undefined && (likelyTier.best !== null || settled) ? likelyTier.sources.slice(0, PRELOAD_SOURCES) : [], [likelyTier, settled]);
    const preloadKey = preloadSources.map(({ infoHash }) => infoHash).join(',');
    useEffect(() => {
        if (baseUrl === null || preloadSources.length === 0) {
            return;
        }
        const timer = setTimeout(() => {
            preloadSources
                .filter(({ infoHash }) => !preloaded.current.has(infoHash))
                .forEach(({ infoHash, fileIdx, trackers }) => {
                    preloaded.current.add(infoHash);
                    preload(baseUrl, infoHash, fileIdx, trackers);
                });
        }, PRELOAD_DEBOUNCE);
        return () => clearTimeout(timer);
        // preloadKey captures the source set; preloadSources changes identity on every count update.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [baseUrl, preloadKey]);

    // Where the player will start, as a fraction of the file (resume point).
    const startFraction = libraryItem !== null && libraryItem.state.video_id === videoId && libraryItem.state.duration > 0 ?
        Math.min(1, libraryItem.state.timeOffset / libraryItem.state.duration)
        :
        0;

    const cancel = useCallback(() => {
        raceController.current?.abort();
        raceController.current = null;
        setRacing(null);
    }, []);

    const play = useCallback((tierKey: string) => {
        const tier = tiers.find((tier) => tier.key === tierKey);
        if (baseUrl === null || metaId === null || videoId === null || tier === undefined || tier.sources.length === 0) {
            return;
        }
        setQuality(metaId, tierKey);
        raceController.current?.abort();
        const controller = new AbortController();
        raceController.current = controller;
        setRacing({ tierKey, label: tier.label, weak: tier.weak, racers: [] });
        const onProgress = (racers: RacerProgress[]) => {
            setRacing((current) => current !== null && current.tierKey === tierKey ? { ...current, racers } : current);
        };
        race({ baseUrl, sources: tier.sources, startFraction, signal: controller.signal, onProgress })
            .then((winner) => {
                if (controller.signal.aborted || typeof winner.stream.deepLinks?.player !== 'string') {
                    return;
                }
                core.transport.analytics({ event: 'StreamClicked', args: { stream: winner.stream } });
                setRacing(null);
                raceController.current = null;
                navigate(toPath(winner.stream.deepLinks.player));
            })
            .catch(() => undefined);
    }, [tiers, baseUrl, metaId, videoId, startFraction, core.transport, navigate]);

    // Resume / next episode asked to autoplay this video: once the remembered
    // quality has a live count (or nothing more is coming), play it.
    useEffect(() => {
        if (autoplay === null || autoplay.metaId !== metaId || autoplay.videoId !== videoId) {
            return;
        }
        const tier = remembered !== null ? tiers.find((tier) => tier.key === remembered) : undefined;
        if (tier !== undefined && (tier.best !== null || (settled && !streamsLoading))) {
            setState({ autoplay: null });
            play(tier.key);
        } else if (tier === undefined && !streamsLoading) {
            setState({ autoplay: null });
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
