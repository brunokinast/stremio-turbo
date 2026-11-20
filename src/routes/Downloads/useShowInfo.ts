// What the screen needs to know about each show, from the user's Cinemeta
// add-on, looked up once per show:
// - records saved before the client said which show they are (found by file
//   name only) get the show's id, name and poster written onto them, so the
//   screen shows posters and the search never repeats;
// - the show's episode runtime, for each file's bitrate (size ÷ runtime). It's
//   written onto kept records too.

import { useEffect, useState } from 'react';
import { useProfile } from 'stremio/common';
import { setDownloadMeta } from 'stremio/smartPlay/downloads';
import type { DownloadMeta } from 'stremio/smartPlay/downloads';
import type { Group, Item, Runtimes } from './groups';

const CINEMETA = 'com.linvo.cinemeta';
// Show names searched and metaIds looked up this session (found or not).
const searched = new Set<string>();
const looked = new Set<string>();
const runtimesCache: Runtimes = new Map();

type Meta = { id: string, type: string, name: string, poster?: string, runtime?: string };

const getJson = async <T, >(url: string): Promise<T | null> => {
    try {
        const response = await fetch(url);
        return response.ok ? await response.json() as T : null;
    } catch (_e) {
        return null;
    }
};

const search = async (base: string, type: string, name: string): Promise<Meta | null> => {
    const body = await getJson<{ metas?: Meta[] }>(`${base}/catalog/${type}/top/search=${encodeURIComponent(name)}.json`);
    const metas = body?.metas ?? [];
    return metas.find((meta) => meta.name.toLowerCase() === name.toLowerCase()) ?? metas[0] ?? null;
};

// "60 min", "1h 2min", "1h" -> seconds.
const parseRuntime = (text: string | undefined): number | null => {
    if (typeof text !== 'string') {
        return null;
    }
    const hours = /(\d+)\s*h/i.exec(text);
    const minutes = /(\d+)\s*m/i.exec(text);
    const total = (hours !== null ? Number(hours[1]) * 60 : 0) + (minutes !== null ? Number(minutes[1]) : 0);
    return total > 0 ? total * 60 : null;
};

const typeOf = (group: Group) => group.items.some((item) => item.episodeLabel !== null) ? 'series' : 'movie';

// Writes details onto a group's kept records (the server stores them).
const writeMeta = (baseUrl: string, items: Item[], details: (item: Item) => DownloadMeta) => {
    items.filter((item) => item.record.kind === 'kept').forEach((item) => {
        setDownloadMeta(baseUrl, item.record.infoHash, item.record.fileIdx, { ...item.record.meta, ...details(item) });
    });
};

const useShowInfo = (groups: Group[], baseUrl: string | null): Runtimes => {
    const profile = useProfile() as { addons: { transportUrl: string, manifest: { id: string } }[] };
    const cinemeta = profile.addons.find((addon) => addon.manifest.id === CINEMETA)?.transportUrl.replace(/\/manifest\.json$/, '') ?? null;
    const [runtimes, setRuntimes] = useState<Runtimes>(() => new Map(runtimesCache));

    useEffect(() => {
        if (baseUrl === null || cinemeta === null) {
            return;
        }
        groups.filter((group) => !group.unknown && group.name.length > 0).forEach((group) => {
            const type = typeOf(group);
            if (group.poster === null && !searched.has(group.name.toLowerCase())) {
                searched.add(group.name.toLowerCase());
                search(cinemeta, type, group.name).then((meta) => {
                    if (meta !== null) {
                        writeMeta(baseUrl, group.items, (item) => ({
                            metaId: item.record.meta?.metaId ?? meta.id,
                            type: item.record.meta?.type ?? meta.type,
                            name: meta.name,
                            poster: meta.poster,
                            season: item.record.meta?.season ?? (item.season || undefined),
                            episode: item.record.meta?.episode ?? (item.episode || undefined),
                        }));
                    }
                });
            }
            const metaId = group.metaId;
            if (metaId !== null && !looked.has(metaId)) {
                looked.add(metaId);
                getJson<{ meta?: Meta }>(`${cinemeta}/meta/${type}/${encodeURIComponent(metaId)}.json`).then((body) => {
                    const runtime = parseRuntime(body?.meta?.runtime);
                    if (runtime === null) {
                        return;
                    }
                    runtimesCache.set(metaId, runtime);
                    setRuntimes(new Map(runtimesCache));
                    writeMeta(baseUrl, group.items.filter((item) => item.record.meta?.runtime === undefined), () => ({ runtime }));
                });
            }
        });
    }, [groups, baseUrl, cinemeta]);

    return runtimes;
};

export default useShowInfo;
