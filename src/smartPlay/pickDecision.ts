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
// healthy then; null while it should keep measuring.
const decide = (candidates: Candidate[], timeUp: boolean): Candidate | 'none' | null => {
    const onDisk = candidates.find((c) => c.need === 0);
    if (onDisk !== undefined) {
        return onDisk;
    }
    if (!timeUp && !candidates.every((c) => c.live)) {
        return null;
    }
    const healthy = candidates.filter((c) => c.live && isHealthy(c.rate, c.need));
    if (healthy.length === 0) {
        return 'none';
    }
    return healthy.reduce((best, c) => (c.ratio ?? 0) > (best.ratio ?? 0) ? c : best);
};

export type { Candidate };
export { decide };
