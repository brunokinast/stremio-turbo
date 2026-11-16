// Shared smart-play state: autoplay requests handed from Resume/next-episode
// to the streams list.

import { useSyncExternalStore } from 'react';

type AutoplayRequest = {
    metaId: string,
    videoId: string,
};

type State = {
    autoplay: AutoplayRequest | null,
};

let state: State = { autoplay: null };
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

export type { AutoplayRequest };
export { setState, useSmartPlayState };
