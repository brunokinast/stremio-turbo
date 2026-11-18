// Picks the source to play among a quality's sources.
//
// When some were preloaded (the user stayed on the streams page), what the
// preload measured decides: the source with the most peers sending to us. A
// race between them would only time reads of data already cached, a coin flip
// (streaming-research findings/16).
//
// Otherwise the sources race: each reads the same stretch of its file and the
// first to deliver it wins; the others are removed from the server. A racer
// that has read ALONE_WIN_BYTES while no other has delivered anything wins at
// once: waiting for the full stretch cost 2-4 s on 4K plays and the others had
// not sent a byte (mpv needs under 1 MiB for its first frame). Race reads are
// probes, so the server keeps the file's head and tail ready for the player.
//
// Weak swarms (typical for 4K) can take a minute just to find peers and fetch
// metadata, so the race never picks blindly: it waits for real data, adds
// more sources while none delivers, and reports each racer's progress.

import { readRange, remove, stats, probeUrl } from './serverApi';
import type { RankedSource } from './groupByQuality';

const START_RACERS = 3;
const MAX_RACERS = 7;
// Racers added each time WIDEN_AFTER passes with no data from any of them.
const WIDEN_BY = 2;
const WIDEN_AFTER = 15000;
const RACE_BYTES = 8 * 1024 * 1024;
const ALONE_WIN_BYTES = 1024 * 1024;
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

type Readiness = {
    // Preloaded, with peers sending: the best of these is played without a race.
    best: RankedSource | null,
    // Race order otherwise: sources with metadata first, then the live-seeder order.
    queue: RankedSource[],
};

const readiness = async (baseUrl: string, sources: RankedSource[]): Promise<Readiness> => {
    const candidates = sources.slice(0, MAX_RACERS);
    const states = await Promise.all(candidates.map((source) => stats(baseUrl, source.infoHash)));
    let best: RankedSource | null = null;
    let bestUnchoked = 0;
    candidates.forEach((source, idx) => {
        const s = states[idx];
        if (s !== null && s.files.length > 0 && (s.unchoked ?? 0) > bestUnchoked) {
            best = source;
            bestUnchoked = s.unchoked;
        }
    });
    const hasMetadata = (idx: number) => states[idx] !== null && states[idx]!.files.length > 0;
    return {
        best,
        queue: [
            ...candidates.filter((_, idx) => hasMetadata(idx)),
            ...candidates.filter((_, idx) => !hasMetadata(idx)),
        ],
    };
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
    const { best, queue } = await readiness(baseUrl, sources);
    if (signal.aborted) {
        throw new DOMException('Race cancelled', 'AbortError');
    }
    if (best !== null) {
        const chosen: RankedSource = best;
        sources.forEach((source) => {
            if (source.infoHash !== chosen.infoHash) {
                remove(baseUrl, source.infoHash);
            }
        });
        return chosen;
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
            const url = probeUrl(baseUrl, source.infoHash, source.fileIdx, source.trackers);
            // The server ends a stream that stalls for long (or times out
            // waiting for metadata); a stalled racer may still come good, so
            // it resumes from where it got to until the race is over.
            const read = () => {
                const done = racer.read;
                readRange(url, offset + done, RACE_BYTES - done, racer.controller.signal, (bytes) => {
                    racer.read = done + bytes;
                    if (racer.read >= ALONE_WIN_BYTES && racers.every((other) => other === racer || other.read === 0)) {
                        finish(racer);
                    }
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
