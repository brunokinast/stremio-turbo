// The quality order every flow follows (plan 11): the exact remembered label,
// then the other labels at the same resolution, then each lower resolution
// (all its labels). Each step holds its top sources by live seeders.

import { resolutionOf } from './groupByQuality';
import type { RankedSource, RankedTier } from './groupByQuality';

// How many sources a step tests.
const STEP_SOURCES = 3;

type Match = 'exact' | 'same' | 'lower';

type Step = {
    // The tier key a play of this step remembers (the exact one, or the
    // step's best-seeded tier).
    key: string,
    label: string,
    match: Match,
    sources: RankedSource[],
};

const bySeeders = (a: RankedSource, b: RankedSource) => {
    if (a.seeders !== null || b.seeders !== null) {
        return (b.seeders ?? -1) - (a.seeders ?? -1);
    }
    return (b.listedSeeders ?? -1) - (a.listedSeeders ?? -1);
};

const stepOf = (tiers: RankedTier[], match: Match): Step => {
    const sources = tiers.flatMap((tier) => tier.sources).sort(bySeeders);
    // A copy on disk is always among the tested sources: it wins at once.
    const onDisk = sources.filter((source) => source.onDisk !== null);
    const top = [...onDisk, ...sources.filter((source) => source.onDisk === null)].slice(0, Math.max(STEP_SOURCES, onDisk.length));
    const lead = tiers.reduce((best, tier) => (tier.best ?? -1) > (best.best ?? -1) ? tier : best, tiers[0]);
    return {
        key: match === 'exact' ? tiers[0].key : lead.key,
        label: tiers.length === 1 ? tiers[0].label : tiers.map((tier) => tier.label).join(' / '),
        match,
        sources: top,
    };
};

// The steps from startKey down. A start key not offered begins at its
// resolution's labels; one with no resolution (e.g. "web-dl") is its own
// step, followed by everything at or below 4K, best first.
const qualitySteps = (tiers: RankedTier[], startKey: string): Step[] => {
    const offered = tiers.filter((tier) => tier.sources.length > 0);
    const steps: Step[] = [];
    const exact = offered.find((tier) => tier.key === startKey);
    if (exact !== undefined) {
        steps.push(stepOf([exact], 'exact'));
    }
    const startResolution = resolutionOf(startKey) ?? 2160;
    const same = offered.filter((tier) => tier !== exact && resolutionOf(tier.key) === startResolution);
    if (same.length > 0) {
        steps.push(stepOf(same, exact !== undefined ? 'same' : 'exact'));
    }
    const lower = Array.from(new Set(offered
        .map((tier) => resolutionOf(tier.key))
        .filter((resolution): resolution is number => resolution !== null && resolution < startResolution)))
        .sort((a, b) => b - a);
    lower.forEach((resolution) => {
        steps.push(stepOf(offered.filter((tier) => resolutionOf(tier.key) === resolution), 'lower'));
    });
    return steps;
};

export type { Match, Step };
export { qualitySteps, STEP_SOURCES };
