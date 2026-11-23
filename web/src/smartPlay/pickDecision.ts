// Copyright (C) 2017-2026 Smart code 203358507

// The picker's decision for one quality step (picker.ts), kept free of I/O so
// it can be tested on its own.

import { isHealthy } from './health';
import type { RankedSource } from './groupByQuality';

type Candidate = {
    source: RankedSource,
    need: number | null,
    rate: number | null,
    // rate / need; Infinity on disk; null while unknown.
    ratio: number | null,
    // The rate comes from this play's measurement (or the file is on disk),
    // not the 24 h memory.
    live: boolean,
};

// A copy on disk wins at once. Otherwise, once every candidate has a live
// rate (or the time is up), the healthiest healthy one; 'none' when nobody is
// healthy then; null while it should keep measuring. A step needn't wait for
// its slowest source either: once one has kept up for HEALTHY_FOR
// (healthyLongEnough), the healthiest healthy one plays. All of a step's
// sources are the same quality, so waiting longer only changes which healthy
// source wins; it cost up to the whole step timeout when one never connected.
const decide = (candidates: Candidate[], timeUp: boolean, healthyLongEnough = false): Candidate | 'none' | null => {
    const onDisk = candidates.find((c) => c.need === 0);
    if (onDisk !== undefined) {
        return onDisk;
    }
    const healthy = candidates.filter((c) => c.live && isHealthy(c.rate, c.need));
    if (!timeUp && !candidates.every((c) => c.live) && !(healthyLongEnough && healthy.length > 0)) {
        return null;
    }
    if (healthy.length === 0) {
        return 'none';
    }
    return healthy.reduce((best, c) => (c.ratio ?? 0) > (best.ratio ?? 0) ? c : best);
};

// How long a source must keep up before a step stops waiting for the rest.
const HEALTHY_FOR = 3000;

export type { Candidate };
export { decide, HEALTHY_FOR };
