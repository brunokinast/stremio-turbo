// A file waiting behind an earlier episode of its torrent isn't stalled.

const { STALL_AFTER, waitingBehind, stalledSince } = require('../../src/smartPlay/stall');

const record = (fileIdx, first = false) => ({ infoHash: 'pack', fileIdx, first });

describe('stall', () => {
    it('finds the file a torrent downloads first', () => {
        const all = [record(1, true), record(2), record(0), { infoHash: 'other', fileIdx: 1, first: true }];
        expect(waitingBehind(all[1], all)).toBe(all[0]);
        expect(waitingBehind(all[0], all)).toBe(null);
        expect(waitingBehind({ infoHash: 'lone', fileIdx: 0 }, all)).toBe(null);
    });

    it('is stalled after STALL_AFTER without bytes, unless waiting behind another file', () => {
        const now = 1e9;
        expect(stalledSince(now - STALL_AFTER, now, false)).toBe(true);
        expect(stalledSince(now - STALL_AFTER + 1, now, false)).toBe(false);
        expect(stalledSince(now - STALL_AFTER, now, true)).toBe(false);
        expect(stalledSince(undefined, now, false)).toBe(false);
    });
});
