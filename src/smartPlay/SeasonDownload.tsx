// "Download season" (plan 12): a button beside the season picker with the
// season's live status, opening a chooser of every quality the season's
// episodes offer. The chooser fills in as answers arrive: each episode's
// sources from the add-ons (qualities, sizes, step-downs), then live seeders,
// then, once a row's ⬇ is clicked, each episode's race. It opens as a
// popover by the button, and as a centered dialog when that doesn't fit.

import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import classNames from 'classnames';
import Icon from '@stremio/stremio-icons/react';
import { useProfile } from 'stremio/common';
import Button from 'stremio/components/Button';
import ModalDialog from 'stremio/components/ModalDialog';
import { groupByQuality, rankTiers } from './groupByQuality';
import type { Tier } from './groupByQuality';
import useLiveSeeders from './useLiveSeeders';
import { isComplete, useDownloads } from './downloads';
import { withDisk } from './withDisk';
import { fetchStreams } from './next';
import { getQuality } from './qualityMemory';
import { seasonRows, stepDowns } from './seasonPlan';
import type { EpisodeOffer, SeasonRow } from './seasonPlan';
import { startDownload, useManualDownloads } from './manualDownload';
import type { Job, Show } from './manualDownload';
import styles from './SeasonDownload.less';

const POLL = 5000;
// Free space kept spare after a download.
const SPARE = 10e9;

type Props = {
    show: Show,
    season: number,
    // The season's episodes.
    videos: Video[],
};

const gb = (bytes: number) => (bytes / 1e9).toFixed(1);

// The release name a source card shows first (Torrentio: the torrent title).
const releaseOf = (description: string | undefined) => (description ?? '').split('\n')[0];

// Each episode's sources from the add-ons, fetched once per opening.
const useSeasonTiers = (open: boolean, show: Show, videos: Video[]) => {
    const profile = useProfile();
    const [tiers, setTiers] = useState<Map<string, Tier[]>>(() => new Map());
    const [pending, setPending] = useState(0);
    const key = `${show.id}:${videos.map(({ id }) => id).join(',')}`;
    useEffect(() => {
        if (!open) {
            return;
        }
        let active = true;
        const aired = videos.filter((video) => !video.upcoming);
        setTiers(new Map());
        setPending(aired.length);
        aired.forEach((video) => {
            fetchStreams(profile.addons, show.type, video.id)
                .then((groups) => {
                    if (active) {
                        setTiers((previous) => new Map(previous).set(video.id, groupByQuality(groups, null)));
                    }
                })
                .finally(() => {
                    if (active) {
                        setPending((count) => count - 1);
                    }
                });
        });
        return () => {
            active = false;
        };
        // key stands for show and videos.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [open, key]);
    return { tiers, loading: pending > 0, total: videos.filter((video) => !video.upcoming).length, loaded: tiers.size };
};

const SeasonDownload = ({ show, season, videos }: Props) => {
    const { t } = useTranslation();
    const [open, setOpen] = useState(false);
    const [asDialog, setAsDialog] = useState(false);
    const [expanded, setExpanded] = useState<string | null>(null);
    const { downloads: records, disk, baseUrl } = useDownloads(POLL);
    const jobs = useManualDownloads();
    const { tiers: fetched, loading, total, loaded } = useSeasonTiers(open, show, videos);
    const allTiers = useMemo(() => Array.from(fetched.values()).flat(), [fetched]);
    const { live, settled } = useLiveSeeders(allTiers, open ? baseUrl : null);
    const remembered = getQuality(show.id);
    const menuRef = useRef<HTMLDivElement>(null);

    // The season's records, by episode.
    const seasonRecords = useMemo(() => {
        const ids = new Set(videos.map(({ id }) => id));
        return (records ?? []).filter((d) => d.kind === 'kept' && d.meta?.metaId === show.id && d.meta?.videoId !== undefined && ids.has(d.meta.videoId));
    }, [records, videos, show.id]);

    const offers = useMemo((): EpisodeOffer[] => videos.map((video) => ({
        videoId: video.id,
        season: video.season ?? season,
        episode: video.episode ?? 0,
        title: video.title ?? null,
        unaired: video.upcoming,
        downloading: seasonRecords.some((d) => d.meta?.videoId === video.id && !isComplete(d)),
        onDisk: seasonRecords.some((d) => d.meta?.videoId === video.id && isComplete(d)),
        tiers: withDisk(rankTiers(fetched.get(video.id) ?? [], live), records, video.id),
    })), [videos, season, seasonRecords, fetched, live, records]);

    const allRows = useMemo(() => seasonRows(offers), [offers]);
    // Qualities with nothing left to fetch aren't offered.
    const rows = useMemo(() => allRows.filter((row) => row.episodes.length > 0), [allRows]);
    const seasonJob = jobs.find((job) => job.kind === 'season' && job.metaId === show.id && job.season === season && job.state === 'racing') ?? null;
    const lastJob = jobs.filter((job) => job.kind === 'season' && job.metaId === show.id && job.season === season && job.state !== 'undone').pop() ?? null;

    // The button's status: racing, then progress over the season's records.
    const aired = videos.filter((video) => !video.upcoming).length;
    const onDisk = videos.filter((video) => seasonRecords.some((d) => d.meta?.videoId === video.id && isComplete(d))).length;
    const moving = seasonRecords.filter((d) => !isComplete(d));
    const percent = seasonRecords.length > 0 ?
        Math.floor(100 * seasonRecords.reduce((sum, d) => sum + d.bytesCompleted, 0) / Math.max(1, seasonRecords.reduce((sum, d) => sum + d.size, 0)))
        :
        0;
    const status = seasonJob !== null ?
        t('SMART_PLAY_SEASON_CHOOSING')
        :
        aired > 0 && onDisk >= aired ?
            t('SMART_PLAY_SEASON_ON_DISK')
            :
            moving.length > 0 ?
                t('SMART_PLAY_SEASON_PROGRESS', { done: onDisk, total: aired, percent })
                :
                onDisk > 0 ?
                    t('SMART_PLAY_SEASON_PARTLY', { done: onDisk, total: aired })
                    :
                    t('SMART_PLAY_SEASON_DOWNLOAD');

    const close = useCallback(() => {
        setOpen(false);
        setAsDialog(false);
        setExpanded(null);
    }, []);

    // Esc and clicks outside close the popover.
    useEffect(() => {
        if (!open || asDialog) {
            return;
        }
        const onKey = (event: KeyboardEvent) => event.key === 'Escape' && close();
        const onDown = (event: MouseEvent) => {
            if (menuRef.current !== null && !menuRef.current.parentElement?.contains(event.target as Node)) {
                close();
            }
        };
        window.addEventListener('keydown', onKey);
        window.addEventListener('mousedown', onDown);
        return () => {
            window.removeEventListener('keydown', onKey);
            window.removeEventListener('mousedown', onDown);
        };
    }, [open, asDialog, close]);

    // The popover becomes the dialog once its content doesn't fit.
    useLayoutEffect(() => {
        const menu = menuRef.current;
        if (open && !asDialog && menu !== null && menu.scrollHeight > menu.clientHeight + 1) {
            setAsDialog(true);
        }
    }, [open, asDialog, rows, expanded, loading, jobs]);

    const download = useCallback((row: SeasonRow) => {
        if (baseUrl !== null && row.episodes.length > 0) {
            startDownload(baseUrl, show, 'season', row.label, row.episodes);
            setExpanded(row.key);
        }
    }, [baseUrl, show]);

    const renderSeeders = (seeders: number | null) => (
        <span className={styles['seeders']}>
            <span className={classNames(styles['dot'], { [styles['weak']]: seeders !== null && seeders < 10, [styles['unknown']]: seeders === null, [styles['checking']]: !settled })} />
            {seeders ?? '?'}
        </span>
    );

    const renderBreakdown = (row: SeasonRow, job: Job | null) => (
        <div className={styles['breakdown']}>
            {
                row.episodes.map((planned) => {
                    const ep = job?.episodes.find((e) => e.planned.videoId === planned.videoId) ?? null;
                    const shown = ep?.winner ?? planned.sources[0] ?? null;
                    const best = ep !== null ? Math.max(0, ...planned.sources.map((s) => ep.rates[s.infoHash] ?? 0)) : 0;
                    return (
                        <div key={planned.videoId} className={classNames(styles['episode'], { [styles['stepped']]: planned.stepped })}>
                            <span className={styles['episode-number']}>{`E${planned.episode}`}</span>
                            <span className={styles['release']} title={releaseOf(shown?.stream.description)}>
                                {planned.stepped ? <span className={styles['episode-quality']}>{planned.label}</span> : null}
                                {releaseOf(shown?.stream.description)}
                            </span>
                            <span className={styles['episode-size']}>{shown?.size !== null && shown?.size !== undefined ? `${gb(shown.size)} GB` : '?'}</span>
                            <span className={styles['episode-state']}>
                                {
                                    ep === null ?
                                        renderSeeders(shown?.seeders ?? null)
                                        :
                                        ep.state === 'racing' ?
                                            <span className={styles['racing']}>{best > 0 ? t('SMART_PLAY_SEASON_RATE', { rate: (best / 1e6).toFixed(1) }) : t('SMART_PLAY_SEASON_TESTING')}</span>
                                            :
                                            ep.state === 'queued' ?
                                                <span className={styles['queued']}>
                                                    <Icon className={styles['queued-icon']} name={'checkmark'} />
                                                    {t('SMART_PLAY_DOWNLOAD_QUEUED')}
                                                </span>
                                                :
                                                <span className={styles['failed']}>{t('SMART_PLAY_SEASON_NO_SOURCE')}</span>
                                }
                            </span>
                        </div>
                    );
                })
            }
        </div>
    );

    const renderRow = (row: SeasonRow) => {
        const job = lastJob !== null && lastJob.quality === row.label ? lastJob : null;
        const fits = disk === null || row.bytes + SPARE <= disk.free;
        const downs = stepDowns(row);
        const busy = seasonJob !== null;
        return (
            <div key={row.key} className={styles['row']}>
                <div className={styles['row-head']}>
                    <Button
                        className={styles['row-main']}
                        title={t('SMART_PLAY_SEASON_DETAILS')}
                        aria-expanded={expanded === row.key}
                        onClick={() => setExpanded(expanded === row.key ? null : row.key)}
                    >
                        <span className={styles['label']}>{row.label}</span>
                        {row.key === remembered ? <span className={styles['flag']}>{t('SMART_PLAY_LAST_PLAYED')}</span> : null}
                        <span className={styles['count']}>{t('SMART_PLAY_SEASON_EPISODES', { count: row.episodes.length })}</span>
                        <span className={classNames(styles['size'], { [styles['too-big']]: !fits })}>{`${gb(row.bytes)} GB`}</span>
                        {renderSeeders(row.weakest)}
                        <Icon className={classNames(styles['caret'], { [styles['open']]: expanded === row.key })} name={'caret-down'} />
                    </Button>
                    <Button
                        className={classNames(styles['download'], { [styles['disabled']]: !fits || busy || row.episodes.length === 0 })}
                        title={!fits && disk !== null ? t('SMART_PLAY_SEASON_NO_SPACE', { size: gb(row.bytes), free: gb(disk.free), spare: gb(SPARE) }) : t('SMART_PLAY_SEASON_DOWNLOAD_HINT', { count: row.episodes.length, quality: row.label, size: gb(row.bytes) })}
                        disabled={!fits || busy || row.episodes.length === 0}
                        onClick={() => download(row)}
                    >
                        <Icon className={styles['icon']} name={'download'} />
                    </Button>
                </div>
                {
                    downs.map((down) => (
                        <div key={down.label} className={styles['step-down']}>{t('SMART_PLAY_SEASON_STEPPED', { episodes: down.episodes, quality: down.label })}</div>
                    ))
                }
                {expanded === row.key ? renderBreakdown(row, job) : null}
            </div>
        );
    };

    const skipped = allRows[0]?.skipped ?? { onDisk: offers.filter((o) => o.onDisk === true || o.tiers.some((tier) => tier.disk !== null)).length, downloading: 0, unaired: videos.filter((v) => v.upcoming).length };
    const footer = [
        skipped.onDisk > 0 ? t('SMART_PLAY_SEASON_SKIP_ON_DISK', { count: skipped.onDisk }) : null,
        skipped.downloading > 0 ? t('SMART_PLAY_SEASON_SKIP_DOWNLOADING', { count: skipped.downloading }) : null,
        skipped.unaired > 0 ? t('SMART_PLAY_SEASON_SKIP_UNAIRED', { count: skipped.unaired }) : null,
        disk !== null ? t('SMART_PLAY_SEASON_FREE', { free: gb(disk.free) }) : null,
    ].filter((part) => part !== null).join(' · ');

    const content = (
        <React.Fragment>
            {
                loading ?
                    <div className={styles['loading']}>{t('SMART_PLAY_SEASON_LOADING', { loaded, total })}</div>
                    :
                    rows.length === 0 ?
                        <div className={styles['nothing']}>{t('SMART_PLAY_SEASON_NOTHING')}</div>
                        :
                        null
            }
            <div className={styles['rows']}>{rows.map(renderRow)}</div>
            {footer.length > 0 ? <div className={styles['footer']}>{footer}</div> : null}
        </React.Fragment>
    );

    return (
        <div className={styles['season-download']}>
            <Button
                className={classNames(styles['button'], { [styles['active']]: seasonJob !== null || moving.length > 0, [styles['done']]: aired > 0 && onDisk >= aired })}
                title={t('SMART_PLAY_SEASON_HINT', { season })}
                aria-expanded={open}
                onClick={() => (open ? close() : setOpen(true))}
            >
                <Icon className={styles['button-icon']} name={aired > 0 && onDisk >= aired ? 'checkmark' : 'download'} />
                <span className={classNames(styles['button-label'], { [styles['checking']]: seasonJob !== null })}>{status}</span>
            </Button>
            {
                open && !asDialog ?
                    <div ref={menuRef} className={styles['popover']} role={'dialog'} aria-label={t('SMART_PLAY_SEASON_TITLE', { season })}>
                        <div className={styles['title']}>{t('SMART_PLAY_SEASON_TITLE', { season })}</div>
                        {content}
                    </div>
                    :
                    null
            }
            {
                open && asDialog ?
                    <ModalDialog className={styles['dialog']} title={t('SMART_PLAY_SEASON_TITLE_SHOW', { name: show.name, season })} onCloseRequest={close}>
                        {content}
                    </ModalDialog>
                    :
                    null
            }
        </div>
    );
};

export default SeasonDownload;
