// Episode list: which episodes of a show were downloaded ahead (ahead.ts) and
// how far each download is, polled from the server while the list is shown.

import { useCallback, useEffect, useState } from 'react';
import useStreamingServer from 'stremio/common/useStreamingServer';
import { downloadedSource } from './ahead';
import { listDownloads, removeDownload } from './downloads';
import type { Download } from './downloads';

const POLL = 5000;

// videoId -> its download, for the show's episodes downloaded ahead.
const useEpisodeDownloads = (metaId: string | null, videoIds: string[], enabled: boolean) => {
    const baseUrl = useStreamingServer().selected?.transportUrl ?? null;
    const [downloads, setDownloads] = useState<Map<string, Download>>(() => new Map());
    const idsKey = videoIds.join(',');

    const refresh = useCallback(() => {
        if (baseUrl === null || metaId === null) {
            return;
        }
        const sources = videoIds
            .map((videoId) => ({ videoId, source: downloadedSource(metaId, videoId) }))
            .filter(({ source }) => source !== null);
        if (sources.length === 0) {
            setDownloads((current) => current.size === 0 ? current : new Map());
            return;
        }
        listDownloads(baseUrl).then((list) => {
            const next = new Map<string, Download>();
            sources.forEach(({ videoId, source }) => {
                const entry = list?.find((d) => d.infoHash === source!.infoHash && d.fileIdx === source!.fileIdx);
                if (entry !== undefined) {
                    next.set(videoId, entry);
                }
            });
            setDownloads(next);
        });
        // idsKey stands for videoIds.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [baseUrl, metaId, idsKey]);

    useEffect(() => {
        if (!enabled) {
            return;
        }
        refresh();
        const timer = setInterval(refresh, POLL);
        return () => clearInterval(timer);
    }, [enabled, refresh]);

    const remove = useCallback((videoId: string) => {
        const entry = downloads.get(videoId);
        if (baseUrl !== null && entry !== undefined) {
            removeDownload(baseUrl, entry.infoHash, entry.fileIdx).then(refresh);
        }
    }, [baseUrl, downloads, refresh]);

    return { downloads, remove };
};

export type { Download };
export default useEpisodeDownloads;
