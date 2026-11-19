// Groups torrent streams by the quality label their addon gives them (the 2nd
// line of stream.name, e.g. Torrentio's "4k HDR" or "1080p").

import type { StreamGroup } from 'stremio/routes/Details/Streams/useStreamsByAddon';
import { isKnown } from './serverApi';
import type { ScrapeResult } from './serverApi';

// A tier is weak when its best source has fewer live seeders than this.
const WEAK_SEEDERS = 10;

type Source = {
    stream: Stream,
    addonName: string,
    infoHash: string,
    fileIdx: number | null,
    trackers: string[],
    // The addon's own (often stale) seeder figure, e.g. Torrentio's 👤.
    listedSeeders: number | null,
    size: number | null,
};

type Tier = {
    key: string,
    label: string,
    sources: Source[],
};

type RankedSource = Source & {
    // Live seeders from the server, or null while unknown.
    seeders: number | null,
};

type RankedTier = {
    key: string,
    label: string,
    sources: RankedSource[],
    best: number | null,
    weak: boolean,
    // Watch progress (0–1) of the source last played from this tier, from core.
    progress: number | null,
};

const SIZE_UNITS: Record<string, number> = { KB: 1e3, MB: 1e6, GB: 1e9, TB: 1e12 };

// The addon's seeder figure in a stream description, e.g. Torrentio's "👤 8".
const LISTED_SEEDERS = /👤\s*(\d+)/u;

const parseListedSeeders = (description: string): number | null => {
    const match = LISTED_SEEDERS.exec(description);
    return match ? Number(match[1]) : null;
};

const parseSize = (stream: Stream): number | null => {
    if (typeof stream.behaviorHints?.videoSize === 'number') {
        return stream.behaviorHints.videoSize;
    }
    const match = /💾\s*([\d.]+)\s*(KB|MB|GB|TB)/u.exec(stream.description ?? '');
    return match ? Math.round(Number(match[1]) * SIZE_UNITS[match[2]]) : null;
};

const qualityLabel = (stream: Stream): string | null => {
    const label = (stream.name ?? '').split('\n')[1]?.trim();
    return label ? label : null;
};

const tierKey = (label: string) => label.toLowerCase();

// Core builds each stream's player link for the video it was requested for
// (…/{type}/{metaId}/{videoId}). Right after navigating to another episode
// the list can still hold the previous episode's streams; this filters them.
const isForVideo = (stream: Stream, videoId: string) => {
    return typeof stream.deepLinks?.player === 'string' && stream.deepLinks.player.endsWith(`/${encodeURIComponent(videoId)}`);
};

// videoId null: the streams were requested for one video already (an
// addon's own response, which carries no player links).
const groupByQuality = (groups: StreamGroup[], videoId: string | null): Tier[] => {
    const tiers = new Map<string, Tier>();
    groups.forEach(({ addon, streams }) => {
        streams.forEach((stream) => {
            const label = qualityLabel(stream);
            if (label === null || typeof stream.infoHash !== 'string' || (videoId !== null && !isForVideo(stream, videoId))) {
                return;
            }
            const key = tierKey(label);
            let tier = tiers.get(key);
            if (tier === undefined) {
                tier = { key, label, sources: [] };
                tiers.set(key, tier);
            }
            const infoHash = stream.infoHash.toLowerCase();
            if (tier.sources.some((source) => source.infoHash === infoHash && source.fileIdx === (stream.fileIdx ?? null))) {
                return;
            }
            tier.sources.push({
                stream,
                addonName: addon.manifest.name,
                infoHash,
                fileIdx: stream.fileIdx ?? null,
                trackers: stream.announce ?? [],
                listedSeeders: parseListedSeeders(stream.description ?? ''),
                size: parseSize(stream),
            });
        });
    });
    return Array.from(tiers.values());
};

// Orders each tier's sources by live seeders (unknown ones last, by the
// addon's figure), keeping the addon's tier order.
const rankTiers = (tiers: Tier[], live: Map<string, ScrapeResult>): RankedTier[] => {
    return tiers.map((tier) => {
        const sources = tier.sources
            .map((source): RankedSource => {
                const result = live.get(source.infoHash);
                return { ...source, seeders: result !== undefined && isKnown(result) ? result.seeders : null };
            })
            .sort((a, b) => {
                if (a.seeders !== null || b.seeders !== null) {
                    return (b.seeders ?? -1) - (a.seeders ?? -1);
                }
                return (b.listedSeeders ?? -1) - (a.listedSeeders ?? -1);
            });
        const best = sources[0]?.seeders ?? null;
        const progress = sources.find(({ stream }) => typeof stream.progress === 'number' && stream.progress > 0)?.stream.progress ?? null;
        return {
            key: tier.key,
            label: tier.label,
            sources,
            best,
            weak: best !== null && best < WEAK_SEEDERS,
            progress,
        };
    });
};

// The vertical resolution a tier key starts with ("4k dv | hdr" -> 2160,
// "1080p" -> 1080), or null when it names none ("web-dl").
const resolutionOf = (key: string): number | null => {
    const token = key.split(/[\s|]/)[0];
    if (token === '8k') {
        return 4320;
    }
    if (token === '4k') {
        return 2160;
    }
    const match = /^(\d{3,4})p$/.exec(token);
    return match ? Number(match[1]) : null;
};

// The tier to preload when the remembered quality isn't offered: the
// most-seeded tier at the resolution of lastKey (the last quality played, e.g.
// "4k hdr" -> a "4k ..." tier), else the first tier at 4K or below (tiers keep
// the addon's order, best quality first; an 8K tier is rarely what's wanted).
const fallbackTier = (tiers: RankedTier[], lastKey: string | null): RankedTier | undefined => {
    const offered = tiers.filter((tier) => tier.sources.length > 0);
    const lastResolution = lastKey !== null ? resolutionOf(lastKey) : null;
    if (lastResolution !== null) {
        const same = offered.filter((tier) => resolutionOf(tier.key) === lastResolution);
        const best = same.reduce<RankedTier | undefined>((top, tier) => top === undefined || (tier.best ?? -1) > (top.best ?? -1) ? tier : top, undefined);
        if (best !== undefined) {
            return best;
        }
    }
    return offered.find((tier) => {
        const resolution = resolutionOf(tier.key);
        return resolution !== null && resolution <= 2160;
    }) ?? offered[0];
};

// Whether any loaded stream carries the given quality.
const offersQuality = (streams: MetaDetails['streams'], videoId: string, key: string): boolean => {
    return streams.some((resource) => resource.content.type === 'Ready' &&
        resource.content.content.some((stream) => typeof stream.infoHash === 'string' && isForVideo(stream, videoId) && qualityLabel(stream)?.toLowerCase() === key));
};

export type { Source, Tier, RankedSource, RankedTier };
export { groupByQuality, rankTiers, tierKey, offersQuality, fallbackTier, resolutionOf, LISTED_SEEDERS, WEAK_SEEDERS };
