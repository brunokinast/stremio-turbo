// Client for the streaming server's background downloads (our Rivulet fork,
// GET/POST/PATCH /downloads): whole files fetched in the background and kept
// on disk, so an episode downloaded ahead plays without buffering.

type DownloadState = 'queued' | 'downloading' | 'paused' | 'complete';

type Download = {
    infoHash: string,
    fileIdx: number,
    name?: string,
    size: number,
    bytesCompleted: number,
    // Bytes per second.
    rate: number,
    state: DownloadState,
    watched: boolean,
    addedAt: string,
};

// The server's downloads, or null when it doesn't support them.
const listDownloads = (baseUrl: string): Promise<Download[] | null> => {
    return fetch(new URL('downloads', baseUrl))
        .then((response) => response.ok ? response.json() as Promise<Download[]> : null)
        .catch(() => null);
};

// Starts a background download. Rejects with the server's reason (e.g. the
// download budget is full).
const addDownload = async (baseUrl: string, infoHash: string, fileIdx: number, sources: string[]): Promise<Download> => {
    const response = await fetch(new URL('downloads', baseUrl), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ infoHash, fileIdx, sources }),
    });
    const body = await response.json().catch(() => null);
    if (!response.ok) {
        throw new Error(typeof body?.error === 'string' ? body.error : `download failed: ${response.status}`);
    }
    return body as Download;
};

// Marks a download watched (deleted first when space is needed). A file that
// isn't a download is ignored.
const markWatched = (baseUrl: string, infoHash: string, fileIdx: number): void => {
    fetch(new URL(`downloads/${infoHash}/${fileIdx}`, baseUrl), {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ watched: true }),
    }).catch(() => undefined);
};

const removeDownload = (baseUrl: string, infoHash: string, fileIdx: number): Promise<void> => {
    return fetch(new URL(`downloads/${infoHash}/${fileIdx}`, baseUrl), { method: 'DELETE' }).then(() => undefined, () => undefined);
};

export type { Download, DownloadState };
export { listDownloads, addDownload, markWatched, removeDownload };
