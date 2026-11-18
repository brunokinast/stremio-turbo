// Whether a source's swarm can keep up with its file: the rate the server
// downloads it at (or the race reads it at, when it's already on disk) against
// the bitrate the file needs (size / runtime).
//
// On House of the Dragon S02E01 4K (streaming-research findings/20) a 31 GB
// remux's swarm delivered 0.4-0.7x its bitrate, steadily from the first
// seconds, and it stalled 54 s of 90; the 9.9 GB file's swarm ramped from
// about 0.9x in its first seconds to 7.8x. So a source keeps up only from its
// full bitrate, measured as the average while downloading (a peak of 1.4x let
// the remux through), and a dwell decides only when every larger source has
// been measured: one already on disk downloads nothing, so its rate shows only
// in the race's reads.

import type { RankedSource } from './groupByQuality';

const VIABLE_RATIO = 1;
// A swarm at this share of its bitrate may still ramp up to it (the 9.9 GB
// file's started near 0.9x; the remux's stayed under 0.1x in the race).
const PROMISING_RATIO = 0.5;
// Recent intervals with download activity a rate averages over.
const RATE_INTERVALS = 3;
// Smaller interval deltas aren't a download: a torrent whose data is already
// on disk still fetches a few KB (metadata, piece checks), which read as a
// rate far below any bitrate and made a cached 11.9 GB file look too slow.
const MIN_INTERVAL_BYTES = 256 * 1024;

// A show's runtime in seconds from Cinemeta-style text ("60 min", "1h 2min",
// "45"), or null.
const parseRuntime = (runtime: string | null | undefined): number | null => {
    if (typeof runtime !== 'string') {
        return null;
    }
    const hours = /(\d+)\s*h/i.exec(runtime);
    const minutes = /(\d+)\s*m/i.exec(runtime);
    if (hours === null && minutes === null) {
        const plain = /^\s*(\d+)\s*$/.exec(runtime);
        return plain !== null ? Number(plain[1]) * 60 : null;
    }
    const seconds = (hours !== null ? Number(hours[1]) * 3600 : 0) + (minutes !== null ? Number(minutes[1]) * 60 : 0);
    return seconds > 0 ? seconds : null;
};

// Bytes per second a source's file needs, or null when its size or the
// runtime is unknown.
const neededRate = (source: RankedSource, runtime: number | null): number | null => {
    return source.size !== null && runtime !== null && runtime > 0 ? source.size / runtime : null;
};

type Interval = { seconds: number, bytes: number };

// Download rate per torrent from the server's cumulative `downloaded` count:
// the average over its last RATE_INTERVALS intervals that had a download (a
// preloaded torrent stops once its primed pieces are in). A race read's rate
// counts too: data already on disk reads at once and downloads nothing.
class RateMeter {
    private last = new Map<string, { at: number, bytes: number }>();
    private active = new Map<string, Interval[]>();
    private reads = new Map<string, number>();

    add(infoHash: string, at: number, downloaded: number) {
        const prev = this.last.get(infoHash);
        if (prev !== undefined && at - prev.at < 500) {
            return;
        }
        if (prev !== undefined && downloaded - prev.bytes >= MIN_INTERVAL_BYTES) {
            const intervals = this.active.get(infoHash) ?? [];
            intervals.push({ seconds: (at - prev.at) / 1000, bytes: downloaded - prev.bytes });
            this.active.set(infoHash, intervals.slice(-RATE_INTERVALS));
        }
        this.last.set(infoHash, { at, bytes: downloaded });
    }

    // A race read's rate: bytes read since its first byte, per second.
    addRead(infoHash: string, rate: number) {
        this.reads.set(infoHash, rate);
    }

    rate(infoHash: string): number | null {
        const intervals = this.active.get(infoHash);
        const download = intervals !== undefined && intervals.length > 0 ?
            intervals.reduce((sum, { bytes }) => sum + bytes, 0) / intervals.reduce((sum, { seconds }) => sum + seconds, 0)
            :
            null;
        const read = this.reads.get(infoHash) ?? null;
        return download === null ? read : read === null ? download : Math.max(download, read);
    }
}

// true: keeps up; false: too slow so far; null: unknown (no bitrate or no rate).
const keepsUp = (rate: number | null, needed: number | null): boolean | null => {
    if (needed === null || rate === null) {
        return null;
    }
    return rate >= VIABLE_RATIO * needed;
};

// Not keeping up yet, but it might: no rate so far, or a promising one.
const mayKeepUp = (rate: number | null, needed: number | null): boolean => {
    return needed !== null && keepsUp(rate, needed) !== true && (rate === null || rate >= PROMISING_RATIO * needed);
};

type Passed = {
    source: RankedSource,
    rate: number,
    needed: number,
};

type Choice = {
    chosen: RankedSource,
    // Larger sources passed over because they couldn't keep up.
    passed: Passed[],
};

// "Best picture that keeps up": the highest-bitrate source among those keeping
// up, or null when none does yet. With measuredOnly, also null while a larger
// source has no rate yet (it might keep up). Larger sources measured and not
// keeping up are reported.
const chooseKeepingUp = (sources: RankedSource[], meter: RateMeter, runtime: number | null, measuredOnly = false): Choice | null => {
    const rated = sources.map((source) => ({ source, rate: meter.rate(source.infoHash), needed: neededRate(source, runtime) }));
    const viable = rated.filter(({ rate, needed }) => keepsUp(rate, needed) === true);
    if (viable.length === 0) {
        return null;
    }
    const best = viable.reduce((top, item) => item.needed! > top.needed! ? item : top);
    const larger = rated.filter(({ needed }) => needed !== null && needed > best.needed!);
    if (measuredOnly && larger.some(({ rate }) => rate === null)) {
        return null;
    }
    const passed = larger
        .filter(({ rate }) => rate !== null)
        .map(({ source, rate, needed }) => ({ source, rate: rate!, needed: needed! }));
    return { chosen: best.source, passed };
};

export type { Choice, Passed };
export { parseRuntime, neededRate, RateMeter, keepsUp, mayKeepUp, chooseKeepingUp, VIABLE_RATIO };
