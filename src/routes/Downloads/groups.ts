// Turns the server's records into what the Downloads screen lists: one group
// per show (by the episode the client named, else by the file name), episodes
// in season and episode order; files that match no show in an Unknown group,
// last.

import parseRelease, { resolutionRank } from 'stremio/smartPlay/parseRelease';
import type { Quality } from 'stremio/smartPlay/parseRelease';
import { isComplete, isLive, isStopped } from 'stremio/smartPlay/downloads';
import type { Download } from 'stremio/smartPlay/downloads';

type Filter = 'all' | 'downloading' | 'stopped';
type Sort = 'size' | 'played' | 'added';

type Item = {
    key: string,
    record: Download,
    // "S02E05" (null for a movie or an unparsed file).
    episodeLabel: string | null,
    title: string | null,
    fileName: string,
    onDisk: number,
    // The file's size; null while its torrent's file list isn't known.
    total: number | null,
    // 0-1, null when the size is unknown.
    progress: number | null,
    lastPlayed: number | null,
    added: number,
    season: number,
    episode: number,
    quality: Quality,
    // Bits per second (size ÷ runtime), null when either is unknown.
    bitrate: number | null,
    // Downloading, queued, or playing.
    live: boolean,
    // All of it on disk.
    complete: boolean,
    // Part of it on disk, nothing fetching the rest (resumable).
    stopped: boolean,
};

type Group = {
    key: string,
    name: string,
    poster: string | null,
    metaId: string | null,
    // Files that match no show.
    unknown: boolean,
    items: Item[],
    onDisk: number,
    lastPlayed: number | null,
    added: number,
    downloading: boolean,
};

const UNKNOWN_KEY = '\u0000unknown';

// An unkept file is identified by its path (several can share fileIdx -1).
const recordKey = (record: Pick<Download, 'infoHash' | 'fileIdx'> & { path?: string }) => record.path !== undefined ? `${record.infoHash}/${record.path}` : `${record.infoHash}/${record.fileIdx}`;

const baseName = (path: string) => (path.split('/').pop() ?? path).replace(/\.part$/, '');

const pad = (n: number) => String(n).padStart(2, '0');

const timeOf = (iso: string | undefined): number | null => {
    const t = iso !== undefined ? Date.parse(iso) : NaN;
    return Number.isNaN(t) ? null : t;
};

// metaId -> the show's episode runtime in seconds.
type Runtimes = Map<string, number>;

const toItem = (record: Download, runtimes: Runtimes): Item & { show: string | null, metaId: string | null, poster: string | null } => {
    const meta = record.meta ?? {};
    const path = record.path || record.name || record.infoHash;
    const parsed = parseRelease(path);
    const season = meta.season ?? parsed.season;
    const episode = meta.episode ?? parsed.episode;
    const total = record.info === undefined && record.size > 0 ? record.size : null;
    const runtime = meta.runtime ?? (meta.metaId !== undefined ? runtimes.get(meta.metaId) : undefined);
    return {
        key: recordKey(record),
        record,
        episodeLabel: season !== null && season !== undefined && episode !== null && episode !== undefined ? `S${pad(season)}E${pad(episode)}` : null,
        title: meta.title ?? null,
        fileName: baseName(path),
        onDisk: record.bytesOnDisk,
        total,
        progress: total !== null ? Math.min(1, record.bytesCompleted / total) : null,
        lastPlayed: timeOf(record.lastPlayed),
        added: timeOf(record.addedAt) ?? 0,
        season: season ?? 0,
        episode: episode ?? 0,
        quality: parsed.quality,
        bitrate: total !== null && runtime !== undefined && runtime > 0 ? (total * 8) / runtime : null,
        live: isLive(record),
        complete: isComplete(record),
        stopped: isStopped(record),
        show: meta.name ?? parsed.show,
        metaId: meta.metaId ?? null,
        poster: meta.poster ?? null,
    };
};

// Whether a is the better copy of an episode to keep than b: all on disk
// (a complete copy beats a few MB of a better release), then higher
// resolution, then higher bitrate (or size, at equal runtime).
const isBetter = (a: Item, b: Item) => {
    if (a.complete !== b.complete) {
        return a.complete;
    }
    const rank = resolutionRank(a.quality.resolution) - resolutionRank(b.quality.resolution);
    if (rank !== 0) {
        return rank > 0;
    }
    return (a.bitrate ?? a.total ?? 0) > (b.bitrate ?? b.total ?? 0);
};

// A name with capitals reads better than one taken from a lower-case file name.
const betterName = (a: string, b: string) => /[A-Z]/.test(b) && !/[A-Z]/.test(a) ? b : a;

const matchesFilter = (item: Item, filter: Filter) => {
    switch (filter) {
        case 'downloading':
            return item.live;
        case 'stopped':
            return item.stopped;
        default:
            return true;
    }
};

const groupDownloads = (records: Download[], filter: Filter, sort: Sort, runtimes: Runtimes = new Map()): Group[] => {
    const groups = new Map<string, Group>();
    // A temporary file (a measurement) is listed only while it moves.
    records.filter((record) => record.kind !== 'temporary' || isLive(record)).forEach((record) => {
        const item = toItem(record, runtimes);
        if (!matchesFilter(item, filter)) {
            return;
        }
        // Unknown: no show named by the client, and a name that doesn't read as
        // a release (no episode, no resolution): any name parses to some "show".
        const unknown = item.metaId === null && (item.show === null || (item.episodeLabel === null && item.quality.resolution === null));
        const key = unknown ? UNKNOWN_KEY : (item.metaId ?? item.show!.toLowerCase());
        let group = groups.get(key);
        if (group === undefined) {
            group = { key, name: item.show ?? '', poster: null, metaId: null, unknown, items: [], onDisk: 0, lastPlayed: null, added: 0, downloading: false };
            groups.set(key, group);
        }
        if (item.show !== null) {
            group.name = betterName(group.name, item.show);
        }
        group.poster = group.poster ?? item.poster;
        group.metaId = group.metaId ?? item.metaId;
        group.items.push(item);
        group.onDisk += item.onDisk;
        group.lastPlayed = Math.max(group.lastPlayed ?? 0, item.lastPlayed ?? 0) || null;
        group.added = Math.max(group.added, item.added);
        group.downloading = group.downloading || item.live;
    });
    // Records named by the client and parsed ones of the same show merge by name.
    const byName = new Map<string, Group>();
    groups.forEach((group) => {
        const nameKey = group.unknown || group.name === '' ? group.key : group.name.toLowerCase();
        const same = byName.get(nameKey);
        if (same === undefined) {
            byName.set(nameKey, group);
            return;
        }
        same.items.push(...group.items);
        same.onDisk += group.onDisk;
        same.poster = same.poster ?? group.poster;
        same.metaId = same.metaId ?? group.metaId;
        same.lastPlayed = Math.max(same.lastPlayed ?? 0, group.lastPlayed ?? 0) || null;
        same.added = Math.max(same.added, group.added);
        same.downloading = same.downloading || group.downloading;
    });
    const sortValue = (x: { onDisk: number, lastPlayed: number | null, added: number }) => {
        switch (sort) {
            case 'played':
                return x.lastPlayed ?? 0;
            case 'added':
                return x.added;
            default:
                return x.onDisk;
        }
    };
    const list = Array.from(byName.values());
    list.forEach((group) => {
        group.items.sort((a, b) => a.season - b.season || a.episode - b.episode || a.fileName.localeCompare(b.fileName));
    });
    return list.sort((a, b) => Number(a.unknown) - Number(b.unknown) || sortValue(b) - sortValue(a));
};

export type { Filter, Sort, Group, Item, Runtimes };
export { groupDownloads, isBetter, recordKey };
