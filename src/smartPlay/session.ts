// Shared smart-play state: autoplay requests handed from Resume/next-episode
// to the streams list, and the chosen source's supply handed to the player.

import { useSyncExternalStore } from 'react';

type AutoplayRequest = {
    metaId: string,
    videoId: string,
};

// The source smart play started: its measured rate and the rate its file
// needs (bytes per second), for the player's start buffer (startBuffer.ts).
type Handoff = {
    infoHash: string,
    rate: number,
    needed: number,
};

type State = {
    autoplay: AutoplayRequest | null,
    handoff: Handoff | null,
};

let state: State = { autoplay: null, handoff: null };
const listeners = new Set<() => void>();

const getState = () => state;

const setState = (patch: Partial<State>) => {
    state = { ...state, ...patch };
    listeners.forEach((listener) => listener());
};

const subscribe = (listener: () => void) => {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
};

const useSmartPlayState = () => useSyncExternalStore(subscribe, getState);

export type { AutoplayRequest, Handoff };
export { getState as getSmartPlayState, setState, useSmartPlayState };
