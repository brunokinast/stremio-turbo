// When a kept file counts as stalled. Of a torrent's files still to
// download, the server gives the earliest episode first claim on its peers
// (it reports that file as `first`); the others can gain nothing while peers
// only have bytes for it, so they wait behind it rather than stall.

type StallRecord = { infoHash: string, fileIdx: number, first?: boolean };

// A file that hasn't gained a byte for this long is stalled.
const STALL_AFTER = 10 * 60 * 1000;

// The file of the same torrent that downloads first while this one waits.
const waitingBehind = <T extends StallRecord>(record: StallRecord, all: T[]): T | null =>
    record.first === true ? null : all.find((other) => other.infoHash === record.infoHash && other.fileIdx !== record.fileIdx && other.first === true) ?? null;

// `lastMoved`: when the file last gained bytes (undefined: not watched yet).
const stalledSince = (lastMoved: number | undefined, now: number, behind: boolean) =>
    !behind && lastMoved !== undefined && now - lastMoved >= STALL_AFTER;

export { STALL_AFTER, waitingBehind, stalledSince };
