// Smart play's toasts for decisions it makes on its own (each automatic
// choice is said out loud), in one place.

import { useCallback, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import useToast from 'stremio/common/Toast/useToast';

type NextEvent =
    | { kind: 'queued', episode: string, quality: string, gb: number, ratio: number | null, lower: boolean }
    | { kind: 'on-disk', episode: string, quality: string, gb: number }
    | { kind: 'weak', episode: string, quality: string, gb: number, ratio: number | null }
    | { kind: 'replaced', episode: string, quality: string, gb: number, ratio: number | null, oldGb: number, oldRatio: number | null }
    | { kind: 'none', episode: string }
    | { kind: 'failed', episode: string, reason: string };

const times = (ratio: number | null) => ratio === null ? '?' : ratio === Infinity ? '∞' : ratio.toFixed(1);

const useNotify = () => {
    const { t } = useTranslation();
    const toast = useToast();
    const next = useCallback((event: NextEvent) => {
        const common = 'quality' in event ? { quality: event.quality, size: event.gb.toFixed(1) } : {};
        const title =
            event.kind === 'queued' ? t(event.lower ? 'SMART_PLAY_AHEAD_QUEUED_LOWER' : 'SMART_PLAY_AHEAD_QUEUED', { episode: event.episode, ...common, ratio: times(event.ratio) }) :
                event.kind === 'on-disk' ? t('SMART_PLAY_AHEAD_ON_DISK', { episode: event.episode, ...common }) :
                    event.kind === 'weak' ? t('SMART_PLAY_AHEAD_WEAK', { episode: event.episode, ...common, ratio: times(event.ratio) }) :
                        event.kind === 'replaced' ? t('SMART_PLAY_AHEAD_REPLACED', { episode: event.episode, ...common, ratio: times(event.ratio), passedSize: event.oldGb.toFixed(1), oldRatio: times(event.oldRatio) }) :
                            event.kind === 'none' ? t('SMART_PLAY_AHEAD_NONE', { episode: event.episode }) :
                                t('SMART_PLAY_AHEAD_FULL', { episode: event.episode, reason: event.reason });
        toast.show({ type: event.kind === 'failed' ? 'error' : 'info', title, timeout: 8000 });
    }, [t, toast]);
    return useMemo(() => ({ next }), [next]);
};

export type { NextEvent };
export { useNotify, times };
