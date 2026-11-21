// Plan 11's watch: the sliding-window rate and the 30 s unhealthy rule.

const { WindowRate, HealthWatch, UNHEALTHY_FOR } = require('../../src/smartPlay/watch');

describe('window rate', () => {
    it('is unknown until the samples span 15 s', () => {
        const r = new WindowRate();
        r.add(0, 0);
        r.add(10000, 10e6);
        expect(r.rate()).toBe(null);
        r.add(20000, 20e6);
        expect(r.rate()).toBeCloseTo(1e6);
    });
    it('reads a stall as 0 once the window passes it', () => {
        const r = new WindowRate();
        for (let t = 0; t <= 60000; t += 10000) {
            r.add(t, t <= 20000 ? t * 1000 : 20e6);
        }
        expect(r.rate()).toBe(0);
    });
    it('starts over when the bytes go down (data deleted)', () => {
        const r = new WindowRate();
        r.add(0, 50e6);
        r.add(20000, 60e6);
        r.add(30000, 1e6);
        expect(r.rate()).toBe(null);
    });
});

describe('health watch', () => {
    it('fires after 30 s unhealthy in a row, then starts over', () => {
        const w = new HealthWatch();
        const fired = [0, 10000, 20000, 30000, 40000].map((t) => w.observe(t, false));
        expect(fired).toEqual([false, false, false, true, false]);
        expect(UNHEALTHY_FOR).toBe(30000);
    });
    it('a healthy check resets it', () => {
        const w = new HealthWatch();
        expect([0, 10000, 20000].map((t) => w.observe(t, false))).toEqual([false, false, false]);
        expect(w.observe(25000, true)).toBe(false);
        expect([30000, 50000, 60000].map((t) => w.observe(t, false))).toEqual([false, false, true]);
    });
    it('an unknown check neither starts nor resets it', () => {
        const w = new HealthWatch();
        expect(w.observe(0, null)).toBe(false);
        expect(w.observe(10000, false)).toBe(false);
        expect(w.observe(20000, null)).toBe(false);
        expect(w.observe(40000, false)).toBe(true);
    });
});
