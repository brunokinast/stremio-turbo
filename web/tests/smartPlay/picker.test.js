// Plan 11's shared rules: health (need, margin), the quality order, and the
// picker's decision for one step.

const { need, healthRatio, isHealthy, MARGIN } = require('../../src/smartPlay/health');
const { qualitySteps } = require('../../src/smartPlay/candidates');
const { decide } = require('../../src/smartPlay/pickDecision');

const GB = 1e9;
const source = (infoHash, extra = {}) => ({ infoHash, fileIdx: 0, size: 10 * GB, seeders: 10, listedSeeders: null, onDisk: null, trackers: [], stream: {}, addonName: 'T', ...extra });
const tier = (key, sources) => ({ key, label: key, sources, best: sources[0]?.seeders ?? null, weak: false, progress: null, disk: null });

describe('health', () => {
    it('needs size / runtime from the start', () => {
        expect(need({ size: 10 * GB, completed: 0, runtime: 3600, startFraction: 0 })).toBeCloseTo(10 * GB / 3600);
    });
    it('needs nothing for a file all on disk', () => {
        expect(need({ size: 10 * GB, completed: 10 * GB, runtime: 3600, startFraction: 0.5 })).toBe(0);
        expect(healthRatio(null, 0)).toBe(Infinity);
        expect(isHealthy(null, 0)).toBe(true);
    });
    it('needs less with part downloaded', () => {
        const fresh = need({ size: 10 * GB, completed: 0, runtime: 3600, startFraction: 0 });
        const half = need({ size: 10 * GB, completed: 5 * GB, runtime: 3600, startFraction: 0 });
        expect(half).toBeCloseTo(fresh / 2);
    });
    it('keeps the same need at a resume point (less to fetch, less time)', () => {
        expect(need({ size: 10 * GB, completed: 0, runtime: 3600, startFraction: 0.5 })).toBeCloseTo(10 * GB / 3600);
    });
    it('is unknown without size or runtime', () => {
        expect(need({ size: null, completed: 0, runtime: 3600, startFraction: 0 })).toBe(null);
        expect(need({ size: GB, completed: 0, runtime: null, startFraction: 0 })).toBe(null);
    });
    it('is healthy from the margin up', () => {
        expect(MARGIN).toBe(1.25);
        expect(isHealthy(1.25, 1)).toBe(true);
        expect(isHealthy(1.2, 1)).toBe(false);
        expect(isHealthy(null, 1)).toBe(false);
    });
});

describe('quality order', () => {
    const tiers = [
        tier('4k dv | hdr', [source('a', { seeders: 5 }), source('b', { seeders: 50 })]),
        tier('4k hdr', [source('c', { seeders: 20 })]),
        tier('4k', [source('d', { seeders: 9 })]),
        tier('1080p', [source('e')]),
        tier('720p', [source('f')]),
        tier('web-dl', [source('g')]),
        tier('480p', []),
    ];
    it('goes exact, same resolution, then each lower one', () => {
        const steps = qualitySteps(tiers, '4k dv | hdr');
        expect(steps.map((s) => [s.match, s.label])).toEqual([
            ['exact', '4k dv | hdr'],
            ['same', '4k hdr / 4k'],
            ['lower', '1080p'],
            ['lower', '720p'],
        ]);
    });
    it('orders a step by live seeders, top 3', () => {
        const steps = qualitySteps([tier('4k', [1, 2, 3, 4, 5].map((n) => source(`s${n}`, { seeders: n })))], '4k');
        expect(steps[0].sources.map((s) => s.infoHash)).toEqual(['s5', 's4', 's3']);
    });
    it('always tests a copy on disk', () => {
        const steps = qualitySteps([tier('4k', [source('x', { seeders: 90 }), source('y', { seeders: 80 }), source('z', { seeders: 70 }), source('disk', { seeders: 0, onDisk: 7 * GB })])], '4k');
        expect(steps[0].sources[0].infoHash).toBe('disk');
    });
    it('starts at the resolution of a quality not offered', () => {
        const steps = qualitySteps(tiers, '4k remux');
        expect(steps[0]).toMatchObject({ match: 'exact', label: '4k dv | hdr / 4k hdr / 4k' });
    });
});

describe('step decision', () => {
    const candidate = (extra) => ({ source: source('a'), need: 1, rate: null, ratio: null, live: false, ...extra });
    it('takes a copy on disk at once', () => {
        const disk = candidate({ need: 0, ratio: Infinity, live: true });
        expect(decide([candidate(), disk], false)).toBe(disk);
    });
    it('waits until every candidate has a live rate', () => {
        expect(decide([candidate({ rate: 3, ratio: 3, live: true }), candidate()], false)).toBe(null);
    });
    it('picks the healthiest healthy one', () => {
        const a = candidate({ rate: 2, ratio: 2, live: true });
        const b = candidate({ rate: 5, ratio: 5, live: true });
        const c = candidate({ rate: 1, ratio: 1, live: true });
        expect(decide([a, b, c], false)).toBe(b);
    });
    it('reports none when nobody keeps up with the margin', () => {
        expect(decide([candidate({ rate: 1.1, ratio: 1.1, live: true })], false)).toBe('none');
    });
    it('decides at the time limit with whoever has a rate', () => {
        const a = candidate({ rate: 3, ratio: 3, live: true });
        expect(decide([a, candidate()], true)).toBe(a);
        expect(decide([candidate()], true)).toBe('none');
    });
    it("stops waiting for a slow source once one has kept up long enough", () => {
        const fast = candidate({ rate: 3, ratio: 3, live: true });
        expect(decide([fast, candidate()], false, false)).toBe(null);
        expect(decide([fast, candidate()], false, true)).toBe(fast);
    });
    it("keeps waiting when nobody has kept up, however long", () => {
        expect(decide([candidate({ rate: 1.1, ratio: 1.1, live: true }), candidate()], false, true)).toBe(null);
    });
    it("doesn't take a remembered rate as live", () => {
        expect(decide([candidate({ rate: 9, ratio: 9, live: false })], true)).toBe('none');
    });
});
