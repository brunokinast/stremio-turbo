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

// Same URL stremio-core builds for torrent streams: /{infoHash}/{fileIdx}?tr=…
const streamUrl = (baseUrl: string, infoHash: string, fileIdx: number | null, trackers: string[]): URL => {
    const url = new URL(`${infoHash}/${fileIdx ?? -1}`, baseUrl);
    trackers.forEach((tracker) => url.searchParams.append('tr', tracker));
    return url;
};

// Reads up to `bytes` from `offset`, calling onProgress with the bytes read so
// far. Resolves with the total once the range is read or the body ends.
const readRange = async (url: URL, offset: number, bytes: number, signal: AbortSignal, onProgress: (read: number) => void): Promise<number> => {
    const response = await fetch(url, {
        headers: { Range: `bytes=${offset}-${offset + bytes - 1}` },
        signal,
    });
    if (!response.ok || response.body === null) {
        throw new Error(`read failed: ${response.status}`);
    }
    const reader = response.body.getReader();
    let read = 0;
    try {
        while (read < bytes) {
            const { done, value } = await reader.read();
            if (done) {
                break;
            }
            read += value.byteLength;
            onProgress(read);
        }
    } finally {
        reader.cancel().catch(() => undefined);
    }
    return read;
};

const remove = (baseUrl: string, infoHash: string): Promise<void> => {
    return fetch(new URL(`${infoHash}/remove`, baseUrl)).then(() => undefined, () => undefined);
};

export type { ScrapeResult, ScrapeTorrent };
export { isKnown, scrapeStream, streamUrl, readRange, remove };
