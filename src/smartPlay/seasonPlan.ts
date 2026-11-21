// Manual downloads (plan 12), the pure part: what a season download in one
// quality would fetch, episode by episode, and which torrents its race tests.
//
// Each episode takes the chosen quality, or the first step below it it has
// (the shared order, candidates.ts). Episodes with a complete copy on disk,
// already downloading, or not aired yet are skipped. The race tests each
// episode's top sources by live seeders; a torrent shared by several episodes
// (a season pack) is tested once, on its earliest episode's file, and its
// rate counts for every episode it holds.

import { qualitySteps, STEP_SOURCES } from './candidates';
import { resolutionOf } from './groupByQuality';
import type { RankedSource, RankedTier } from './groupByQuality';

// How many torrents one race tests at once.
const MAX_TESTS = 12;

type EpisodeOffer = {
    videoId: string,
    season: number,
    episode: number,
    title: string | null,
    // Not aired yet.
    unaired: boolean,
    // A record for this episode is already downloading (not complete).
    downloading: boolean,
    // A complete kept record names this episode (whether or not an add-on
    // still lists its source).
    onDisk?: boolean,
    tiers: RankedTier[],
};

type PlannedEpisode = {
    videoId: string,
    season: number,
    episode: number,
    title: string | null,
    // The step this episode takes, and whether it's below the chosen quality.
    label: string,
    stepped: boolean,
    // The sources its race compares, best seeded first (with a file index).
    sources: RankedSource[],
};

type SeasonRow = {
    key: string,
    label: string,
    episodes: PlannedEpisode[],
    // Estimated: each episode's best-seeded source.
    bytes: number,
    // The weakest episode's best live seeders (null while unknown).
    weakest: number | null,
    skipped: { onDisk: number, downloading: number, unaired: number },
};

const hasDisk = (offer: EpisodeOffer) => offer.onDisk === true || offer.tiers.some((tier) => tier.disk !== null);

// The quality keys any episode offers, in the order the episodes list them
// (each episode's tiers are already ranked like the quality rows).
const offeredKeys = (offers: EpisodeOffer[]) => {
    const keys = new Map<string, string>();
    offers.forEach((offer) => offer.tiers.forEach((tier) => {
        if (tier.sources.length > 0 && !keys.has(tier.key)) {
            keys.set(tier.key, tier.label);
        }
    }));
    return keys;
};

const planEpisode = (offer: EpisodeOffer, key: string): PlannedEpisode | null => {
    const steps = qualitySteps(offer.tiers, key);
    const step = steps.find((s) => s.sources.some((source) => source.fileIdx !== null));
    if (step === undefined) {
        return null;
    }
    const sources = step.sources.filter((source) => source.fileIdx !== null).slice(0, STEP_SOURCES);
    // A key the episode doesn't offer starts the order at its resolution's
    // labels, which the order calls exact; for a download it's a step down.
    const exact = step.match === 'exact' && step.key === key;
    // Named by the quality of its best-seeded source, not the whole step.
    const label = exact ? step.label : offer.tiers.find((tier) => tier.sources.includes(sources[0]))?.label ?? step.label;
    return {
        videoId: offer.videoId,
        season: offer.season,
        episode: offer.episode,
        title: offer.title,
        label,
        stepped: !exact,
        sources,
    };
};

const seasonRows = (offers: EpisodeOffer[]): SeasonRow[] => {
    const skipped = {
        onDisk: offers.filter(hasDisk).length,
        downloading: offers.filter((offer) => !hasDisk(offer) && offer.downloading).length,
        unaired: offers.filter((offer) => !hasDisk(offer) && !offer.downloading && offer.unaired).length,
    };
    const wanted = offers.filter((offer) => !hasDisk(offer) && !offer.downloading && !offer.unaired);
    // Highest resolution first (labels without one last), keeping the
    // add-ons' order within a resolution.
    const keys = Array.from(offeredKeys(offers))
        .map((entry, index) => ({ entry, index, resolution: resolutionOf(entry[0]) ?? -1 }))
        .sort((a, b) => b.resolution - a.resolution || a.index - b.index)
        .map(({ entry }) => entry);
    return keys.map(([key, label]) => {
        const episodes = wanted
            .map((offer) => planEpisode(offer, key))
            .filter((planned): planned is PlannedEpisode => planned !== null);
        const bytes = episodes.reduce((sum, planned) => sum + (planned.sources[0]?.size ?? 0), 0);
        const seeders = episodes.map((planned) => planned.sources[0]?.seeders ?? null);
        const weakest = seeders.length > 0 && seeders.every((s) => s !== null) ? Math.min(...seeders as number[]) : null;
        return { key, label, episodes, bytes, weakest, skipped };
    });
};

// Episode numbers in runs, e.g. [3, 4, 5, 8] -> "E3–E5, E8".
const episodeRuns = (episodes: number[]): string => {
    const sorted = [...episodes].sort((a, b) => a - b);
    const runs: string[] = [];
    for (let i = 0; i < sorted.length;) {
        let j = i;
        while (j + 1 < sorted.length && sorted[j + 1] === sorted[j] + 1) {
            j++;
        }
        runs.push(i === j ? `E${sorted[i]}` : `E${sorted[i]}–E${sorted[j]}`);
        i = j + 1;
    }
    return runs.join(', ');
};

// The row's step-downs, grouped by the quality taken: [{ label, episodes }].
const stepDowns = (row: SeasonRow) => {
    const byLabel = new Map<string, number[]>();
    row.episodes.filter((planned) => planned.stepped).forEach((planned) => {
        byLabel.set(planned.label, [...(byLabel.get(planned.label) ?? []), planned.episode]);
    });
    return Array.from(byLabel, ([label, episodes]) => ({ label, episodes: episodeRuns(episodes) }));
};

type Test = { source: RankedSource, videoId: string };

const bySeeders = (a: RankedSource, b: RankedSource) => (b.seeders ?? b.listedSeeders ?? -1) - (a.seeders ?? a.listedSeeders ?? -1);

// The torrents a race tests, in waves of at most `max` (raced one after the
// other): every episode's top source (so each has at least one measured),
// earliest episodes first; the last wave is filled with the other sources by
// seeders. Each torrent once, on the file of the earliest episode that lists
// it (a season pack is measured once for all its episodes).
const testWaves = (episodes: PlannedEpisode[], max = MAX_TESTS): Map<string, Test>[] => {
    const ordered = [...episodes].sort((a, b) => a.season - b.season || a.episode - b.episode);
    const firstFile = new Map<string, Test>();
    ordered.forEach((planned) => planned.sources.forEach((source) => {
        if (!firstFile.has(source.infoHash)) {
            firstFile.set(source.infoHash, { source, videoId: planned.videoId });
        }
    }));
    const tops = Array.from(new Set(ordered.filter((planned) => planned.sources.length > 0).map((planned) => planned.sources[0].infoHash)));
    const waves: Map<string, Test>[] = [];
    for (let i = 0; i < tops.length; i += max) {
        waves.push(new Map(tops.slice(i, i + max).map((infoHash) => [infoHash, firstFile.get(infoHash)!])));
    }
    const last = waves[waves.length - 1];
    if (last !== undefined) {
        ordered.flatMap((planned) => planned.sources.slice(1)).sort(bySeeders).forEach((source) => {
            if (last.size < max && !waves.some((wave) => wave.has(source.infoHash))) {
                last.set(source.infoHash, firstFile.get(source.infoHash)!);
            }
        });
    }
    return waves;
};

// An episode's winner: the source that would finish soonest (largest rate ÷
// size) among those with a measured rate; with none measured, the best
// seeded.
const chooseWinner = (planned: PlannedEpisode, rates: Map<string, number | null>): RankedSource | null => {
    const measured = planned.sources
        .map((source) => ({ source, rate: rates.get(source.infoHash) ?? null }))
        .filter((c): c is { source: RankedSource, rate: number } => c.rate !== null && c.rate > 0);
    if (measured.length > 0) {
        return measured.reduce((best, c) => c.rate / (c.source.size ?? 1) > best.rate / (best.source.size ?? 1) ? c : best).source;
    }
    return planned.sources[0] ?? null;
};

// The record order the server ranks files by (earliest episode first).
const orderOf = (season: number, episode: number) => season * 1000 + episode;

export type { EpisodeOffer, PlannedEpisode, SeasonRow, Test };
export { seasonRows, planEpisode, stepDowns, episodeRuns, testWaves, chooseWinner, orderOf, MAX_TESTS };
