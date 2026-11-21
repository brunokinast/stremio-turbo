// Copyright (C) 2017-2026 Smart code 203358507

// One file on the Downloads screen: its episode, release, state and actions.

import React from 'react';
import { useTranslation } from 'react-i18next';
import classnames from 'classnames';
import Icon from '@stremio/stremio-icons/react';
import { Button, Checkbox } from 'stremio/components';
import { isStalled } from 'stremio/smartPlay/downloads';
import type { Download } from 'stremio/smartPlay/downloads';
import { waitingBehind } from 'stremio/smartPlay/stall';
import { size as gb } from 'stremio/smartPlay/format';
import { isBetter } from './groups';
import type { Group, Item } from './groups';
import { isMoving, Own, pressable } from './shared';
import styles from './Downloads.less';

const mbps = (bits: number) => `${Math.round(bits / 1e6)} Mb`;

const qualityText = (item: Item) => [item.quality.resolution, item.quality.hdr, item.quality.encode ?? item.quality.source].filter((part) => part !== null).join(' ');

type Props = {
    item: Item,
    group: Group,
    // Every record (for a file waiting behind another of its torrent).
    downloads: Download[],
    checked: boolean,
    // Its other-source race is running.
    retrying: boolean,
    onToggle: (on: boolean) => void,
    onPause: (record: Download) => void,
    onResume: (record: Download) => void,
    onRetry: (record: Download) => void,
    onDelete: (item: Item) => void,
};

const DownloadRow = ({ item, group, downloads, checked, retrying, onToggle, onPause, onResume, onRetry, onDelete }: Props) => {
    const { t } = useTranslation();
    const { record } = item;
    const stalled = isStalled(record, downloads);

    const state = () => {
        const percent = item.progress !== null ? `${Math.floor(item.progress * 100)}%` : null;
        // What the file is, when it isn't a plain download: the state says it
        // (no separate tags).
        const what = record.streaming ? t('SMART_PLAY_DOWNLOADS_STATE_PLAYING') : record.kind === 'temporary' ? t('SMART_PLAY_DOWNLOADS_STATE_TESTING') : null;
        const hint = record.streaming ? t('SMART_PLAY_DOWNLOADS_PLAYING_HINT') : record.kind === 'temporary' ? t('SMART_PLAY_DOWNLOADS_TEST_HINT') : undefined;
        const line = (parts: (string | null)[], active = false, title = hint) => (
            <span className={classnames(styles['state'], { [styles['active']]: active })} title={title}>{parts.filter((part) => part !== null).join(' · ')}</span>
        );
        if (record.info !== undefined) {
            // Its torrent's file list isn't known: no size, so no %.
            return record.info === 'pending' ?
                line([t('SMART_PLAY_DOWNLOADS_READING_INFO')], false, t('SMART_PLAY_DOWNLOADS_READING_INFO_HINT'))
                :
                line([t('SMART_PLAY_DOWNLOADS_NO_INFO')], false, t('SMART_PLAY_DOWNLOADS_NO_INFO_HINT'));
        }
        if (item.complete) {
            // Complete is the normal case: nothing to say unless it plays.
            return what !== null ? line([what]) : null;
        }
        // Waiting behind an earlier episode of its torrent: said only while
        // it gets nothing (it often downloads alongside).
        const behind = waitingBehind(record, downloads);
        if (behind !== null && record.state !== 'paused' && record.rate === 0) {
            const episode = behind.meta?.episode !== undefined ? `E${behind.meta.episode}` : t('SMART_PLAY_DOWNLOADS_EARLIER');
            return line([what, percent, t('SMART_PLAY_DOWNLOADS_WAITING', { episode })], false, t('SMART_PLAY_DOWNLOADS_WAITING_HINT'));
        }
        if (stalled) {
            return line([what, percent, retrying ? t('SMART_PLAY_DOWNLOADS_RETRYING') : t('SMART_PLAY_DOWNLOADS_STALLED')], false, t('SMART_PLAY_DOWNLOADS_STALLED_HINT'));
        }
        switch (record.state) {
            case 'downloading': {
                const left = record.rate > 0 && item.total !== null ? Math.ceil((item.total - record.bytesCompleted) / record.rate / 60) : null;
                return line([what, percent, `${(record.rate / 1e6).toFixed(1)} MB/s`, left !== null ? t('SMART_PLAY_DOWNLOADS_MINUTES_LEFT', { count: left }) : null, record.peers ? t('SMART_PLAY_DOWNLOADS_PEERS', { count: record.peers }) : null], true);
            }
            case 'queued':
                return line([what, percent, t('SMART_PLAY_DOWNLOADS_CONNECTING')], true, t('SMART_PLAY_DOWNLOADS_CONNECTING_HINT'));
            default:
                // Stopped (paused, or data nobody fetches), or a test between reads.
                return line([what, percent], record.streaming === true);
        }
    };

    // Two rows for the same episode are duplicates, whatever their state
    // (two releases kept, an unkept copy beside a kept one, two sources tested).
    const duplicate = () => {
        const others = group.unknown || item.episodeLabel === null ? [] : group.items.filter((other) => other !== item && other.episodeLabel === item.episodeLabel);
        if (others.length === 0) {
            return null;
        }
        const describe = (other: Item) => [qualityText(other) || other.fileName, other.bitrate !== null ? mbps(other.bitrate) : null, gb(other.onDisk)].filter((part) => part !== null).join(' · ');
        const best = [item, ...others].reduce((a, b) => isBetter(b, a) ? b : a);
        const hint = [
            t('SMART_PLAY_DOWNLOADS_DUPLICATE_HINT', { episode: item.episodeLabel }),
            ...others.map((other) => `• ${describe(other)}${other === best ? ` (${t('SMART_PLAY_DOWNLOADS_BETTER')})` : ''}`),
            best === item ? t('SMART_PLAY_DOWNLOADS_THIS_BETTER') : t('SMART_PLAY_DOWNLOADS_OTHER_BETTER'),
        ].join('\n');
        return <span className={classnames(styles['copies'], { [styles['best']]: best === item })} title={hint}>{t(best === item ? 'SMART_PLAY_DOWNLOADS_DUPLICATE_BEST' : 'SMART_PLAY_DOWNLOADS_DUPLICATE_WORSE')}</span>;
    };

    // Moving: pause + delete; stopped: resume + delete; stalled: also try
    // another source; anything else: delete.
    const actions = () => {
        const canPause = record.kind === 'kept' && isMoving(item);
        const playing = record.streaming === true;
        return (
            <div className={styles['actions']} onClick={(event) => event.stopPropagation()}>
                {
                    canPause ?
                        <Button className={styles['action']} title={t('SMART_PLAY_DOWNLOADS_PAUSE')} onClick={() => onPause(record)}>
                            <Icon className={styles['action-icon']} name={'pause'} />
                        </Button>
                        :
                        item.stopped ?
                            <Button className={styles['action']} title={t('SMART_PLAY_DOWNLOADS_RESUME')} onClick={() => onResume(record)}>
                                <Icon className={styles['action-icon']} name={'play'} />
                            </Button>
                            :
                            null
                }
                {
                    stalled && record.meta?.videoId !== undefined ?
                        <Button className={classnames(styles['action'], { [styles['disabled']]: retrying })} title={t('SMART_PLAY_DOWNLOADS_TRY_ANOTHER')} onClick={() => onRetry(record)}>
                            <Icon className={styles['action-icon']} name={'reset'} />
                        </Button>
                        :
                        null
                }
                <Button className={classnames(styles['action'], styles['danger'], { [styles['disabled']]: playing })} title={playing ? t('SMART_PLAY_DOWNLOADS_PLAYING_NO_DELETE') : t('SMART_PLAY_DOWNLOADS_DELETE')} onClick={() => !playing && onDelete(item)}>
                    <Icon className={styles['action-icon']} name={'bin'} />
                </Button>
            </div>
        );
    };

    // An episode by its number; a movie (or any unnumbered file) by its
    // show's name when known, else by its file.
    const label = item.episodeLabel ?? (record.meta?.name ?? item.fileName);
    const showBar = item.progress !== null && !item.complete;
    const quality = qualityText(item);
    return (
        <div className={classnames(styles['row'], styles['hoverable'], { [styles['selected']]: checked, [styles['live']]: item.live })} {...pressable(() => onToggle(!checked))}>
            <Own><Checkbox className={styles['check']} name={`select-${item.key}`} checked={checked} onChange={({ checked: on }) => onToggle(on)} /></Own>
            <div className={styles['what']}>
                <div className={styles['label']}>
                    <span className={styles['episode']}>{label}</span>
                    {item.title !== null ? <span className={styles['title']}>{item.title}</span> : null}
                    {duplicate()}
                    {record.watched ? <span className={styles['watched']}>{t('SMART_PLAY_DOWNLOADS_WATCHED')}</span> : null}
                </div>
                <div className={styles['file']} title={record.path ?? item.fileName}>
                    {item.fileName}
                </div>
            </div>
            <div className={styles['quality']} title={quality.length > 0 ? t('SMART_PLAY_DOWNLOADS_QUALITY_HINT') : undefined}>
                {quality.length > 0 ? <span className={styles['quality-text']}>{quality}</span> : null}
                {item.bitrate !== null ? <span className={styles['bitrate']}>{mbps(item.bitrate)}</span> : null}
            </div>
            <div className={styles['state-cell']}>
                {state()}
                {
                    showBar ?
                        <div className={classnames(styles['bar'], { [styles['moving']]: isMoving(item), [styles['flowing']]: record.state === 'downloading' })}>
                            <div className={styles['fill']} style={{ width: `${item.progress! * 100}%` }} />
                        </div>
                        :
                        null
                }
            </div>
            <div className={styles['size']}>
                {gb(item.onDisk)}
                {showBar && item.total !== null ? <span className={styles['of']}>{` / ${gb(item.total)}`}</span> : null}
            </div>
            {actions()}
        </div>
    );
};

export default DownloadRow;
