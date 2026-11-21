// Copyright (C) 2017-2026 Smart code 203358507

// The watch's timing, kept free of I/O so it can be tested: a
// download's rate over a sliding window of its bytes on disk, and the rule
// that a source unhealthy for UNHEALTHY_FOR in a row gets replaced.

// How often the watch checks.
const WATCH_EVERY = 10000;
// A source unhealthy this long in a row is replaced.
const UNHEALTHY_FOR = 30000;
// The rate window, and the least span of samples it needs.
const RATE_WINDOW = 30000;
const RATE_MIN_SPAN = 15000;

// Rate from a record's bytes on disk: the bytes gained over the last
// RATE_WINDOW (a stalled download reads 0, unlike RateMeter, which only
// counts intervals that moved).
class WindowRate {
    private samples: { at: number, bytes: number }[] = [];

    add(at: number, bytes: number) {
        const last = this.samples[this.samples.length - 1];
        if (last !== undefined && bytes < last.bytes) {
            // The data was deleted or replaced: start over.
            this.samples = [];
        }
        this.samples.push({ at, bytes });
        while (this.samples.length > 2 && at - this.samples[1].at >= RATE_WINDOW) {
            this.samples.shift();
        }
    }

    rate(): number | null {
        const first = this.samples[0];
        const last = this.samples[this.samples.length - 1];
        if (first === undefined || last.at - first.at < RATE_MIN_SPAN) {
            return null;
        }
        return (last.bytes - first.bytes) / ((last.at - first.at) / 1000);
    }
}

// true once a source has been unhealthy UNHEALTHY_FOR in a row; a healthy
// check resets it, an unknown one (no rate yet) leaves it as it is.
class HealthWatch {
    private since: number | null = null;

    observe(at: number, healthy: boolean | null): boolean {
        if (healthy === true) {
            this.since = null;
            return false;
        }
        if (healthy === null) {
            return false;
        }
        if (this.since === null) {
            this.since = at;
        }
        if (at - this.since >= UNHEALTHY_FOR) {
            this.since = null;
            return true;
        }
        return false;
    }

    reset() {
        this.since = null;
    }
}

export { WindowRate, HealthWatch, WATCH_EVERY, UNHEALTHY_FOR };
