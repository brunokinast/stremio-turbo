// Copyright (C) 2017-2026 Smart code 203358507

// Seconds of steady playback: the player's time moving forward while playing,
// by about the wall time passed (a seek jumps further and doesn't count).
// Counted from zero again whenever `resetKey` changes; the caller may also
// reset the returned ref itself (after switching files).

import { useEffect, useRef } from 'react';

// `time` in milliseconds.
const useSteadyPlayback = (time: number | null, paused: boolean | null, resetKey: string) => {
    const played = useRef(0);
    const last = useRef<{ time: number, at: number } | null>(null);

    useEffect(() => {
        played.current = 0;
        last.current = null;
    }, [resetKey]);

    useEffect(() => {
        if (time === null) {
            return;
        }
        const now = Date.now();
        const prev = last.current;
        last.current = { time, at: now };
        if (prev !== null && paused === false && time > prev.time && time - prev.time <= (now - prev.at) * 2 + 1000) {
            played.current += Math.min(time - prev.time, now - prev.at) / 1000;
        }
    }, [time, paused]);

    return played;
};

export default useSteadyPlayback;
