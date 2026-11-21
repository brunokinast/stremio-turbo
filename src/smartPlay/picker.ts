// Copyright (C) 2017-2026 Smart code 203358507

// The picker, one procedure for every flow: walk the quality steps
// (candidates.ts); in each, test the top sources start-only at the start
// point and take the healthiest healthy one (health.ts). A copy on disk needs
// nothing and wins at once. When none in a step is healthy, step down (the
// caller says so in a toast) and repeat; when no step has one, the healthiest
// seen plays anyway, flagged weak.
//
// A test is a temporary record on the server (POST /downloads, kind
// temporary): it primes the file's start, end and the window at the start
// point, and is deleted when its lease lapses (2 min) unless renewed. The
// winner becomes kept when the player opens it (the server records a played
// file); losers are deleted as soon as a step is decided. A source that
// already has a kept record (a manual download, a Next pick) is measured as
// it is and never deleted: the picker only removes the tests it added.

import { addDownload, hasKept, removeDownload } from './downloads';
import type { Download, DownloadMeta } from './downloads';
import { stats } from './serverApi';
import { knownRate, RateMeter } from './supply';
import { healthRatio, need } from './health';
import { decide } from './pickDecision';
import type { Candidate } from './pickDecision';
import type { RankedSource } from './groupByQuality';
import type { Step } from './candidates';

// How long a step waits for every candidate's rate.
const STEP_TIMEOUT = 20000;
const POLL = 1000;
// Temporary records are renewed this often (their lease is 2 min).
const RENEW = 30000;

type StepResult = 'testing' | 'healthy' | 'too-slow';

type Progress = {
    steps: { step: Step, result: StepResult, best: number | null }[],
    candidates: Candidate[],
    // The healthiest candidate so far (for "Play leader now").
    leader: Candidate | null,
};

type PickResult = {
    source: RankedSource,
    step: Step,
    ratio: number | null,
    // No step had a healthy source: the healthiest seen plays.
    weak: boolean,
    // Steps passed over, with their best ratio.
    passed: { step: Step, best: number | null }[],
    // The source is a test this pick added (the caller renews or deletes it);
    // false for a copy on disk or a record that was already kept.
    owned: boolean,
};

type PickOptions = {
    baseUrl: string,
    steps: Step[],
    startFraction: number,
    runtime: number | null,
    // The server's records, for the bytes already on disk.
    records: Download[] | null,
    meta?: DownloadMeta,
    meter: RateMeter,
    signal: AbortSignal,
    onProgress?: (progress: Progress) => void,
    onStepDown?: (from: Step, best: number | null, to: Step) => void,
    // On cancel, the source not to delete (the leader the caller plays).
    keep?: () => RankedSource | null,
    // Sources taken on trust, as if on disk: another file of the torrent
    // being played, which the server holds back while it plays, so a test
    // can't measure it (the watch checks its real progress later).
    trusted?: (source: RankedSource) => boolean,
};

const completedOf = (records: Download[] | null, source: RankedSource) => {
    const record = (records ?? []).find((d) => d.infoHash === source.infoHash && d.fileIdx === source.fileIdx);
    return record !== undefined ? record.bytesCompleted : 0;
};

const sleep = (ms: number, signal: AbortSignal) => new Promise<void>((resolve, reject) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener('abort', () => {
        clearTimeout(timer);
        reject(new DOMException('Pick cancelled', 'AbortError'));
    }, { once: true });
});

const pick = async ({ baseUrl, steps, startFraction, runtime, records, meta, meter, signal, onProgress, onStepDown, keep, trusted }: PickOptions): Promise<PickResult> => {
    if (steps.length === 0) {
        throw new Error('no sources');
    }
    const progress: Progress = { steps: [], candidates: [], leader: null };
    const passed: { step: Step, best: number | null }[] = [];
    let overall: { candidate: Candidate, step: Step } | null = null;
    const report = () => onProgress?.({ ...progress, steps: [...progress.steps], candidates: [...progress.candidates] });

    for (let i = 0; i < steps.length; i++) {
        const step = steps[i];
        progress.steps.push({ step, result: 'testing', best: null });
        const tested = step.sources;
        const owns = (source: RankedSource): source is RankedSource & { fileIdx: number } => source.onDisk === null && source.fileIdx !== null && !hasKept(records, source);
        const register = () => tested.forEach((source) => {
            if (owns(source)) {
                addDownload(baseUrl, source.infoHash, source.fileIdx, source.trackers, { kind: 'temporary', origin: 'picker', startFraction, meta }).catch(() => undefined);
            }
        });
        register();
        const renew = setInterval(register, RENEW);
        const started = Date.now();
        let decision: Candidate | 'none' | null = null;
        try {
            for (;;) {
                const now = Date.now();
                await Promise.all(tested.map((source) => source.onDisk !== null ? null : stats(baseUrl, source.infoHash).then((s) => {
                    if (s !== null) {
                        meter.add(source.infoHash, now, s.downloaded);
                    }
                })));
                const candidates = tested.map((source): Candidate => {
                    const needed = source.onDisk !== null || trusted?.(source) === true ? 0 : need({ size: source.size, completed: completedOf(records, source), runtime, startFraction });
                    const live = meter.rate(source.infoHash);
                    const known = live !== null ? { rate: live } : knownRate(meter, source, runtime);
                    const rate = known?.rate ?? null;
                    return { source, need: needed, rate, ratio: healthRatio(rate, needed), live: live !== null || needed === 0 };
                });
                progress.candidates = candidates;
                const leader = candidates.reduce<Candidate | null>((best, c) => c.ratio !== null && (best === null || c.ratio > (best.ratio ?? 0)) ? c : best, null);
                progress.leader = leader;
                if (leader !== null && (overall === null || (leader.ratio ?? 0) > (overall.candidate.ratio ?? 0))) {
                    overall = { candidate: leader, step };
                }
                progress.steps[i].best = leader?.ratio ?? null;
                decision = decide(candidates, Date.now() - started >= STEP_TIMEOUT);
                report();
                if (decision !== null) {
                    break;
                }
                await sleep(POLL, signal);
            }
        } finally {
            clearInterval(renew);
            if (signal.aborted) {
                // Cancelled (page left, or "Play leader now"): the step's tests
                // go, except the one the caller is about to play.
                tested.forEach((source) => {
                    if (owns(source) && source !== keep?.()) {
                        removeDownload(baseUrl, source.infoHash, source.fileIdx);
                    }
                });
            }
        }
        const winner = decision !== 'none' && decision !== null ? decision : null;
        // Losers (or the whole step) go now, not when their lease lapses.
        tested.forEach((source) => {
            if (source !== winner?.source && owns(source)) {
                removeDownload(baseUrl, source.infoHash, source.fileIdx);
            }
        });
        if (winner !== null) {
            progress.steps[i].result = 'healthy';
            report();
            return { source: winner.source, step, ratio: winner.ratio, weak: false, passed, owned: owns(winner.source) };
        }
        progress.steps[i].result = 'too-slow';
        passed.push({ step, best: progress.steps[i].best });
        report();
        if (i + 1 < steps.length) {
            onStepDown?.(step, progress.steps[i].best, steps[i + 1]);
        }
    }
    const fallback = overall ?? { candidate: { source: steps[0].sources[0], need: null, rate: null, ratio: null, live: false }, step: steps[0] };
    return { source: fallback.candidate.source, step: fallback.step, ratio: fallback.candidate.ratio, weak: true, passed, owned: overall !== null && !hasKept(records, fallback.candidate.source) && fallback.candidate.source.onDisk === null };
};

export type { Candidate, PickResult, Progress, StepResult };
export { pick, STEP_TIMEOUT };
