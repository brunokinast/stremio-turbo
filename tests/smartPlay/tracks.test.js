// After a source switch, tracks are chosen again by language.

const { trackFor } = require('../../src/smartPlay/tracks');

const tracks = [{ id: '1', lang: 'pt' }, { id: '2', lang: 'en' }, { id: '3', lang: 'en-GB' }, { id: '4', lang: null }];

describe('trackFor', () => {
    it('prefers the same tag', () => {
        expect(trackFor(tracks, 'en-GB').id).toBe('3');
    });
    it('falls back to the same base language', () => {
        expect(trackFor(tracks, 'en-US').id).toBe('2');
        expect(trackFor([{ id: '5', lang: 'pt_BR' }], 'pt').id).toBe('5');
    });
    it('finds nothing for another language or none', () => {
        expect(trackFor(tracks, 'fr')).toBeUndefined();
        expect(trackFor(tracks, null)).toBeUndefined();
    });
});
