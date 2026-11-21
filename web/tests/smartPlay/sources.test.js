// Pure helpers of smart play's source handling: grouping add-on streams by
// quality, ranking by live seeders, the fallback quality, and release names.

const { groupByQuality, rankTiers, resolutionOf, fallbackTier } = require('../../src/smartPlay/groupByQuality');
const { default: parseRelease, parseQuality, resolutionRank } = require('../../src/smartPlay/parseRelease');

const addon = { manifest: { name: 'Torrentio' } };
const stream = (infoHash, label, extra = {}) => ({ name: `Torrentio\n${label}`, infoHash, fileIdx: 0, description: '', ...extra });

describe('groupByQuality', () => {
    it('groups by the label on the name\'s second line, once per file', () => {
        const tiers = groupByQuality([{ addon, streams: [
            stream('AA', '4k DV | HDR', { description: 'Show.S01E01\n👤 12 💾 18.2 GB' }),
            stream('aa', '4k DV | HDR'),
            stream('bb', '1080p', { behaviorHints: { videoSize: 5e9 } }),
            { name: 'no label', infoHash: 'cc' },
        ] }], null);
        expect(tiers.map((tier) => [tier.key, tier.sources.length])).toEqual([['4k dv | hdr', 1], ['1080p', 1]]);
        const [top] = tiers[0].sources;
        expect([top.infoHash, top.listedSeeders, top.size, top.addonName]).toEqual(['aa', 12, 18.2e9, 'Torrentio']);
        expect(tiers[1].sources[0].size).toBe(5e9);
    });

    it('drops streams core built for another episode', () => {
        const mine = stream('aa', '1080p', { deepLinks: { player: '#/player/x/tt1/tt1%3A1%3A2' } });
        const stale = stream('bb', '1080p', { deepLinks: { player: '#/player/x/tt1/tt1%3A1%3A1' } });
        const [tier] = groupByQuality([{ addon, streams: [mine, stale] }], 'tt1:1:2');
        expect(tier.sources.map((s) => s.infoHash)).toEqual(['aa']);
    });
});

describe('rankTiers', () => {
    it('orders by live seeders, unknown ones last by the listed figure', () => {
        const tiers = groupByQuality([{ addon, streams: [
            stream('a', '4k', { description: '👤 50' }),
            stream('b', '4k', { description: '👤 5' }),
            stream('c', '4k', { description: '👤 1' }),
        ] }], null);
        const live = new Map([['c', { seeders: 30, leechers: 0, trackers: 1, done: true }]]);
        const [tier] = rankTiers(tiers, live);
        expect(tier.sources.map((s) => s.infoHash)).toEqual(['c', 'a', 'b']);
        expect(tier.best).toBe(30);
    });
});

describe('quality fallback', () => {
    const ranked = (key, best) => ({ key, label: key, sources: [{}], best, weak: false, progress: null, disk: null });

    it('reads a tier key\'s resolution', () => {
        expect([resolutionOf('4k dv | hdr'), resolutionOf('1080p'), resolutionOf('8k'), resolutionOf('web-dl')]).toEqual([2160, 1080, 4320, null]);
    });

    it('takes the most seeded tier at the last resolution, else the first at 4K or below', () => {
        const tiers = [ranked('8k', 9), ranked('4k', 3), ranked('4k hdr', 7), ranked('1080p', 50)];
        expect(fallbackTier(tiers, '4k dv | hdr').key).toBe('4k hdr');
        expect(fallbackTier(tiers, '720p').key).toBe('4k');
        expect(fallbackTier(tiers, null).key).toBe('4k');
    });
});

describe('parseRelease', () => {
    it('reads show, season and episode from a path', () => {
        expect(parseRelease('Peaky Blinders (2013 - 2022)/season 6/Peaky.Blinders.s06e02.BDRip.1080p.mkv.part')).toMatchObject({ show: 'Peaky Blinders', season: 6, episode: 2 });
        expect(parseRelease('House.of.the.Dragon.S02E07.DV.HDR.2160p.WEB.H265-SuccessfulCrab[TGx].mkv')).toMatchObject({ show: 'House of the Dragon', season: 2, episode: 7 });
        expect(parseRelease('Some.Movie.2021.2160p.mkv')).toMatchObject({ show: 'Some Movie', season: null, episode: null });
    });

    it('reads the quality, marking only true re-encodes', () => {
        expect(parseQuality('Show.S01E01.2160p.DV.HDR.Remux.x265.mkv')).toEqual({ resolution: '2160p', hdr: 'DV', source: 'Remux', encode: null });
        expect(parseQuality('Show.S01E01.1080p.BluRay.x264.mkv')).toEqual({ resolution: '1080p', hdr: null, source: 'BluRay', encode: 'x264' });
        expect(parseQuality('Show.S01E01.HDR10+.2160p.WEB-DL.mkv')).toMatchObject({ hdr: 'HDR10+', source: 'WEB-DL' });
        expect(resolutionRank('2160p')).toBeGreaterThan(resolutionRank('1080p'));
        expect(resolutionRank(null)).toBe(0);
    });
});
