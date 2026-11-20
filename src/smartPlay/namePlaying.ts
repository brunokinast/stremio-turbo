// Names the file being played on the server's record: the server records a
// played file as kept on its own, without knowing which episode it is, so the
// Downloads screen could only show its file name. Once playback has started
// (the record exists), the show and episode are attached, once per file.

import { useEffect, useRef } from 'react';
import { setDownloadMeta } from './downloads';

type Options = {
    baseUrl: string | null,
    meta: { id: string, type: string, name?: string, poster?: string | null, videos?: Video[] } | null,
    videoId: string | null,
    infoHash: string | null,
    fileIdx: number | null,
    // Playback position (ms); the record exists once it has advanced.
    time: number | null,
};

const useNamePlaying = ({ baseUrl, meta, videoId, infoHash, fileIdx, time }: Options) => {
    const named = useRef<string | null>(null);
    const started = time !== null && time > 0;
    useEffect(() => {
        if (!started || baseUrl === null || meta === null || videoId === null || infoHash === null || fileIdx === null) {
            return;
        }
        const key = `${infoHash}/${fileIdx}/${videoId}`;
        if (named.current === key) {
            return;
        }
        named.current = key;
        const video = meta.videos?.find(({ id }) => id === videoId);
        setDownloadMeta(baseUrl, infoHash, fileIdx, {
            metaId: meta.id,
            videoId,
            type: meta.type,
            name: meta.name,
            poster: meta.poster ?? undefined,
            season: video?.season,
            episode: video?.episode,
            title: video?.title || undefined,
        });
    }, [started, baseUrl, meta, videoId, infoHash, fileIdx]);
};

export default useNamePlaying;
