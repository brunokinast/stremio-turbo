// Warm up the top continue-watching item while the board is shown: preload the
// torrent its last play used (core keeps that stream in the item's player
// link), around where it resumes. Most plays start there, through the details
// page and the streams list, and only then would smart play preload; a source
// with its swarm and resume window ready plays at once (0.2 s after a 15 s
// dwell vs 5-10 s cold, streaming-research findings/20-22).
//
// It downloads without a click, so it's a setting (on by default), and an
// unplayed warm-up is removed after WARM_FOR.

import { useCallback, useEffect, useState } from 'react';
import { useCore } from 'stremio/core';
import useStreamingServer from 'stremio/common/useStreamingServer';
import { preload, remove } from './serverApi';

const SETTING_KEY = 'smartPlay.warmUp';
const WARM_FOR = 10 * 60 * 1000;

type ContinueWatchingItem = {
    // Percent watched (0-100), as the board's progress bar draws it.
    progress?: number | null,
    deepLinks?: { player?: string | null } | null,
};

// infoHash -> removal timer (null once played).
const warmed = new Map<string, ReturnType<typeof setTimeout> | null>();

const isWarmUpEnabled = (): boolean => {
    try {
        return localStorage.getItem(SETTING_KEY) !== 'off';
    } catch (_e) {
        return true;
    }
};

const setWarmUpEnabled = (on: boolean) => {
    try {
        localStorage.setItem(SETTING_KEY, on ? 'on' : 'off');
    } catch (_e) {
        // Storage unavailable: the default (on) stays.
    }
};

// The torrent is being played: keep it.
const claim = (infoHash: string) => {
    const timer = warmed.get(infoHash);
    if (timer) {
        clearTimeout(timer);
        warmed.set(infoHash, null);
    }
};

// Claims a warmed torrent the given stream URL plays from.
const claimUrl = (url: string) => {
    warmed.forEach((_timer, infoHash) => {
        if (url.includes(infoHash)) {
            claim(infoHash);
        }
    });
};

// The stream an item's player link carries (`#/player/<stream>/...`).
const encodedStream = (link: string): string | null => {
    const encoded = link.split('/')[2];
    return typeof encoded === 'string' && encoded.length > 0 ? decodeURIComponent(encoded) : null;
};

// Board: warm the first continue-watching item when enabled and smart play runs.
const useBoardWarmUp = (items: ContinueWatchingItem[], enabled: boolean) => {
    const core = useCore();
    const baseUrl = useStreamingServer().selected?.transportUrl ?? null;
    const top = items[0];
    const link = top?.deepLinks?.player ?? null;
    const progress = top?.progress ?? 0;
    useEffect(() => {
        if (!enabled || baseUrl === null || typeof link !== 'string' || !isWarmUpEnabled()) {
            return;
        }
        const encoded = encodedStream(link);
        if (encoded === null) {
            return;
        }
        let cancelled = false;
        core.transport.decodeStream(encoded).then((stream) => {
            const infoHash = typeof stream?.infoHash === 'string' ? stream.infoHash.toLowerCase() : null;
            if (cancelled || infoHash === null || warmed.has(infoHash)) {
                return;
            }
            const trackers = Array.isArray(stream.announce) && stream.announce.length > 0 ? stream.announce : [`dht:${infoHash}`];
            const fileIdx = typeof stream.fileIdx === 'number' ? stream.fileIdx : null;
            const startFraction = progress > 0 && progress < 100 ? progress / 100 : 0;
            preload(baseUrl, infoHash, fileIdx, trackers, startFraction);
            warmed.set(infoHash, setTimeout(() => {
                if (warmed.get(infoHash)) {
                    remove(baseUrl, infoHash);
                    warmed.delete(infoHash);
                }
            }, WARM_FOR));
        }).catch(() => undefined);
        return () => {
            cancelled = true;
        };
    }, [enabled, baseUrl, link, progress, core.transport]);
};

// Settings: the toggle's props.
const useWarmUpToggle = () => {
    const [checked, setChecked] = useState(isWarmUpEnabled);
    const onClick = useCallback(() => {
        setChecked((current) => {
            setWarmUpEnabled(!current);
            return !current;
        });
    }, []);
    return { checked, onClick };
};

export { claim, claimUrl, isWarmUpEnabled, useBoardWarmUp, useWarmUpToggle };
