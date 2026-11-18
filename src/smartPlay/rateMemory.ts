// Each source's last measured rate ÷ bitrate, kept per device for a day. A
// swarm's health changes over days, not between two plays of an evening: the
// House of the Dragon remux measured 0.02-0.4x its bitrate in all eight plays
// of one evening, while the race waited up to 5 s for a fresh rate before
// passing it over (streaming-research findings/23). A remembered ratio only
// stands in until a live rate arrives, and nothing is chosen on it alone.

const STORAGE_KEY = 'smartPlay.rates';
const MEMORY_TTL = 24 * 60 * 60 * 1000;
const MAX_ENTRIES = 200;

type Entry = { ratio: number, at: number };

const load = (): Record<string, Entry> => {
    try {
        const parsed = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}');
        return parsed !== null && typeof parsed === 'object' ? parsed : {};
    } catch (_e) {
        return {};
    }
};

const remember = (infoHash: string, ratio: number) => {
    if (!isFinite(ratio) || ratio < 0) {
        return;
    }
    const entries = load();
    entries[infoHash] = { ratio, at: Date.now() };
    const kept = Object.entries(entries)
        .sort(([, a], [, b]) => b.at - a.at)
        .slice(0, MAX_ENTRIES);
    try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries(kept)));
    } catch (_e) {
        // Storage unavailable: nothing is remembered.
    }
};

// The ratio measured within the last day, or null.
const recall = (infoHash: string): number | null => {
    const entry = load()[infoHash];
    return entry !== undefined && typeof entry.ratio === 'number' && Date.now() - entry.at < MEMORY_TTL ? entry.ratio : null;
};

export { remember, recall, MEMORY_TTL };
