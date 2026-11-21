// Copyright (C) 2017-2026 Smart code 203358507

// The quality last chosen for each show, and the last one chosen anywhere,
// kept per device.

const storageKey = (metaId: string) => `smartPlay.quality.${metaId}`;
const LAST_KEY = 'smartPlay.quality';

const read = (key: string): string | null => {
    try {
        return localStorage.getItem(key);
    } catch (_e) {
        return null;
    }
};

const getQuality = (metaId: string): string | null => read(storageKey(metaId));

// The last quality played on any show: the best guess for a show not played yet.
const getLastQuality = (): string | null => read(LAST_KEY);

// Tier keys are lowercased labels; the label as the add-on wrote it ("4k DV
// | HDR") is kept beside it, to name a remembered quality that isn't offered.
const labelKey = (tierKey: string) => `smartPlay.qualityLabel.${tierKey}`;

const qualityLabel = (tierKey: string): string => read(labelKey(tierKey)) ?? tierKey;

const setQuality = (metaId: string, tierKey: string, label: string | null) => {
    try {
        localStorage.setItem(storageKey(metaId), tierKey);
        localStorage.setItem(LAST_KEY, tierKey);
        if (label !== null) {
            localStorage.setItem(labelKey(tierKey), label);
        }
    } catch (_e) {
        // Storage unavailable: the choice just isn't remembered.
    }
};

export { getQuality, getLastQuality, setQuality, qualityLabel };
