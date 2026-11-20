// Client for the streaming server's record of every file on disk (our Rivulet
// fork, /downloads): kept files (downloaded whole, deleted only on request; a
// played file becomes kept), temporary ones (measurements, leased), and
// unkept files (data on disk with no record, listed by path: an episode that
// was streamed and never kept, a folder from an earlier run). One shared
// poller feeds every view of it (the Downloads screen, the episode chips, the
// quality list).

import { useCallback, useEffect, useState } from 'react';
import { useCore } from 'stremio/core';

type DownloadState = 'queued' | 'downloading' | 'paused' | 'complete' | 'idle';
type DownloadKind = 'kept' | 'temporary' | 'unkept';

// The episode a file is for, as the client named it.
type DownloadMeta = {
    metaId?: string,
    videoId?: string,
    type?: string,
    name?: string,
    poster?: string,
    season?: number,
    episode?: number,
    title?: string,
    // Seconds.
    runtime?: number,
};

type Download = {
    infoHash: string,
    // -1 for an unkept file whose torrent's file list isn't known.
    fileIdx: number,
    kind: DownloadKind,
    origins?: string[],
    meta?: DownloadMeta,
    name?: string,
    size: number,
    bytesCompleted: number,
    bytesOnDisk: number,
    // Bytes per second.
    rate: number,
    state: DownloadState,
    watched: boolean,
    addedAt: string,
    lastPlayed?: string,
    // An unkept file's path in its torrent's folder.
    path?: string,
    // Set on an unkept file whose torrent's file list isn't known (size 0):
    // being fetched, or nobody answered.
    info?: 'pending' | 'missing',
    // A player has the file open.
    streaming?: boolean,
    peers?: number,
    // Its rank among its torrent's kept files, and whether it downloads
    // first (High priority) of them.
    order?: number,
    first?: boolean,
};

type DiskInfo = { free: number, total: number, used: number };

type DownloadRequest = {
    kind?: 'kept' | 'temporary',
    origin?: string,
    meta?: DownloadMeta,
    startFraction?: number,
    // Ranks a kept file among its torrent's kept files (earliest episode
    // first): season * 1000 + episode.
    order?: number,
};

// The server's records, or null when it doesn't support them.
const listDownloads = (baseUrl: string): Promise<Download[] | null> => {
    return fetch(new URL('downloads', baseUrl))
        .then((response) => response.ok ? response.json() as Promise<Download[]> : null)
        .catch(() => null);
};

const getDisk = (baseUrl: string): Promise<DiskInfo | null> => {
    return fetch(new URL('downloads/disk', baseUrl))
        .then((response) => response.ok ? response.json() as Promise<DiskInfo> : null)
        .catch(() => null);
};

// Records a file (kept by default: downloaded whole). Rejects with the
// server's reason.
const addDownload = async (baseUrl: string, infoHash: string, fileIdx: number, sources: string[], request: DownloadRequest = {}): Promise<Download> => {
    const response = await fetch(new URL('downloads', baseUrl), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ infoHash, fileIdx, sources, ...request }),
    });
    const body = await response.json().catch(() => null);
    if (!response.ok) {
        throw new Error(typeof body?.error === 'string' ? body.error : `download failed: ${response.status}`);
    }
    return body as Download;
};

const patchDownload = (baseUrl: string, infoHash: string, fileIdx: number, patch: { watched?: boolean, paused?: boolean, meta?: DownloadMeta }): Promise<void> => {
    return fetch(new URL(`downloads/${infoHash}/${fileIdx}`, baseUrl), {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(patch),
    }).then(() => undefined, () => undefined);
};

// Marks a file watched. A file with no record is ignored.
const markWatched = (baseUrl: string, infoHash: string, fileIdx: number): void => {
    patchDownload(baseUrl, infoHash, fileIdx, { watched: true });
};

// Names the episode a recorded file is for (a played file is recorded by the
// server before the client says which episode it is).
const setDownloadMeta = (baseUrl: string, infoHash: string, fileIdx: number, meta: DownloadMeta): Promise<void> => {
    return patchDownload(baseUrl, infoHash, fileIdx, { meta });
};

// Pauses or resumes a kept file's download.
const setPaused = (baseUrl: string, infoHash: string, fileIdx: number, paused: boolean): Promise<void> => {
    return patchDownload(baseUrl, infoHash, fileIdx, { paused });
};

type Removable = Pick<Download, 'infoHash' | 'fileIdx'> & { path?: string };

// Deletes a record and its data: one file, an unkept file (by its path), or
// a whole torrent's folder (fileIdx -1).
const removeDownload = (baseUrl: string, infoHash: string, fileIdx: number, path?: string): Promise<void> => {
    const url = new URL(fileIdx < 0 || path !== undefined ? `downloads/${infoHash}` : `downloads/${infoHash}/${fileIdx}`, baseUrl);
    if (path !== undefined) {
        url.searchParams.set('path', path);
    }
    return fetch(url, { method: 'DELETE' }).then(() => undefined, () => undefined);
};

// ---- the shared poller ------------------------------------------------------

type Snapshot = { downloads: Download[] | null, disk: DiskInfo | null };

const store = {
    snapshot: { downloads: null, disk: null } as Snapshot,
    listeners: new Map<() => void, number>(),
    baseUrl: null as string | null,
    timer: null as ReturnType<typeof setTimeout> | null,
};

const notify = () => store.listeners.forEach((_interval, listener) => listener());

// A kept file that hasn't moved for this long is stalled (the Downloads
// screen offers another source).
const STALL_AFTER = 10 * 60 * 1000;
// Per kept, incomplete, unpaused file: its bytes and when they last grew.
const movement = new Map<string, { bytes: number, at: number }>();

const trackMovement = (downloads: Download[] | null) => {
    const now = Date.now();
    const seen = new Set<string>();
    (downloads ?? []).forEach((d) => {
        if (d.kind !== 'kept' || d.state === 'complete' || d.state === 'paused') {
            return;
        }
        const key = `${d.infoHash}:${d.fileIdx}`;
        seen.add(key);
        const last = movement.get(key);
        if (last === undefined || d.bytesCompleted > last.bytes) {
            movement.set(key, { bytes: d.bytesCompleted, at: now });
        }
    });
    Array.from(movement.keys()).filter((key) => !seen.has(key)).forEach((key) => movement.delete(key));
};

// A kept file still to download that hasn't gained a byte in STALL_AFTER
// (as far as this session has watched it).
const isStalled = (d: Download) => {
    const last = d.kind === 'kept' && d.state !== 'complete' && d.state !== 'paused' ? movement.get(`${d.infoHash}:${d.fileIdx}`) : undefined;
    return last !== undefined && Date.now() - last.at >= STALL_AFTER;
};

const refreshStore = async () => {
    const baseUrl = store.baseUrl;
    if (baseUrl === null) {
        return;
    }
    const [downloads, disk] = await Promise.all([listDownloads(baseUrl), getDisk(baseUrl)]);
    if (baseUrl === store.baseUrl) {
        trackMovement(downloads);
        store.snapshot = { downloads, disk };
        notify();
    }
};

// Polls at the fastest interval any subscriber asked for, while any is mounted.
const schedule = () => {
    if (store.timer !== null) {
        clearTimeout(store.timer);
        store.timer = null;
    }
    if (store.listeners.size === 0 || store.baseUrl === null) {
        return;
    }
    const interval = Math.min(...store.listeners.values());
    store.timer = setTimeout(() => {
        refreshStore().finally(schedule);
    }, interval);
};

// The streaming server's local URL, read without suspending: useStreamingServer
// suspends on its first read, and in the nav bar (which every route renders,
// above the routes' modal container) that re-suspended the tree on every
// mount, in a loop.
const useServerUrl = (): string | null => {
    const core = useCore();
    const [url, setUrl] = useState<string | null>(store.baseUrl);
    useEffect(() => {
        let active = true;
        const read = () => {
            core.transport.getState('streaming_server').then((state) => {
                const selected = (state as { selected?: { transportUrl?: string } | null }).selected;
                if (active) {
                    setUrl(typeof selected?.transportUrl === 'string' ? selected.transportUrl : null);
                }
            }).catch(() => undefined);
        };
        const onState = (models: string[]) => {
            if (models.includes('streaming_server')) {
                read();
            }
        };
        read();
        core.on('state', onState);
        return () => {
            active = false;
            core.off('state', onState);
        };
    }, [core]);
    return url;
};

// The server's records and disk space, refreshed every intervalMs while
// mounted (and at once after remove()).
const useDownloads = (intervalMs: number, enabled = true) => {
    const baseUrl = useServerUrl();
    const [snapshot, setSnapshot] = useState<Snapshot>(store.snapshot);

    useEffect(() => {
        if (!enabled || baseUrl === null) {
            return;
        }
        const listener = () => setSnapshot(store.snapshot);
        store.listeners.set(listener, intervalMs);
        if (store.baseUrl !== baseUrl) {
            store.baseUrl = baseUrl;
            store.snapshot = { downloads: null, disk: null };
        }
        setSnapshot(store.snapshot);
        refreshStore().finally(schedule);
        return () => {
            store.listeners.delete(listener);
            schedule();
        };
    }, [baseUrl, intervalMs, enabled]);

    const remove = useCallback(async (records: Removable[]) => {
        if (baseUrl === null) {
            return;
        }
        await Promise.all(records.map(({ infoHash, fileIdx, path }) => removeDownload(baseUrl, infoHash, fileIdx, path)));
        await refreshStore();
    }, [baseUrl]);

    const pause = useCallback(async (record: Pick<Download, 'infoHash' | 'fileIdx'>, paused: boolean) => {
        if (baseUrl === null) {
            return;
        }
        await setPaused(baseUrl, record.infoHash, record.fileIdx, paused);
        await refreshStore();
    }, [baseUrl]);

    // Resumes a stopped file: a paused one is unpaused; an unkept one is
    // recorded as kept, so it finishes in the background.
    const resume = useCallback(async (record: Download) => {
        if (baseUrl === null) {
            return;
        }
        if (record.kind === 'unkept') {
            await addDownload(baseUrl, record.infoHash, record.fileIdx, [`dht:${record.infoHash}`], { kind: 'kept', origin: 'resume', meta: record.meta }).catch(() => undefined);
        } else {
            await setPaused(baseUrl, record.infoHash, record.fileIdx, false);
        }
        await refreshStore();
    }, [baseUrl]);

    return { downloads: snapshot.downloads, disk: snapshot.disk, remove, pause, resume, baseUrl };
};

// A file moving right now: downloading, queued to, or playing.
const isLive = (d: Download) => d.state === 'downloading' || d.state === 'queued' || d.streaming === true;

// All of the file is on disk (an unkept file can be whole too).
const isComplete = (d: Download) => d.state === 'complete' || (d.kind === 'unkept' && d.info === undefined && d.size > 0 && d.bytesCompleted >= d.size);

// Part of the file is on disk and nothing is fetching the rest: paused, or
// unkept. Resumable.
const isStopped = (d: Download) => !isComplete(d) && (d.state === 'paused' || (d.kind === 'unkept' && d.info === undefined && d.fileIdx >= 0));

// Origins smart play gives a record by itself. A record with any other
// origin (the user's own download, "manual" or "resume") is never replaced or
// deleted by smart play.
const AUTOMATIC_ORIGINS = ['picker', 'play', 'next'];
const isAutomatic = (d: Pick<Download, 'origins'>) => (d.origins ?? []).every((origin) => AUTOMATIC_ORIGINS.includes(origin));

// How many files are downloading now (the nav tab's badge).
const useActiveDownloads = (): number => {
    const { downloads } = useDownloads(10000);
    return (downloads ?? []).filter((d) => d.state === 'downloading' || d.state === 'queued').length;
};

export type { Download, DownloadKind, DownloadMeta, DownloadRequest, DownloadState, DiskInfo };
export { refreshStore as refreshDownloads, listDownloads, getDisk, addDownload, markWatched, setDownloadMeta, setPaused, removeDownload, isLive, isComplete, isStopped, isAutomatic, isStalled, useDownloads, useActiveDownloads };
