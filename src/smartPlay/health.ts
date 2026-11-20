// One rule for "fast enough", shared by every flow (plan 11): a source is
// healthy when its download rate covers what is still missing of its file
// before playback reaches the end, with a margin.
//
//   need    = bytes still missing / seconds until playback reaches the end
//   healthy = rate >= MARGIN x need
//
// A file all on disk needs nothing (always healthy); a partly downloaded one
// needs less. Rates come from RateMeter (supply.ts) and, until a live one
// arrives, from the 24 h memory (rateMemory.ts).

const MARGIN = 1.25;

type NeedInput = {
    // The file's size in bytes, or null when unknown.
    size: number | null,
    // Bytes of it already on disk (the server's record), 0 when none.
    completed: number,
    // The episode's runtime in seconds, or null when unknown.
    runtime: number | null,
    // Where playback starts, as a fraction of the file.
    startFraction: number,
};

// Bytes per second the source must deliver; 0 when nothing is missing, null
// when the size or runtime is unknown.
const need = ({ size, completed, runtime, startFraction }: NeedInput): number | null => {
    if (size === null || size <= 0) {
        return null;
    }
    if (completed >= size) {
        return 0;
    }
    if (runtime === null || runtime <= 0) {
        return null;
    }
    const start = Math.min(Math.max(startFraction, 0), 1);
    const ahead = size * (1 - start);
    // Downloaded bytes are spread over the file; those past the start count.
    const missing = Math.max(0, ahead - completed * (1 - start));
    const seconds = runtime * (1 - start);
    return seconds > 0 ? missing / seconds : 0;
};

// Speed to spare: rate / need (Infinity when nothing is needed), null when
// either is unknown.
const healthRatio = (rate: number | null, needed: number | null): number | null => {
    if (needed === 0) {
        return Infinity;
    }
    if (rate === null || needed === null) {
        return null;
    }
    return rate / needed;
};

const isHealthy = (rate: number | null, needed: number | null): boolean => {
    const ratio = healthRatio(rate, needed);
    return ratio !== null && ratio >= MARGIN;
};

export type { NeedInput };
export { MARGIN, need, healthRatio, isHealthy };
