// Copyright (C) 2017-2026 Smart code 203358507

// Show, season and episode from a torrent file path, for records the client
// never named (files recorded before it said which episode they are, and
// leftover folders): "Peaky Blinders (2013 - 2022)/season 6/Peaky.Blinders.
// s06e02.BDRip.1080p.mkv.part" -> Peaky Blinders, 6, 2.

type Quality = {
    // "2160p", "1080p", …
    resolution: string | null,
    // "DV", "HDR10+", "HDR"
    hdr: string | null,
    // "Remux", "WEB-DL", "WEBRip", "BluRay", "WEB"
    source: string | null,
    // "x265"/"x264" when the release is a re-encode (a group compressed it
    // again), not the stream or disc as published.
    encode: string | null,
};

type Release = {
    show: string | null,
    season: number | null,
    episode: number | null,
    quality: Quality,
};

const RESOLUTIONS: [RegExp, string][] = [
    [/\b(2160p|4k|uhd)\b/i, '2160p'],
    [/\b1080[pi]\b/i, '1080p'],
    [/\b720p\b/i, '720p'],
    [/\b(480p|576p|sd)\b/i, '480p'],
];

// Higher is better; unknown sorts lowest.
const resolutionRank = (resolution: string | null): number => resolution === null ? 0 : (RESOLUTIONS.length - RESOLUTIONS.findIndex(([, label]) => label === resolution));

const parseQuality = (path: string): Quality => {
    // Word boundaries need separators, not dots or brackets.
    const text = path.replace(/[._[\]()]+/g, ' ');
    const resolution = RESOLUTIONS.find(([pattern]) => pattern.test(text))?.[1] ?? null;
    const hdr = /\b(DV|DoVi|Dolby ?Vision)\b/i.test(text) ? 'DV' : /\bHDR10(\+|Plus)/i.test(text) ? 'HDR10+' : /\bHDR(10)?\b/i.test(text) ? 'HDR' : null;
    const source = /\bremux\b/i.test(text) ? 'Remux' :
        /\bweb[ -]?dl\b/i.test(text) ? 'WEB-DL' :
            /\bweb[ -]?rip\b/i.test(text) ? 'WEBRip' :
                /\b(blu[ -]?ray|bdrip|brrip|bd)\b/i.test(text) ? 'BluRay' :
                    /\bweb\b/i.test(text) ? 'WEB' : null;
    const encoder = /\bx26([45])\b/i.exec(text);
    const encode = encoder !== null && source !== 'Remux' && source !== 'WEB-DL' ? `x26${encoder[1]}` : null;
    return { resolution, hdr, source, encode };
};

const EPISODE = /[Ss](\d{1,2})[ ._-]?[Ee](\d{1,3})/;

// "Peaky.Blinders." / "House of the Dragon (2022) [S02]" -> "Peaky Blinders" / "House of the Dragon".
const cleanTitle = (raw: string): string => {
    return raw
        .replace(/\.(mkv|mp4|avi|part)$/gi, '')
        .replace(/[._]+/g, ' ')
        .replace(/[([][^)\]]*[)\]]/g, ' ')
        .replace(/\b(19|20)\d{2}\b.*$/, ' ')
        .replace(/\s+-\s*$/, '')
        .replace(/\s+/g, ' ')
        .trim();
};

const parseRelease = (path: string): Release => {
    const parts = path.split('/').filter((part) => part.length > 0);
    const file = parts[parts.length - 1] ?? '';
    const match = EPISODE.exec(file) ?? EPISODE.exec(path);
    const season = match !== null ? Number(match[1]) : null;
    const episode = match !== null ? Number(match[2]) : null;
    const fromFile = match !== null && match.index > 0 && EPISODE.test(file) ? cleanTitle(file.slice(0, file.search(EPISODE))) : '';
    const fromFolder = parts.length > 1 ? cleanTitle(parts[0]) : '';
    const show = fromFile || fromFolder || (match === null ? cleanTitle(file) : '');
    return { show: show.length > 0 ? show : null, season, episode, quality: parseQuality(path) };
};

export type { Quality, Release };
export { parseQuality, resolutionRank };
export default parseRelease;
