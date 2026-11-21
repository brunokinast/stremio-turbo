// Copyright (C) 2017-2026 Smart code 203358507

// The last decision of a background flow (the Next slot, the mid-episode
// watch), kept for diagnosis only when localStorage `smartPlay.debug` is
// set: read it back from sessionStorage `smartPlay.<name>Trace`.

const enabled = () => {
    try {
        return localStorage.getItem('smartPlay.debug') !== null;
    } catch (_e) {
        return false;
    }
};

const debugTrace = (name: string, entry: object) => {
    if (!enabled()) {
        return;
    }
    try {
        sessionStorage.setItem(`smartPlay.${name}Trace`, JSON.stringify({ at: new Date().toISOString(), ...entry }));
    } catch (_e) {
        // Storage unavailable: no trace.
    }
};

export default debugTrace;
