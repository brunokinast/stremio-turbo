// Races a quality's sources: each reads the same stretch of its file and the
// first to deliver it wins; the others are removed from the server. The pieces
// read stay in the server's cache, so the player starts from them instantly.
//
// Weak swarms (typical for 4K) can take a minute just to find peers and fetch
// metadata, so the race never picks blindly: it waits for real data, adds
// more sources while none delivers, and reports each racer's progress.

import { readRange, remove, stats, streamUrl } from './serverApi';
import type { RankedSource } from './groupByQuality';

const START_RACERS = 3;
const MAX_RACERS = 7;
// Racers added each time WIDEN_AFTER passes with no data from any of them.
const WIDEN_BY = 2;
const WIDEN_AFTER = 15000;
const RACE_BYTES = 8 * 1024 * 1024;
// Once some racer has data, the one with the most bytes wins after this long.
const SETTLE_AFTER = 30000;
const POLL_INTERVAL = 1000;
const RETRY_DELAY = 2000;

type RacerPhase = 'metadata' | 'connecting' | 'downloading';

type RacerProgress = {
    infoHash: string,
    seeders: number | null,
    phase: RacerPhase,
    peers: number,
    // Bytes per second.
    speed: number,
};

type RaceOptions = {
    baseUrl: string,
    sources: RankedSource[],
    // Fraction of the file the player will start at (resume position).
    startFraction: number,
    signal: AbortSignal,
    onProgress: (racers: RacerProgress[]) => void,
};

type Racer = {
    source: RankedSource,
    read: number,
    hasMetadata: boolean,
    peers: number,
    speed: number,
    controller: AbortController,
};

// Sources whose metadata the server already has (preloaded) go first: they
// can deliver right away. Otherwise the live-seeder order stands.
const orderByReadiness = async (baseUrl: string, sources: RankedSource[]): Promise<RankedSource[]> => {
    const candidates = sources.slice(0, MAX_RACERS);
    const ready = await Promise.all(candidates.map((source) => stats(baseUrl, source.infoHash).then((s) => s !== null && s.files.length > 0)));
    return [
        ...candidates.filter((_, idx) => ready[idx]),
        ...candidates.filter((_, idx) => !ready[idx]),
    ];
};

const phaseOf = (racer: Racer): RacerPhase => {
    if (racer.read > 0) {
        return 'downloading';
    }
    return racer.hasMetadata ? 'connecting' : 'metadata';
};

const race = async ({ baseUrl, sources, startFraction, signal, onProgress }: RaceOptions): Promise<RankedSource> => {
    if (sources.length <= 1) {
        return sources[0];
    }
    const queue = await orderByReadiness(baseUrl, sources);
    if (signal.aborted) {
        throw new DOMException('Race cancelled', 'AbortError');
    }

    return new Promise((resolve, reject) => {
        const racers: Racer[] = [];
        const startedAt = Date.now();
        let lastWiden = startedAt;
        let settled = false;

        const report = () => onProgress(racers.map((racer) => ({
            infoHash: racer.source.infoHash,
            seeders: racer.source.seeders,
            phase: phaseOf(racer),
            peers: racer.peers,
            speed: racer.speed,
        })));

        const stop = () => {
            settled = true;
            clearInterval(poll);
            racers.forEach((racer) => racer.controller.abort());
        };

        const finish = (winner: Racer) => {
            if (settled) {
                return;
            }
            stop();
            racers.forEach((racer) => {
                if (racer !== winner) {
                    remove(baseUrl, racer.source.infoHash);
                }
            });
            resolve(winner.source);
        };

        const start = (source: RankedSource) => {
            const racer: Racer = { source, read: 0, hasMetadata: false, peers: 0, speed: 0, controller: new AbortController() };
            racers.push(racer);
            const offset = source.size !== null ? Math.max(0, Math.min(Math.floor(source.size * startFraction), source.size - RACE_BYTES)) : 0;
            const url = streamUrl(baseUrl, source.infoHash, source.fileIdx, source.trackers);
            // The server ends a stream that stalls for long (or times out
            // waiting for metadata); a stalled racer may still come good, so
            // it resumes from where it got to until the race is over.
            const read = () => {
                const done = racer.read;
                readRange(url, offset + done, RACE_BYTES - done, racer.controller.signal, (bytes) => {
                    racer.read = done + bytes;
                })
                    .then(() => {
                        if (racer.read >= RACE_BYTES) {
                            finish(racer);
                        } else if (!settled) {
                            setTimeout(read, RETRY_DELAY);
                        }
                    })
                    .catch(() => {
                        if (!settled) {
                            setTimeout(read, RETRY_DELAY);
                        }
                    });
            };
            read();
        };

        const tick = () => {
            if (settled) {
                return;
            }
            const now = Date.now();
            const leader = racers.reduce<Racer | null>((best, racer) => racer.read > (best?.read ?? 0) ? racer : best, null);
            if (leader !== null && now - startedAt >= SETTLE_AFTER) {
                finish(leader);
                return;
            }
            if (leader === null && now - lastWiden >= WIDEN_AFTER && racers.length < queue.length) {
                lastWiden = now;
                queue.slice(racers.length, racers.length + WIDEN_BY).forEach(start);
            }
            Promise.all(racers.map((racer) => stats(baseUrl, racer.source.infoHash).then((s) => {
                racer.hasMetadata = s !== null && s.files.length > 0;
                racer.peers = s?.peers ?? 0;
                racer.speed = s?.downloadSpeed ?? 0;
            }))).then(() => {
                if (!settled) {
                    report();
                }
            });
        };

        signal.addEventListener('abort', () => {
            if (!settled) {
                stop();
                reject(new DOMException('Race cancelled', 'AbortError'));
            }
        }, { once: true });

        queue.slice(0, START_RACERS).forEach(start);
        report();
        const poll = setInterval(tick, POLL_INTERVAL);
    });
};

export type { RacerProgress, RacerPhase };
export default race;
