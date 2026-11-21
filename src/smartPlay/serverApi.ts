// Copyright (C) 2017-2026 Smart code 203358507

// Client for the streaming server (our Rivulet fork) routes smart play uses.

// A torrent's best figures so far, merged from trackers, DHT scrapes and (for
// torrents the server has open) connected peers.
type ScrapeResult = {
    infoHash: string,
    seeders: number,
    leechers: number,
    completed: number,
    // Trackers that answered.
    trackers: number,
    // DHT nodes returned scrape filters.
    dht?: boolean,
    // Includes an open torrent's connected peers.
    live?: boolean,
};

type ScrapeTorrent = {
    infoHash: string,
    trackers: string[],
};

// With no source answering, the counts are unknown rather than zero.
const isKnown = (result: ScrapeResult) => result.trackers > 0 || result.dht === true || result.live === true;

// Streams swarm figures as the server learns them: one call per improvement,
// fast sources (open torrents, trackers) first, DHT estimates over the next
// seconds. Resolves once every source has answered or given up.
const scrapeStream = async (baseUrl: string, torrents: ScrapeTorrent[], signal: AbortSignal, onUpdate: (result: ScrapeResult) => void): Promise<void> => {
    const response = await fetch(new URL('scrape', baseUrl), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/x-ndjson' },
        body: JSON.stringify({ torrents }),
        signal,
    });
    if (!response.ok || response.body === null) {
        throw new Error(`scrape failed: ${response.status}`);
    }
    const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
    let buffer = '';
    for (;;) {
        const { done, value } = await reader.read();
        if (done) {
            return;
        }
        buffer += value;
        let newline = buffer.indexOf('\n');
        while (newline >= 0) {
            const line = buffer.slice(0, newline).trim();
            buffer = buffer.slice(newline + 1);
            newline = buffer.indexOf('\n');
            if (line === '') {
                continue;
            }
            const message = JSON.parse(line) as ScrapeResult | { done: true };
            if ('done' in message) {
                return;
            }
            onUpdate(message);
        }
    }
};

type TorrentStats = {
    peers: number,
    // Bytes per second.
    downloadSpeed: number,
    // Bytes downloaded so far.
    downloaded: number,
    // Empty until the torrent's metadata is known.
    files: unknown[],
};

// A torrent's live stats, or null when the server doesn't have it open.
const stats = (baseUrl: string, infoHash: string): Promise<TorrentStats | null> => {
    return fetch(new URL(`${infoHash}/stats.json`, baseUrl))
        .then((response) => response.ok ? response.json() as Promise<TorrentStats | null> : null)
        .then((result) => result !== null ? { ...result, files: result.files ?? [] } : null)
        .catch(() => null);
};

type Incoming = {
    // Peers that connected in since the server started.
    count: number,
    listenPort: number,
    // When a torrent became active (incoming is only expected then), or null.
    activeSince: string | null,
};

// The server's incoming connections (is its listening port reachable?), or
// null when it doesn't answer.
const incoming = (baseUrl: string): Promise<Incoming | null> => {
    return fetch(new URL('network-info', baseUrl))
        .then((response) => response.ok ? response.json() as Promise<{ incoming?: Incoming }> : null)
        .then((body) => body?.incoming ?? null)
        .catch(() => null);
};

export type { Incoming, ScrapeResult, ScrapeTorrent, TorrentStats };
export { incoming, isKnown, scrapeStream, stats };
