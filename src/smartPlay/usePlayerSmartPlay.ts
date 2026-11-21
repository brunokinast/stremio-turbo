// Copyright (C) 2017-2026 Smart code 203358507

// Smart play in the player: the Next slot (next.ts), the mid-episode switch
// (nowWatch.ts) and the record naming (namePlaying.ts), fed from the player's
// state once, so Player.js keeps a single touch point.

import { useNextSlot } from './next';
import { useNowWatch } from './nowWatch';
import useNamePlaying from './namePlaying';

type Options = {
    // Core's player model (Player.js `player`).
    player: {
        metaItem: { type: string, content?: unknown } | null,
        selected: { stream?: { infoHash?: unknown, fileIdx?: unknown }, streamRequest?: { path?: { id?: string } } } | null,
    },
    baseUrl: string | null,
    addons: Addon[],
    // The player's video (time and duration in milliseconds).
    video: Parameters<typeof useNowWatch>[0]['video'] & { state: { time: number | null, duration: number | null, paused: boolean | null } },
    shell: Parameters<typeof useNowWatch>[0]['shell'],
};

const usePlayerSmartPlay = ({ player, baseUrl, addons, video, shell }: Options) => {
    const meta = player.metaItem !== null && player.metaItem.type === 'Ready' ? player.metaItem.content as Parameters<typeof useNextSlot>[0]['meta'] : null;
    const videoId = player.selected?.streamRequest?.path?.id ?? null;
    const stream = player.selected?.stream;
    const infoHash = typeof stream?.infoHash === 'string' ? stream.infoHash.toLowerCase() : null;
    const fileIdx = typeof stream?.fileIdx === 'number' ? stream.fileIdx : null;
    const { time, duration, paused } = video.state;

    useNextSlot({ baseUrl, addons, meta, videoId, playingInfoHash: infoHash, playingFileIdx: fileIdx, time, duration, paused });
    useNowWatch({ baseUrl, addons, meta, videoId, playingInfoHash: infoHash, playingFileIdx: fileIdx, time, duration, paused, video, shell });
    useNamePlaying({ baseUrl, meta, videoId, infoHash, fileIdx, time });
};

export default usePlayerSmartPlay;
