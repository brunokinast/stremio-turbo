// The Downloads screen's grouping: by show (client-named and parsed records
// merge by name), unknown files last, temporary tests only while they move.

jest.mock('../../src/smartPlay/downloads', () => ({
    isLive: (d) => d.state === 'downloading' || d.state === 'queued' || d.streaming === true,
    isComplete: (d) => d.state === 'complete',
    isStopped: (d) => d.state === 'paused' || d.state === 'idle',
}));

const { groupDownloads } = require('../../src/routes/Downloads/groups');

const record = (infoHash, fileIdx, extra = {}) => ({ infoHash, fileIdx, kind: 'kept', state: 'complete', size: 1e9, bytesCompleted: 1e9, bytesOnDisk: 1e9, rate: 0, origins: [], ...extra });

describe('groupDownloads', () => {
    it('groups by show, merging client-named and parsed records of the same name', () => {
        const groups = groupDownloads([
            record('a', 1, { meta: { metaId: 'tt1', name: 'House of the Dragon', season: 2, episode: 1 } }),
            record('b', 0, { path: 'House.of.the.Dragon.S02E02.2160p.mkv' }),
            record('c', 0, { path: 'random-file.bin' }),
        ], 'all', 'size');
        expect(groups.map((g) => [g.unknown ? '(unknown)' : g.name, g.items.length])).toEqual([['House of the Dragon', 2], ['(unknown)', 1]]);
        expect(groups[0].items.map((item) => item.episodeLabel)).toEqual(['S2 E1', 'S2 E2']);
    });

    it('lists a temporary test only while it moves', () => {
        const groups = groupDownloads([
            record('a', 0, { kind: 'temporary', state: 'idle', path: 'Show.S01E01.1080p.mkv' }),
            record('b', 0, { kind: 'temporary', state: 'downloading', path: 'Show.S01E02.1080p.mkv' }),
        ], 'all', 'size');
        expect(groups.flatMap((g) => g.items.map((item) => item.record.infoHash))).toEqual(['b']);
    });
});
