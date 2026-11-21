// Copyright (C) 2017-2026 Smart code 203358507

import { useEffect, useMemo, useRef, useState } from 'react';
import { isKnown, scrapeStream } from './serverApi';
import type { ScrapeResult, ScrapeTorrent } from './serverApi';
import { rankTiers } from './groupByQuality';
import type { RankedTier, Tier } from './groupByQuality';

// Sources answer inconsistently (tracker timeouts, partial DHT walks), so a
// lower figure is usually a worse answer rather than a smaller swarm: keep the
// best seen for each torrent.
const mergeBest = (previous: Map<string, ScrapeResult>, result: ScrapeResult) => {
    const known = previous.get(result.infoHash);
    if (known !== undefined && isKnown(known) && (!isKnown(result) || result.seeders < known.seeders)) {
        return previous;
    }
    return new Map(previous).set(result.infoHash, result);
};

// Every source's torrent, ordered so the server asks about the likeliest picks
// first: each tier's sources by the addon's figure, taken round-robin across
// tiers so every quality's top sources come early.
const torrentsOf = (tiers: Tier[]): ScrapeTorrent[] => {
    const ordered = tiers.map(({ sources }) => [...sources].sort((a, b) => (b.listedSeeders ?? -1) - (a.listedSeeders ?? -1)));
    const byHash = new Map<string, string[]>();
    const rounds = Math.max(0, ...ordered.map((sources) => sources.length));
    for (let round = 0; round < rounds; round++) {
        ordered.forEach((sources) => {
            const source = sources[round];
            if (source !== undefined) {
                byHash.set(source.infoHash, Array.from(new Set([...(byHash.get(source.infoHash) ?? []), ...source.trackers])));
            }
        });
    }
    return Array.from(byHash, ([infoHash, trackers]) => ({ infoHash, trackers }));
};

// Live seeder counts for every source, streamed in as the server learns them,
// and the tiers ranked by them. Each torrent is asked about once per mount;
// sources that appear later (addons still loading) get their own request.
// `settled` turns true once every request has finished.
const useLiveSeeders = (tiers: Tier[], baseUrl: string | null) => {
    const [live, setLive] = useState<Map<string, ScrapeResult>>(() => new Map());
    const [pending, setPending] = useState(0);
    const requested = useRef(new Set<string>());
    const controllers = useRef(new Set<AbortController>());

    const torrents = useMemo(() => torrentsOf(tiers), [tiers]);
    const torrentsKey = torrents.map(({ infoHash }) => infoHash).join(',');

    useEffect(() => () => {
        controllers.current.forEach((controller) => controller.abort());
        controllers.current.clear();
        requested.current.clear();
        setPending(0);
    }, [baseUrl]);

    useEffect(() => {
        if (baseUrl === null) {
            return;
        }
        const fresh = torrents.filter(({ infoHash }) => !requested.current.has(infoHash));
        if (fresh.length === 0) {
            return;
        }
        fresh.forEach(({ infoHash }) => requested.current.add(infoHash));
        const controller = new AbortController();
        controllers.current.add(controller);
        setPending((count) => count + 1);
        scrapeStream(baseUrl, fresh, controller.signal, (result) => setLive((previous) => mergeBest(previous, result)))
            .catch(() => undefined)
            .finally(() => {
                controllers.current.delete(controller);
                if (!controller.signal.aborted) {
                    setPending((count) => count - 1);
                }
            });
        // torrentsKey captures the torrent set; torrents itself changes identity on every streams update.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [baseUrl, torrentsKey]);

    const ranked: RankedTier[] = useMemo(() => rankTiers(tiers, live), [tiers, live]);
    return { tiers: ranked, settled: pending === 0, live };
};

export default useLiveSeeders;
