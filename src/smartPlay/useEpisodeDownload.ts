// The streams page's download buttons: a quality row's ⬇ races that tier's
// top sources and keeps the fastest; a source's ⬇ keeps that source. The episode's state for the buttons: its record (kept, not
// complete) or a race running for it.

import { useCallback, useMemo } from 'react';
import { useDownloads } from './downloads';
import type { Download } from './downloads';
import { keepSource, racingFor, startDownload, useManualDownloads } from './manualDownload';
import type { Job, Show } from './manualDownload';
import { planEpisode } from './seasonPlan';
import type { RankedSource, RankedTier } from './groupByQuality';

const POLL = 5000;

type Options = {
    show: Show | null,
    video: { id: string, season?: number, episode?: number, title?: string | null } | null,
    tiers: RankedTier[],
    enabled: boolean,
};

type EpisodeDownloadState = {
    // The episode's kept record still downloading (or paused), if any.
    record: Download | null,
    // A race running for this episode, if any.
    racing: Job | null,
    download: (tierKey: string) => void,
    downloadSource: (source: RankedSource, tierKey: string) => void,
};

const useEpisodeDownload = ({ show, video, tiers, enabled }: Options): EpisodeDownloadState => {
    const { downloads, baseUrl } = useDownloads(POLL, enabled && video !== null);
    const jobs = useManualDownloads();
    const videoId = video?.id ?? null;
    const record = useMemo(() => (downloads ?? []).find((d) => d.kind === 'kept' && d.state !== 'complete' && d.meta?.metaId === show?.id && d.meta?.videoId === videoId) ?? null, [downloads, show, videoId]);
    const racing = videoId !== null ? racingFor(jobs, videoId) : null;

    const offerOf = useCallback(() => video === null ? null : {
        videoId: video.id,
        season: video.season ?? 0,
        episode: video.episode ?? 0,
        title: video.title ?? null,
        unaired: false,
        downloading: false,
        tiers,
    }, [video, tiers]);

    const download = useCallback((tierKey: string) => {
        const offer = offerOf();
        const planned = offer !== null ? planEpisode(offer, tierKey) : null;
        if (baseUrl === null || show === null || planned === null) {
            return;
        }
        startDownload(baseUrl, show, 'episode', tierKey, planned.label, [planned]);
    }, [baseUrl, show, offerOf]);

    const downloadSource = useCallback((source: RankedSource, tierKey: string) => {
        const offer = offerOf();
        const planned = offer !== null ? planEpisode(offer, tierKey) : null;
        if (baseUrl === null || show === null || planned === null || source.fileIdx === null) {
            return;
        }
        keepSource(baseUrl, show, tierKey, { ...planned, stepped: false, label: tiers.find((tier) => tier.key === tierKey)?.label ?? planned.label, sources: [source] }, source);
    }, [baseUrl, show, offerOf, tiers]);

    return { record, racing, download, downloadSource };
};

export type { EpisodeDownloadState };
export default useEpisodeDownload;
