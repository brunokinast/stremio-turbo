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

// The URL stremio-core builds for torrent streams (/{infoHash}/{fileIdx}?tr=…),
// marked as a probe: the server serves it like the player's stream but leaves
// the file prepared for the player (head and tail pieces kept, nothing
// demoted when it closes).
const probeUrl = (baseUrl: string, infoHash: string, fileIdx: number | null, trackers: string[]): URL => {
    const url = new URL(`${infoHash}/${fileIdx ?? -1}`, baseUrl);
    trackers.forEach((tracker) => url.searchParams.append('tr', tracker));
    url.searchParams.set('probe', '1');
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

// Opens a torrent on the server without reading it: it finds peers and
// fetches metadata (plus the file's first and last pieces) in the background,
// so playing it later starts at once. Unplayed torrents close on their own
// after the server's idle timeout.
// Starts a torrent ahead of playback. fileIdx names the episode in a season
// pack, so the server primes that file's first and last pieces instead of
// guessing the largest file.
// startFraction is where playback will start (a resume point, 0 from the
// start): the server primes the pieces there too, where the race and the
// player will read first.
const preload = (baseUrl: string, infoHash: string, fileIdx: number | null, trackers: string[], startFraction: number): void => {
    fetch(new URL(`${infoHash}/create`, baseUrl), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
            torrent: { infoHash },
            ...(fileIdx !== null ? { fileIdx } : {}),
            ...(startFraction > 0 && startFraction < 1 ? { startFraction } : {}),
            // The addon's sources as given ("tracker:<url>", "dht:<hash>"): the
            // server's peer-search syntax already.
            peerSearch: { sources: trackers },
        }),
    }).catch(() => undefined);
};

const remove = (baseUrl: string, infoHash: string): Promise<void> => {
    return fetch(new URL(`${infoHash}/remove`, baseUrl)).then(() => undefined, () => undefined);
};

export type { ScrapeResult, ScrapeTorrent, TorrentStats };
export { isKnown, scrapeStream, probeUrl, readRange, stats, preload, remove };
