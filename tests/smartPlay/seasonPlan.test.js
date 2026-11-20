// Plan 12's season plan: rows per quality with step-downs and skips, the
// race's test set (a pack tested once), and the winner rule.

const { seasonRows, stepDowns, episodeRuns, testSet, chooseWinner, orderOf } = require('../../src/smartPlay/seasonPlan');

const GB = 1e9;
const source = (infoHash, extra = {}) => ({ infoHash, fileIdx: 0, size: 10 * GB, seeders: 10, listedSeeders: null, onDisk: null, trackers: [], stream: {}, addonName: 'T', ...extra });
const tier = (key, sources, disk = null) => ({ key, label: key, sources, best: sources[0]?.seeders ?? null, weak: false, progress: null, disk });
const offer = (episode, tiers, extra = {}) => ({ videoId: `tt1:1:${episode}`, season: 1, episode, title: null, unaired: false, downloading: false, tiers, ...extra });

describe('season rows', () => {
    it('lists every quality, stepping episodes that lack it down', () => {
        const offers = [
            offer(1, [tier('4k hdr', [source('a', { fileIdx: 1 })]), tier('1080p', [source('p', { fileIdx: 1 })])]),
            offer(2, [tier('1080p', [source('p', { fileIdx: 2 })])]),
            offer(3, [tier('4k hdr', [source('a', { fileIdx: 3 })]), tier('1080p', [source('p', { fileIdx: 3 })])]),
        ];
        const rows = seasonRows(offers);
        expect(rows.map((row) => row.key)).toEqual(['4k hdr', '1080p']);
        const hdr = rows[0];
        expect(hdr.episodes.map((ep) => [ep.episode, ep.label, ep.stepped])).toEqual([[1, '4k hdr', false], [2, '1080p', true], [3, '4k hdr', false]]);
        expect(stepDowns(hdr)).toEqual([{ label: '1080p', episodes: 'E2' }]);
        expect(hdr.bytes).toBe(30 * GB);
    });

    it('skips episodes on disk, downloading or unaired', () => {
        const disk = source('d', { onDisk: 5 * GB });
        const offers = [
            offer(1, [tier('1080p', [disk], disk)]),
            offer(2, [tier('1080p', [source('b')])], { downloading: true }),
            offer(3, [tier('1080p', [source('c')])], { unaired: true }),
            offer(4, [tier('1080p', [source('e')])], { onDisk: true }),
            offer(5, [tier('1080p', [source('f')])]),
        ];
        const [row] = seasonRows(offers);
        expect(row.episodes.map((ep) => ep.episode)).toEqual([5]);
        expect(row.skipped).toEqual({ onDisk: 2, downloading: 1, unaired: 1 });
    });

    it('leaves out sources without a file index', () => {
        const [row] = seasonRows([offer(1, [tier('1080p', [source('x', { fileIdx: null, seeders: 99 }), source('y', { seeders: 5 })])])]);
        expect(row.episodes[0].sources.map((s) => s.infoHash)).toEqual(['y']);
    });

    it('reports the weakest episode', () => {
        const [row] = seasonRows([offer(1, [tier('1080p', [source('a', { seeders: 40 })])]), offer(2, [tier('1080p', [source('b', { seeders: 7 })])])]);
        expect(row.weakest).toBe(7);
    });
});

describe('episode runs', () => {
    it('groups consecutive episodes', () => {
        expect(episodeRuns([8, 3, 4, 5])).toBe('E3–E5, E8');
        expect(episodeRuns([2])).toBe('E2');
    });
});

describe('test set', () => {
    const planned = (episode, sources) => ({ videoId: `v${episode}`, season: 1, episode, title: null, label: '4k', stepped: false, sources });

    it('tests a pack once, on its earliest episode', () => {
        const eps = [
            planned(2, [source('pack', { fileIdx: 2, seeders: 100 }), source('b2', { seeders: 20 })]),
            planned(1, [source('pack', { fileIdx: 1, seeders: 100 }), source('b1', { seeders: 30 })]),
        ];
        const tests = testSet(eps);
        expect(Array.from(tests.keys()).sort()).toEqual(['b1', 'b2', 'pack']);
        expect(tests.get('pack').videoId).toBe('v1');
        expect(tests.get('pack').source.fileIdx).toBe(1);
    });

    it('keeps every episode its top source under the cap', () => {
        const eps = [1, 2, 3].map((e) => planned(e, [source(`top${e}`, { seeders: 1 }), source(`x${e}`, { seeders: 50 }), source(`y${e}`, { seeders: 40 })]));
        const tests = testSet(eps, 4);
        expect(tests.size).toBe(4);
        ['top1', 'top2', 'top3'].forEach((h) => expect(tests.has(h)).toBe(true));
        expect(tests.has('x1')).toBe(true);
    });
});

describe('winner', () => {
    const planned = (sources) => ({ videoId: 'v', season: 1, episode: 1, title: null, label: '4k', stepped: false, sources });

    it('takes the one that finishes soonest', () => {
        const big = source('big', { size: 20 * GB });
        const small = source('small', { size: 5 * GB });
        // big: 20 GB at 10 MB/s = 2000 s; small: 5 GB at 4 MB/s = 1250 s.
        expect(chooseWinner(planned([big, small]), new Map([['big', 10e6], ['small', 4e6]]))).toBe(small);
    });

    it('falls back to the best seeded with no rate', () => {
        const a = source('a');
        expect(chooseWinner(planned([a, source('b')]), new Map([['a', null], ['b', 0]]))).toBe(a);
    });

    it('orders by season then episode', () => {
        expect(orderOf(2, 5)).toBe(2005);
        expect(orderOf(1, 12)).toBeLessThan(orderOf(2, 1));
    });
});

describe('a quality an episode lacks entirely', () => {
    it('counts as a step down even when the order starts at its resolution', () => {
        const offers = [
            offer(1, [tier('cam', [source('c', { fileIdx: 1 })]), tier('4k', [source('k', { fileIdx: 1 })])]),
            offer(2, [tier('4k', [source('k', { fileIdx: 2 })])]),
        ];
        const cam = seasonRows(offers).find((row) => row.key === 'cam');
        expect(cam.episodes.map((ep) => [ep.episode, ep.label, ep.stepped])).toEqual([[1, 'cam', false], [2, '4k', true]]);
    });
});
