// Copyright (C) 2017-2026 Smart code 203358507

// Hold a smart-play start on its first frame until a short buffer builds,
// when the chosen source only just keeps up.
//
// The shell runs mpv on its defaults, which start playing with under a second
// buffered. A swarm still ramping then runs dry at once: on House of the
// Dragon 4K the source chosen at 1.08x its bitrate stalled 2.1 s, 0.7 s into
// playback, while its download climbed from 2.2 to 17.5 MB/s. Replaying 25
// measured plays, a 4 s buffer removed every such stall. The shell doesn't
// let the web UI set mpv's cache options, so the hold is a pause the web UI
// lifts.

import { useCallback, useEffect, useRef, useState } from 'react';
import { getSmartPlayState, setState } from './session';

// Buffer ahead of the position (ms) a held start waits for.
const START_BUFFER_MS = 4000;
// Sources at least this many times their bitrate start at once.
const FAST_RATIO = 2;
// A hold never lasts longer than this (ms).
const HOLD_CAP_MS = 20000;

const shouldHold = (rate: number, needed: number) => rate < FAST_RATIO * needed;

type Video = {
    state: {
        stream: { url?: string } | null,
        time: number | null,
        buffered: number | null,
    },
    setPaused: (paused: boolean) => void,
};

// holding: the start is held; cancel(): the user took over (play or pause).
const useStartBuffer = (video: Video) => {
    const [holding, setHolding] = useState(false);
    const videoRef = useRef(video);
    videoRef.current = video;

    const release = useCallback(() => {
        setHolding((current) => {
            if (current) {
                videoRef.current.setPaused(false);
            }
            return false;
        });
    }, []);

    const cancel = useCallback(() => setHolding(false), []);

    const url = video.state.stream?.url;
    useEffect(() => {
        const { handoff } = getSmartPlayState();
        if (typeof url !== 'string' || handoff === null || !url.includes(handoff.infoHash)) {
            return;
        }
        setState({ handoff: null });
        if (shouldHold(handoff.rate, handoff.needed)) {
            videoRef.current.setPaused(true);
            setHolding(true);
        }
    }, [url]);

    const { time, buffered } = video.state;
    useEffect(() => {
        if (holding && time !== null && buffered !== null && buffered - time >= START_BUFFER_MS) {
            release();
        }
    }, [holding, time, buffered, release]);

    useEffect(() => {
        if (!holding) {
            return;
        }
        const timer = setTimeout(release, HOLD_CAP_MS);
        return () => clearTimeout(timer);
    }, [holding, release]);

    return { holding, cancel };
};

export { shouldHold, START_BUFFER_MS, FAST_RATIO, HOLD_CAP_MS };
export default useStartBuffer;
