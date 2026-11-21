// The picker removes only the tests it added: a source that already has a
// kept record (a manual download) is measured but never deleted.

jest.mock('../../src/smartPlay/downloads', () => ({
    addDownload: jest.fn(() => Promise.resolve()),
    removeDownload: jest.fn(() => Promise.resolve()),
    hasKept: (records, file) => (records ?? []).some((d) => d.kind === 'kept' && d.infoHash === file.infoHash && d.fileIdx === file.fileIdx),
}));
jest.mock('../../src/smartPlay/serverApi', () => {
    const downloaded = {};
    const speed = { kept: 1e5, test: 1e8 };
    return {
        stats: jest.fn((_baseUrl, infoHash) => {
            downloaded[infoHash] = (downloaded[infoHash] ?? 0) + speed[infoHash];
            return Promise.resolve({ downloaded: downloaded[infoHash] });
        }),
    };
});

const { addDownload, removeDownload } = require('../../src/smartPlay/downloads');
const { pick } = require('../../src/smartPlay/picker');
const { RateMeter } = require('../../src/smartPlay/supply');

const GB = 1e9;
const source = (infoHash) => ({ infoHash, fileIdx: 0, size: 10 * GB, seeders: 10, listedSeeders: null, onDisk: null, trackers: [], stream: {}, addonName: 'T' });
const records = [{ infoHash: 'kept', fileIdx: 0, kind: 'kept', origins: ['manual'], bytesCompleted: 0 }];
const steps = () => [{ key: '4k', label: '4k', match: 'exact', sources: [source('kept'), source('test')] }];
const touched = (mock, infoHash) => mock.mock.calls.some((call) => call[1] === infoHash);

beforeEach(() => {
    jest.useFakeTimers();
    addDownload.mockClear();
    removeDownload.mockClear();
});
afterEach(() => jest.useRealTimers());

it('never adds a test to, or deletes, a kept source that loses', async () => {
    const done = pick({ baseUrl: 'x', steps: steps(), startFraction: 0, runtime: 3600, records, meter: new RateMeter(), signal: new AbortController().signal });
    await jest.advanceTimersByTimeAsync(25000);
    const result = await done;
    expect(result.source.infoHash).toBe('test');
    expect(result.owned).toBe(true);
    expect(touched(addDownload, 'kept')).toBe(false);
    expect(touched(removeDownload, 'kept')).toBe(false);
});

it('leaves a kept source alone when the pick is cancelled', async () => {
    const controller = new AbortController();
    const done = pick({ baseUrl: 'x', steps: steps(), startFraction: 0, runtime: 3600, records, meter: new RateMeter(), signal: controller.signal }).catch(() => null);
    await jest.advanceTimersByTimeAsync(500);
    controller.abort();
    await jest.advanceTimersByTimeAsync(1000);
    await done;
    expect(touched(removeDownload, 'test')).toBe(true);
    expect(touched(removeDownload, 'kept')).toBe(false);
});

it('reports a kept winner as not owned', async () => {
    const only = [{ key: '4k', label: '4k', match: 'exact', sources: [source('kept')] }];
    const done = pick({ baseUrl: 'x', steps: only, startFraction: 0, runtime: 36000, records, meter: new RateMeter(), signal: new AbortController().signal });
    await jest.advanceTimersByTimeAsync(25000);
    const result = await done;
    expect(result.source.infoHash).toBe('kept');
    expect(result.owned).toBe(false);
    expect(touched(removeDownload, 'kept')).toBe(false);
});
