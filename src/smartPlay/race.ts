// Races the top sources of a quality: each reads the same stretch of its file
// in parallel and the one that delivers fastest wins; the others are removed
// from the server. The pieces read stay in the server's cache, so the player
// starts from them instantly.

import { readRange, remove, streamUrl } from './serverApi';
import type { RankedSource } from './groupByQuality';

const RACERS = 3;
const RACE_BYTES = 8 * 1024 * 1024;
const RACE_TIMEOUT = 8000;

type RaceOptions = {
    baseUrl: string,
    sources: RankedSource[],
    // Fraction of the file the player will start at (resume position).
    startFraction: number,
    signal: AbortSignal,
};

const race = ({ baseUrl, sources, startFraction, signal }: RaceOptions): Promise<RankedSource> => {
    const racers = sources.slice(0, RACERS);
    if (racers.length <= 1) {
        return Promise.resolve(racers[0]);
    }

    return new Promise((resolve, reject) => {
        const read = racers.map(() => 0);
        const controllers = racers.map(() => new AbortController());
        let settled = false;

        const finish = (winnerIdx: number) => {
            if (settled) {
                return;
            }
            settled = true;
            clearTimeout(timeout);
            controllers.forEach((controller) => controller.abort());
            racers.forEach((racer, idx) => {
                if (idx !== winnerIdx) {
                    remove(baseUrl, racer.infoHash);
                }
            });
            resolve(racers[winnerIdx]);
        };

        // Out of time: most bytes wins; with no data at all, the best-ranked.
        const timeout = setTimeout(() => {
            const most = Math.max(...read);
            finish(most > 0 ? read.indexOf(most) : 0);
        }, RACE_TIMEOUT);

        signal.addEventListener('abort', () => {
            if (!settled) {
                settled = true;
                clearTimeout(timeout);
                controllers.forEach((controller) => controller.abort());
                reject(new DOMException('Race cancelled', 'AbortError'));
            }
        }, { once: true });

        racers.forEach((racer, idx) => {
            const offset = racer.size !== null ? Math.max(0, Math.min(Math.floor(racer.size * startFraction), racer.size - RACE_BYTES)) : 0;
            const url = streamUrl(baseUrl, racer.infoHash, racer.fileIdx, racer.trackers);
            readRange(url, offset, RACE_BYTES, controllers[idx].signal, (bytes) => {
                read[idx] = bytes;
            })
                .then((bytes) => {
                    if (bytes >= RACE_BYTES) {
                        finish(idx);
                    }
                })
                .catch(() => undefined);
        });
    });
};

export default race;

