// Copyright (C) 2017-2026 Smart code 203358507

// An episode's sources, fetched from the add-ons directly (not through core):
// the flows that choose a source without the episode's page open (the Next
// slot, the mid-episode switch, manual downloads) use these.

import type { StreamGroup } from 'stremio/routes/Details/Streams/useStreamsByAddon';
import { groupByQuality, rankTiers } from './groupByQuality';
import type { RankedTier } from './groupByQuality';
import { withDisk } from './withDisk';
import type { Download } from './downloads';
import { getLastQuality, getQuality } from './qualityMemory';

// The quality a show starts from when none was ever played.
const DEFAULT_QUALITY = '4k';

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
    return Promise.all(streamAddons(addons, type, videoId).map(async (addon) => {
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
};

// An episode's sources as ranked tiers, with its copies on disk, leaving out
// the torrents in `exclude` (known slow, or the one being replaced).
const episodeTiers = async (addons: Addon[], type: string, videoId: string, records: Download[] | null, exclude: ReadonlySet<string> = new Set()): Promise<RankedTier[]> => {
    const groups = await fetchStreams(addons, type, videoId);
    return withDisk(rankTiers(groupByQuality(groups, null), new Map()), records, videoId)
        .map((tier) => ({ ...tier, sources: tier.sources.filter((source) => !exclude.has(source.infoHash)) }));
};

// The quality a show's picks start from: its own, else the last played
// anywhere, else DEFAULT_QUALITY.
const startQuality = (metaId: string) => getQuality(metaId) ?? getLastQuality() ?? DEFAULT_QUALITY;

export { episodeTiers, fetchStreams, startQuality, DEFAULT_QUALITY };
