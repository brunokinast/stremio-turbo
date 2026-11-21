// Copyright (C) 2017-2026 Smart code 203358507

// Which sources are all on disk, from the server's records.

import { isComplete } from './downloads';
import type { Download } from './downloads';
import type { RankedSource, RankedTier } from './groupByQuality';

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

export { withDisk };
