// Episode list: which of a show's episodes have a file on disk, and how far
// each is, from the server's record (the shared poller). A record names its
// episode when the client said which one it is; older next-episode downloads
// are found through the download-ahead memory (ahead.ts).

import { useCallback, useMemo } from 'react';
import { downloadedSource } from './ahead';
import { useDownloads } from './downloads';
import type { Download } from './downloads';

const POLL = 5000;

// videoId -> its file on disk.
const useEpisodeDownloads = (metaId: string | null, videoIds: string[], enabled: boolean) => {
    const { downloads: records, remove: removeRecords } = useDownloads(POLL, enabled && metaId !== null);
    const idsKey = videoIds.join(',');

    const downloads = useMemo(() => {
        const map = new Map<string, Download>();
        if (metaId === null || records === null) {
            return map;
        }
        const kept = records.filter((d) => d.kind === 'kept');
        videoIds.forEach((videoId) => {
            const named = kept.find((d) => d.meta?.metaId === metaId && d.meta?.videoId === videoId);
            const source = named === undefined ? downloadedSource(metaId, videoId) : null;
            const entry = named ?? (source !== null ? kept.find((d) => d.infoHash === source.infoHash && d.fileIdx === source.fileIdx) : undefined);
            if (entry !== undefined) {
                map.set(videoId, entry);
            }
        });
        return map;
        // idsKey stands for videoIds.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [records, metaId, idsKey]);

    const remove = useCallback((videoId: string) => {
        const entry = downloads.get(videoId);
        if (entry !== undefined) {
            removeRecords([entry]);
        }
    }, [downloads, removeRecords]);

    return { downloads, remove };
};

export type { Download };
export default useEpisodeDownloads;
