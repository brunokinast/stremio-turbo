// Copyright (C) 2017-2026 Smart code 203358507

// The watch on the episode playing: every 10 s, the playing
// file's health from its record (bytes still missing past the position /
// time left, against the download rate). Unhealthy 30 s in a row, the shared
// picker runs from the current position, skipping the torrent playing, and
// a healthy winner (or a faster one, when nothing keeps up) takes over
// mid-episode: the shell's mpv loads it at the same position (`loadfile
// replace start=+<s>`, the command the player loads with; measured gap
// 0.26-0.37 s) and the audio and subtitle tracks are chosen again by
// language. The new file becomes kept when it
// opens (the server records a played file); the old record is deleted.
//
// Shell only: in a browser the player can't swap its file in place.

import { useCallback, useEffect, useRef } from 'react';
import { RateMeter } from './supply';
import { isAutomatic, listDownloads, removeDownload } from './downloads';
import type { Download } from './downloads';
import { qualitySteps } from './candidates';
import { pick } from './picker';
import { healthRatio, isHealthy, need } from './health';
import { HealthWatch, WATCH_EVERY, WindowRate } from './watch';
import { episodeTiers, startQuality } from './addonStreams';
import { useNotify } from './notify';
import useSteadyPlayback from './useSteadyPlayback';
import debugTrace from './debugTrace';

// Not before this much steady playback (the start hold covers the start),
// nor this close to the end.
const WATCH_AFTER = 60;
const END_MARGIN = 180;
// How long track re-selection waits for the new file's track list.
const TRACKS_WAIT = 15000;

type Track = { id: string, lang?: string | null, label?: string | null };

type VideoApi = {
    state: {
        audioTracks: Track[],
        selectedAudioTrackId: string | null,
        subtitlesTracks: Track[],
        selectedSubtitlesTrackId: string | null,
    },
    setAudioTrack: (id: string) => void,
    setSubtitlesTrack: (id: string | null) => void,
};

type Shell = { active: boolean, send: (method: string, ...args: (string | number | object)[]) => void };

type Options = {
    baseUrl: string | null,
    addons: Addon[],
    meta: { id: string, type: string } | null,
    videoId: string | null,
    playingInfoHash: string | null,
    playingFileIdx: number | null,
    // Player time and duration in milliseconds.
    time: number | null,
    duration: number | null,
    paused: boolean | null,
    video: VideoApi,
    shell: Shell,
};

const langOf = (tracks: Track[], id: string | null) => id === null ? null : tracks.find((track) => track.id === id)?.lang ?? null;

const useNowWatch = ({ baseUrl, addons, meta, videoId, playingInfoHash, playingFileIdx, time, duration, paused, video, shell }: Options) => {
    const notify = useNotify();
    // The file actually playing: core's stream, until a switch replaces it.
    const playing = useRef<{ infoHash: string, fileIdx: number } | null>(null);
    const played = useSteadyPlayback(time, paused, `${videoId}:${playingInfoHash}:${playingFileIdx}`);
    const watched = useRef<{ key: string, rate: WindowRate, health: HealthWatch } | null>(null);
    const busy = useRef<AbortController | null>(null);
    // Tracks to choose again once the new file's list arrives.
    const pendingTracks = useRef<{ audio: string | null, subtitles: string | null, hadSubtitles: boolean, oldAudio: Track[], until: number } | null>(null);
    const live = useRef({ baseUrl, addons, meta, videoId, time, duration, paused, video, shell });
    live.current = { baseUrl, addons, meta, videoId, time, duration, paused, video, shell };

    useEffect(() => {
        playing.current = playingInfoHash !== null && playingFileIdx !== null ? { infoHash: playingInfoHash, fileIdx: playingFileIdx } : null;
        watched.current = null;
        return () => {
            busy.current?.abort();
            busy.current = null;
        };
    }, [videoId, playingInfoHash, playingFileIdx]);

    // After a switch: the new file's tracks, chosen by the old ones' languages.
    const { audioTracks, subtitlesTracks } = video.state;
    useEffect(() => {
        const pending = pendingTracks.current;
        if (pending === null) {
            return;
        }
        if (Date.now() > pending.until) {
            pendingTracks.current = null;
            return;
        }
        if (audioTracks.length === 0 || audioTracks === pending.oldAudio) {
            return;
        }
        pendingTracks.current = null;
        const audio = audioTracks.find((track) => track.lang === pending.audio);
        if (audio !== undefined) {
            video.setAudioTrack(audio.id);
        }
        const subtitles = pending.subtitles !== null ? subtitlesTracks.find((track) => track.lang === pending.subtitles) : undefined;
        video.setSubtitlesTrack(pending.hadSubtitles && subtitles !== undefined ? subtitles.id : null);
        // video's setters are stable; the lists decide.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [audioTracks, subtitlesTracks]);

    const switchTo = useCallback(async (current: { infoHash: string, fileIdx: number }, ratio: number | null, old: Download) => {
        const ctx = live.current;
        if (ctx.baseUrl === null || ctx.meta === null || ctx.videoId === null || ctx.time === null || ctx.duration === null) {
            return;
        }
        const { baseUrl: base, meta: show, videoId: episode } = ctx;
        const controller = new AbortController();
        busy.current = controller;
        try {
            const startFraction = ctx.time / ctx.duration;
            const records = await listDownloads(base);
            const tiers = await episodeTiers(ctx.addons, show.type, episode, records, new Set([current.infoHash]));
            const steps = qualitySteps(tiers, startQuality(show.id));
            if (steps.length === 0) {
                return;
            }
            const result = await pick({
                baseUrl: base,
                steps,
                startFraction,
                runtime: ctx.duration / 1000,
                records,
                meter: new RateMeter(),
                signal: controller.signal,
            });
            const source = result.source;
            const now = live.current;
            const stillPlaying = playing.current !== null && playing.current.infoHash === current.infoHash && now.videoId === episode && now.time !== null;
            if (!stillPlaying || source.fileIdx === null || (result.weak && (result.ratio ?? 0) <= (ratio ?? 0))) {
                if (result.owned && source.fileIdx !== null) {
                    removeDownload(base, source.infoHash, source.fileIdx);
                }
                return;
            }
            const { state } = now.video;
            pendingTracks.current = {
                audio: langOf(state.audioTracks, state.selectedAudioTrackId),
                subtitles: langOf(state.subtitlesTracks, state.selectedSubtitlesTrackId),
                hadSubtitles: state.selectedSubtitlesTrackId !== null,
                oldAudio: state.audioTracks,
                until: Date.now() + TRACKS_WAIT,
            };
            const seconds = Math.floor((now.time ?? 0) / 1000);
            now.shell.send('mpv-command', ['loadfile', new URL(`${source.infoHash}/${source.fileIdx}`, base).toString(), 'replace', '-1', `start=+${seconds}`]);
            playing.current = { infoHash: source.infoHash, fileIdx: source.fileIdx };
            watched.current = null;
            played.current = 0;
            // The old record goes once mpv has closed it (a record deleted while
            // read keeps its data), unless it's complete or the user chose it.
            if (old.state !== 'complete' && isAutomatic(old)) {
                setTimeout(() => removeDownload(base, current.infoHash, current.fileIdx), 5000);
            }
            notify.now({ quality: result.step.label, gb: (source.onDisk ?? source.size ?? 0) / 1e9, ratio: result.ratio, oldRatio: ratio });
        } catch (_e) {
            // Cancelled, or no sources: the file playing stays.
        } finally {
            if (busy.current === controller) {
                busy.current = null;
            }
        }
    }, [notify, played]);
    const switchRef = useRef(switchTo);
    switchRef.current = switchTo;

    useEffect(() => {
        const timer = setInterval(async () => {
            const ctx = live.current;
            const current = playing.current;
            if (!ctx.shell.active || busy.current !== null || current === null || ctx.baseUrl === null || ctx.time === null || ctx.duration === null || ctx.duration <= 0 || ctx.paused !== false || played.current < WATCH_AFTER || (ctx.duration - ctx.time) / 1000 < END_MARGIN) {
                return;
            }
            const records = await listDownloads(ctx.baseUrl);
            const record = records?.find((d) => d.infoHash === current.infoHash && d.fileIdx === current.fileIdx);
            if (record === undefined || record.state === 'complete' || live.current.time === null) {
                return;
            }
            const key = `${current.infoHash}:${current.fileIdx}`;
            if (watched.current === null || watched.current.key !== key) {
                watched.current = { key, rate: new WindowRate(), health: new HealthWatch() };
            }
            const now = Date.now();
            watched.current.rate.add(now, record.bytesCompleted);
            const rate = watched.current.rate.rate();
            const position = live.current.time ?? 0;
            const needed = need({ size: record.size, completed: record.bytesCompleted, runtime: ctx.duration / 1000, startFraction: position / ctx.duration });
            const healthy = rate === null || needed === null ? null : isHealthy(rate, needed);
            debugTrace('now', { key, rate, needed, healthy });
            if (watched.current.health.observe(now, healthy)) {
                switchRef.current(current, healthRatio(rate, needed), record);
            }
        }, WATCH_EVERY);
        return () => clearInterval(timer);
    }, [played]);
};

export { useNowWatch };
