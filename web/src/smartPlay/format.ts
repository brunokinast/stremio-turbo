// Copyright (C) 2017-2026 Smart code 203358507

// Numbers as smart play shows them. Unknown is always "–".

const UNKNOWN = '–';

// Gigabytes with one decimal (the unit is in the sentence around it).
const gb = (bytes: number | null | undefined) => bytes === null || bytes === undefined ? UNKNOWN : (bytes / 1e9).toFixed(1);

// A size with its unit: GB from 1 GB, MB below.
const size = (bytes: number) => bytes >= 1e9 ? `${(bytes / 1e9).toFixed(1)} GB` : `${Math.max(0, Math.round(bytes / 1e6))} MB`;

// A health ratio (rate ÷ needed rate), without the "×".
const times = (ratio: number | null) => ratio === null ? UNKNOWN : ratio === Infinity ? '∞' : ratio.toFixed(1);

export { gb, size, times, UNKNOWN };
