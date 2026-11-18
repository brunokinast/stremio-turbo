// Picks the source to play among a quality's sources: the best picture that
// keeps up (supply.ts), i.e. the highest-bitrate source whose measured
// download rate covers its file. A first-to-arrive rule alone crowned a 31 GB
// remux whose swarm delivered 0.4x its bitrate; it stalled 54 s of 90
// (streaming-research findings/20).
//
// When some were preloaded (the user stayed on the streams page), the rates
// measured during that dwell decide, without a race.
//
// Otherwise the sources race: each reads the same stretch of its file, the
// server's download rate for each is measured, and the race ends as soon as a
// racer that has data keeps up: the highest-bitrate one keeping up wins, but
// for LARGER_GRACE not while a larger racer may still keep up. A racer that
// has read ALONE_WIN_BYTES while no other has delivered anything wins at once
// under the same terms, without waiting for the next poll: waiting cost 2-4 s
// on 4K plays (mpv needs under 1 MiB for its first frame). Without a runtime
// (no bitrate), the first to read ALONE_WIN_BYTES alone, else the full
// stretch, wins. Race reads
// are probes, so the server keeps the file's head and tail ready for the
// player.
//
// Weak swarms (typical for 4K) can take a minute just to find peers and fetch
// metadata, so the race never picks blindly: it waits for real data, adds
// more sources while none delivers, and reports each racer's progress.

import { readRange, remove, stats, probeUrl } from './serverApi';
import type { RankedSource } from './groupByQuality';
import { chooseKeepingUp, keepsUp, knownRate, mayKeepUp, neededRate, RateMeter } from './supply';
import { remember } from './rateMemory';
import type { Passed } from './supply';

const START_RACERS = 3;
const MAX_RACERS = 7;
// Racers added each time WIDEN_AFTER passes with no data from any of them.
const WIDEN_BY = 2;
const WIDEN_AFTER = 15000;
const RACE_BYTES = 8 * 1024 * 1024;
const ALONE_WIN_BYTES = 1024 * 1024;
// Once some racer has data, the best one wins after this long.
const SETTLE_AFTER = 30000;
// For this long a racer can't win while a larger one may still keep up: a
// small file already on disk would otherwise win before the better ones
// deliver (it took the 1.56 GB file over the 9.9 GB one in the real app).
const LARGER_GRACE = 10000;
const POLL_INTERVAL = 1000;
// Shortest elapsed time (ms) a read rate is computed over.
const READ_RATE_FLOOR = 50;
const RETRY_DELAY = 2000;

type RacerPhase = 'metadata' | 'connecting' | 'downloading';

type RacerProgress = {
    infoHash: string,
    seeders: number | null,
    phase: RacerPhase,
    peers: number,
    // Bytes per second.
    speed: number,
    // Whether its rate covers its file's bitrate; null while unknown.
    keepsUp: boolean | null,
};

type RaceOptions = {
    baseUrl: string,
    sources: RankedSource[],
    // Fraction of the file the player will start at (resume position).
    startFraction: number,
    // The episode's runtime in seconds (for bitrates), or null when unknown.
    runtime: number | null,
    // Download rates measured so far, e.g. during a dwell on the streams page.
    meter: RateMeter,
    signal: AbortSignal,
    onProgress: (racers: RacerProgress[]) => void,
};

type RaceResult = {
    source: RankedSource,
    // Larger sources passed over because their swarm couldn't keep up.
    passed: Passed[],
};

type Racer = {
    source: RankedSource,
    read: number,
    // When its first byte arrived (ms), for its read rate.
    firstByteAt: number | null,
    hasMetadata: boolean,
    peers: number,
    speed: number,
    controller: AbortController,
};

const phaseOf = (racer: Racer): RacerPhase => {
    if (racer.read > 0) {
        return 'downloading';
    }
    return racer.hasMetadata ? 'connecting' : 'metadata';
};

// Removes every source but the chosen one from the server.
const keepOnly = (baseUrl: string, sources: RankedSource[], chosen: RankedSource) => {
    sources.forEach((source) => {
        if (source.infoHash !== chosen.infoHash) {
            remove(baseUrl, source.infoHash);
        }
    });
};

// Stores each source's live rate ÷ bitrate for later plays (rateMemory.ts).
const rememberRates = (sources: RankedSource[], meter: RateMeter, runtime: number | null) => {
    sources.forEach((source) => {
        const rate = meter.rate(source.infoHash);
        const needed = neededRate(source, runtime);
        if (rate !== null && needed !== null) {
            remember(source.infoHash, rate / needed);
        }
    });
};

const race = async ({ baseUrl, sources, startFraction, runtime, meter, signal, onProgress }: RaceOptions): Promise<RaceResult> => {
    if (sources.length <= 1) {
        return { source: sources[0], passed: [] };
    }
    const candidates = sources.slice(0, MAX_RACERS);
    const states = await Promise.all(candidates.map((source) => stats(baseUrl, source.infoHash)));
    if (signal.aborted) {
        throw new DOMException('Race cancelled', 'AbortError');
    }
    const hasMetadata = (idx: number) => states[idx] !== null && states[idx]!.files.length > 0;
    const now = Date.now();
    states.forEach((s, idx) => {
        if (s !== null) {
            meter.add(candidates[idx].infoHash, now, s.downloaded);
        }
    });
    // A dwell measured the preloaded sources: decide without a race, unless a
    // larger one has no rate yet (e.g. already on disk: the race measures it).
    const dwell = chooseKeepingUp(candidates.filter((_, idx) => hasMetadata(idx)), meter, runtime, true);
    if (dwell !== null) {
        rememberRates(candidates, meter, runtime);
        keepOnly(baseUrl, sources, dwell.chosen);
        return { source: dwell.chosen, passed: dwell.passed };
    }
    // Sources with metadata first, then the live-seeder order.
    const queue = [
        ...candidates.filter((_, idx) => hasMetadata(idx)),
        ...candidates.filter((_, idx) => !hasMetadata(idx)),
    ];

    return new Promise((resolve, reject) => {
        const racers: Racer[] = [];
        const startedAt = Date.now();
        let lastWiden = startedAt;
        let settled = false;

        const keepsUpNow = (racer: Racer) => keepsUp(meter.rate(racer.source.infoHash), neededRate(racer.source, runtime));

        // Within the grace time, whether a racer larger than this source may still keep up.
        const largerMayKeepUp = (source: RankedSource) => {
            const needed = neededRate(source, runtime) ?? Infinity;
            return Date.now() - startedAt < LARGER_GRACE && racers.some((racer) => {
                const other = neededRate(racer.source, runtime);
                return other !== null && other > needed && mayKeepUp(knownRate(meter, racer.source, runtime)?.rate ?? null, other);
            });
        };

        const report = () => onProgress(racers.map((racer) => ({
            infoHash: racer.source.infoHash,
            seeders: racer.source.seeders,
            phase: phaseOf(racer),
            peers: racer.peers,
            speed: racer.speed,
            keepsUp: racer.read > 0 ? keepsUpNow(racer) : null,
        })));

        const stop = () => {
            settled = true;
            clearInterval(poll);
            racers.forEach((racer) => racer.controller.abort());
        };

        const finish = (winner: RankedSource, passed: Passed[]) => {
            if (settled) {
                return;
            }
            stop();
            rememberRates(racers.map((racer) => racer.source), meter, runtime);
            keepOnly(baseUrl, racers.map((racer) => racer.source), winner);
            resolve({ source: winner, passed });
        };

        // Every racer larger than the chosen source known not to keep up
        // (live, or remembered), delivered data or not.
        const passedOver = (chosen: RankedSource) => {
            const chosenNeeds = neededRate(chosen, runtime) ?? Infinity;
            return racers.flatMap((racer): Passed[] => {
                const needed = neededRate(racer.source, runtime);
                const known = knownRate(meter, racer.source, runtime);
                return needed !== null && known !== null && needed > chosenNeeds && keepsUp(known.rate, needed) === false ?
                    [{ source: racer.source, rate: known.rate, needed, remembered: known.remembered }]
                    :
                    [];
            });
        };

        // The highest-bitrate racer with data that keeps up wins.
        const decide = () => {
            const choice = chooseKeepingUp(racers.filter((racer) => racer.read > 0).map((racer) => racer.source), meter, runtime);
            if (choice !== null && !largerMayKeepUp(choice.chosen)) {
                finish(choice.chosen, passedOver(choice.chosen));
            }
        };

        // Alone with data: it wins if it keeps up and no larger racer may still.
        const aloneMayWin = (racer: Racer) => {
            if (neededRate(racer.source, runtime) === null) {
                return true;
            }
            return keepsUpNow(racer) === true && !largerMayKeepUp(racer.source);
        };

        const start = (source: RankedSource) => {
            const racer: Racer = { source, read: 0, firstByteAt: null, hasMetadata: false, peers: 0, speed: 0, controller: new AbortController() };
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
                    const now = Date.now();
                    racer.firstByteAt = racer.firstByteAt ?? now;
                    // Data on disk reads at once and downloads nothing: its read rate
                    // counts (8 MiB can arrive within 60 ms, hence the floor).
                    if (racer.read >= ALONE_WIN_BYTES) {
                        meter.addRead(racer.source.infoHash, racer.read / (Math.max(now - racer.firstByteAt, READ_RATE_FLOOR) / 1000));
                    }
                    if (racer.read >= ALONE_WIN_BYTES && racers.every((other) => other === racer || other.read === 0) && aloneMayWin(racer)) {
                        finish(racer.source, passedOver(racer.source));
                    }
                })
                    .then(() => {
                        if (racer.read >= RACE_BYTES) {
                            // With a bitrate, the rate decides (decide, or the settle time):
                            // the server keeps downloading the file meanwhile.
                            if (neededRate(racer.source, runtime) === null) {
                                finish(racer.source, []);
                            }
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
            const withData = racers.filter((racer) => racer.read > 0);
            if (withData.length > 0 && now - startedAt >= SETTLE_AFTER) {
                // Nobody kept up in time: the best rate for its bitrate, else the most bytes.
                const score = (racer: Racer) => {
                    const needed = neededRate(racer.source, runtime);
                    const rate = meter.rate(racer.source.infoHash);
                    return needed !== null && rate !== null ? rate / needed : racer.read;
                };
                finish(withData.reduce((best, racer) => score(racer) > score(best) ? racer : best).source, []);
                return;
            }
            if (withData.length === 0 && now - lastWiden >= WIDEN_AFTER && racers.length < queue.length) {
                lastWiden = now;
                queue.slice(racers.length, racers.length + WIDEN_BY).forEach(start);
            }
            Promise.all(racers.map((racer) => stats(baseUrl, racer.source.infoHash).then((s) => {
                racer.hasMetadata = s !== null && s.files.length > 0;
                racer.peers = s?.peers ?? 0;
                racer.speed = s?.downloadSpeed ?? 0;
                if (s !== null) {
                    meter.add(racer.source.infoHash, Date.now(), s.downloaded);
                }
            }))).then(() => {
                if (!settled) {
                    if (runtime !== null) {
                        decide();
                    }
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

export type { RacerProgress, RacerPhase, RaceResult };
export default race;
