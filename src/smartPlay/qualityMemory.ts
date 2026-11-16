// The quality last chosen for each show, kept per device.

const storageKey = (metaId: string) => `smartPlay.quality.${metaId}`;

const getQuality = (metaId: string): string | null => {
    try {
        return localStorage.getItem(storageKey(metaId));
    } catch (_e) {
        return null;
    }
};

const setQuality = (metaId: string, tierKey: string) => {
    try {
        localStorage.setItem(storageKey(metaId), tierKey);
    } catch (_e) {
        // Storage unavailable: the choice just isn't remembered.
    }
};

export { getQuality, setQuality };
