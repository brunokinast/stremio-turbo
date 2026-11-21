// Copyright (C) 2017-2026 Smart code 203358507

type Track = { id: string, lang?: string | null };

// The track in `tracks` for a language: the same tag ("en-US"), else the same
// base language ("en"): files tag the same language differently.
const trackFor = <T extends Track>(tracks: T[], lang: string | null) => {
    if (lang === null) {
        return undefined;
    }
    const base = (tag: string) => tag.toLowerCase().split(/[-_]/)[0];
    return tracks.find((track) => track.lang === lang) ?? tracks.find((track) => typeof track.lang === 'string' && base(track.lang) === base(lang));
};

export { trackFor };
