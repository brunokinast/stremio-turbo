// Downloads: everything the streaming server keeps on disk, grouped by show,
// with each file's quality and state: complete (nothing shown), moving (an
// accent bar: downloading, playing, testing, connecting) or stopped (a grey
// bar: paused, or data nobody is fetching, resumable). The server deletes
// nothing it keeps by itself; this is where the user frees space, pauses and
// resumes downloads.

import React, { useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import classnames from 'classnames';
import Icon from '@stremio/stremio-icons/react';
import { Button, Checkbox, Chips, Image, MainNavBars, ModalDialog, MultiselectMenu } from 'stremio/components';
import { useDownloads } from 'stremio/smartPlay/downloads';
import type { Download } from 'stremio/smartPlay/downloads';
import { cancelAhead } from 'stremio/smartPlay/next';
import { isLowDisk } from 'stremio/smartPlay/DiskCheck';
import { groupDownloads, isBetter, recordKey } from './groups';
import useShowInfo from './useShowInfo';
import type { Filter, Group, Item, Sort } from './groups';
import styles from './Downloads.less';

const POLL = 3000;

// Shows and seasons the user collapsed, kept while the app runs.
const collapsedMemory = new Set<string>();

const gb = (bytes: number) => bytes >= 1e9 ? `${(bytes / 1e9).toFixed(1)} GB` : `${Math.max(0, Math.round(bytes / 1e6))} MB`;
const mbps = (bits: number) => `${Math.round(bits / 1e6)} Mb`;

// What the confirm dialog deletes; stop: some of it is still downloading.
type Pending = { mode: 'delete' | 'stop', items: Item[], bytes: number } | null;

const isMoving = (item: Item) => item.record.state === 'downloading' || item.record.state === 'queued';

// Controls inside a clickable row or header (checkbox, buttons) keep their
// click to themselves.
const Own = ({ children, className }: { children: React.ReactNode, className?: string }) => (
    <div className={className} style={{ display: 'contents' }} onClick={(event) => event.stopPropagation()}>{children}</div>
);

// A whole header or row as one control: click, Enter or Space.
const pressable = (onPress: () => void, title?: string) => ({
    role: 'button',
    tabIndex: 0,
    title,
    onClick: onPress,
    onKeyDown: (event: React.KeyboardEvent) => {
        if (event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ')) {
            event.preventDefault();
            onPress();
        }
    },
});

const qualityText = (item: Item) => [item.quality.resolution, item.quality.hdr, item.quality.encode ?? item.quality.source].filter((part) => part !== null).join(' ');

const Downloads = () => {
    const { t } = useTranslation();
    const { downloads, disk, remove, pause, resume, baseUrl } = useDownloads(POLL);
    const [filter, setFilter] = useState<Filter>('all');
    const [sort, setSort] = useState<Sort>('size');
    const [selected, setSelected] = useState<Set<string>>(() => new Set());
    const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set(collapsedMemory));
    const [pending, setPending] = useState<Pending>(null);
    const [busy, setBusy] = useState(false);

    const named = useMemo(() => groupDownloads(downloads ?? [], 'all', sort), [downloads, sort]);
    const runtimes = useShowInfo(named, baseUrl);
    const all = useMemo(() => groupDownloads(downloads ?? [], 'all', sort, runtimes), [downloads, sort, runtimes]);
    const groups = useMemo(() => filter === 'all' ? all : groupDownloads(downloads ?? [], filter, sort, runtimes), [all, downloads, filter, sort, runtimes]);
    const items = useMemo(() => all.flatMap((group) => group.items), [all]);
    const byKey = useMemo(() => new Map(items.map((item) => [item.key, item])), [items]);
    const total = useMemo(() => items.reduce((sum, item) => sum + item.onDisk, 0), [items]);
    const watched = useMemo(() => items.filter((item) => item.record.watched && !item.record.streaming), [items]);
    const liveCount = useMemo(() => items.filter((item) => item.live).length, [items]);
    const stoppedCount = useMemo(() => items.filter((item) => item.stopped).length, [items]);
    const selectedItems = useMemo(() => Array.from(selected).map((key) => byKey.get(key)).filter((item): item is Item => item !== undefined), [selected, byKey]);
    const selectedBytes = selectedItems.reduce((sum, item) => sum + item.onDisk, 0);
    const low = disk !== null && isLowDisk(disk);

    const toggle = useCallback((keys: string[], on: boolean) => {
        setSelected((current) => {
            const next = new Set(current);
            keys.forEach((key) => on ? next.add(key) : next.delete(key));
            return next;
        });
    }, []);
    const toggleCollapsed = useCallback((key: string) => {
        setCollapsed((current) => {
            const next = new Set(current);
            next.has(key) ? next.delete(key) : next.add(key);
            collapsedMemory.clear();
            next.forEach((k) => collapsedMemory.add(k));
            return next;
        });
    }, []);
    // A playing file can't be deleted (the server keeps data being read).
    const ask = useCallback((list: Item[]) => {
        const deletable = list.filter((item) => !item.record.streaming);
        if (deletable.length > 0) {
            setPending({ mode: deletable.some(isMoving) ? 'stop' : 'delete', items: deletable, bytes: deletable.reduce((sum, item) => sum + item.onDisk, 0) });
        }
    }, []);
    const confirm = useCallback(() => {
        if (pending === null) {
            return;
        }
        setBusy(true);
        // A deleted next-episode download isn't started again by itself.
        pending.items.forEach(({ record }) => {
            if (record.meta?.metaId !== undefined && record.meta.videoId !== undefined && (record.origins ?? []).includes('next')) {
                cancelAhead(record.meta.metaId, record.meta.videoId);
            }
        });
        const records: Download[] = pending.items.map((item) => item.record);
        remove(records).finally(() => {
            const gone = new Set(records.map(recordKey));
            setSelected((current) => new Set(Array.from(current).filter((key) => !gone.has(key))));
            setPending(null);
            setBusy(false);
        });
    }, [pending, remove]);

    const filterChips = {
        options: [
            { value: 'all', label: t('SMART_PLAY_DOWNLOADS_FILTER_ALL') },
            { value: 'downloading', label: liveCount > 0 ? `${t('SMART_PLAY_DOWNLOADS_FILTER_DOWNLOADING')} (${liveCount})` : t('SMART_PLAY_DOWNLOADS_FILTER_DOWNLOADING') },
            { value: 'stopped', label: stoppedCount > 0 ? `${t('SMART_PLAY_DOWNLOADS_FILTER_STOPPED')} (${stoppedCount})` : t('SMART_PLAY_DOWNLOADS_FILTER_STOPPED') },
        ],
        selected: [filter],
        onSelect: (value: string) => setFilter(value as Filter),
    };
    const sortMenu = {
        options: [
            { value: 'size', label: t('SMART_PLAY_DOWNLOADS_SORT_SIZE') },
            { value: 'played', label: t('SMART_PLAY_DOWNLOADS_SORT_PLAYED') },
            { value: 'added', label: t('SMART_PLAY_DOWNLOADS_SORT_ADDED') },
        ],
        value: sort,
        onSelect: (value: string) => setSort(value as Sort),
    };

    const state = (item: Item) => {
        const { record } = item;
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
    const duplicateOf = (item: Item, group: Group) => group.unknown || item.episodeLabel === null ? [] : group.items.filter((other) => other !== item && other.episodeLabel === item.episodeLabel);

    const duplicate = (item: Item, group: Group) => {
        const others = duplicateOf(item, group);
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

    // Moving: pause + delete; stopped: resume + delete; anything else: delete.
    const actions = (item: Item) => {
        const { record } = item;
        const canPause = record.kind === 'kept' && isMoving(item);
        const playing = record.streaming === true;
        return (
            <div className={styles['actions']} onClick={(event) => event.stopPropagation()}>
                {
                    canPause ?
                        <Button className={styles['action']} title={t('SMART_PLAY_DOWNLOADS_PAUSE')} onClick={() => pause(record, true)}>
                            <Icon className={styles['action-icon']} name={'pause'} />
                        </Button>
                        :
                        item.stopped ?
                            <Button className={styles['action']} title={t('SMART_PLAY_DOWNLOADS_RESUME')} onClick={() => resume(record)}>
                                <Icon className={styles['action-icon']} name={'play'} />
                            </Button>
                            :
                            null
                }
                <Button className={classnames(styles['action'], styles['danger'], { [styles['disabled']]: playing })} title={playing ? t('SMART_PLAY_DOWNLOADS_PLAYING_NO_DELETE') : t('SMART_PLAY_DOWNLOADS_DELETE')} onClick={() => !playing && ask([item])}>
                    <Icon className={styles['action-icon']} name={'bin'} />
                </Button>
            </div>
        );
    };

    const row = (item: Item, group: Group) => {
        const checked = selected.has(item.key);
        // An episode by its number; a movie (or any unnumbered file) by its
        // show's name when known, else by its file.
        const label = item.episodeLabel ?? (item.record.meta?.name ?? item.fileName);
        const showBar = item.progress !== null && !item.complete;
        const quality = qualityText(item);
        return (
            <div key={item.key} className={classnames(styles['row'], styles['hoverable'], { [styles['selected']]: checked, [styles['live']]: item.live })} {...pressable(() => toggle([item.key], !checked))}>
                <Own><Checkbox className={styles['check']} name={`select-${item.key}`} checked={checked} onChange={({ checked: on }) => toggle([item.key], on)} /></Own>
                <div className={styles['what']}>
                    <div className={styles['label']}>
                        <span className={styles['episode']}>{label}</span>
                        {item.title !== null ? <span className={styles['title']}>{item.title}</span> : null}
                        {duplicate(item, group)}
                        {item.record.watched ? <span className={styles['watched']}>{t('SMART_PLAY_DOWNLOADS_WATCHED')}</span> : null}
                    </div>
                    <div className={styles['file']} title={item.record.path ?? item.fileName}>
                        {item.fileName}
                    </div>
                </div>
                <div className={styles['quality']} title={quality.length > 0 ? t('SMART_PLAY_DOWNLOADS_QUALITY_HINT') : undefined}>
                    {quality.length > 0 ? <span className={styles['quality-text']}>{quality}</span> : null}
                    {item.bitrate !== null ? <span className={styles['bitrate']}>{mbps(item.bitrate)}</span> : null}
                </div>
                <div className={styles['state-cell']}>
                    {state(item)}
                    {
                        showBar ?
                            <div className={classnames(styles['bar'], { [styles['moving']]: isMoving(item), [styles['flowing']]: item.record.state === 'downloading' })}>
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
                {actions(item)}
            </div>
        );
    };

    // A show's episodes, in a card per season (show > season > episode). A
    // show's unnumbered files (a movie) are listed without one.
    const rows = (g: Group) => {
        const seasons = g.unknown ? [] : Array.from(new Set(g.items.map((item) => item.season)));
        if (seasons.length === 0 || (seasons.length === 1 && seasons[0] === 0)) {
            return <div className={styles['rows']}>{g.items.map((item) => row(item, g))}</div>;
        }
        return (
            <div className={styles['seasons']}>
                {
                    seasons.map((season) => {
                        const list = g.items.filter((item) => item.season === season);
                        const keys = list.map((item) => item.key);
                        const allChecked = keys.every((key) => selected.has(key));
                        const bytes = list.reduce((sum, item) => sum + item.onDisk, 0);
                        const key = `${g.key}#${season}`;
                        const open = !collapsed.has(key);
                        return (
                            <div key={season} className={styles['season']}>
                                <div className={classnames(styles['season-header'], styles['hoverable'])} {...pressable(() => toggleCollapsed(key), open ? t('SMART_PLAY_DOWNLOADS_COLLAPSE') : t('SMART_PLAY_DOWNLOADS_EXPAND'))}>
                                    <Own><Checkbox className={styles['check']} name={`select-season-${key}`} checked={allChecked} onChange={({ checked: on }) => toggle(keys, on)} /></Own>
                                    <div className={styles['season-toggle']}>
                                        <Icon className={classnames(styles['chevron'], { [styles['closed']]: !open })} name={'chevron-down'} />
                                        <span className={styles['season-name']}>{season > 0 ? t('SMART_PLAY_DOWNLOADS_SEASON', { season }) : t('SMART_PLAY_DOWNLOADS_SEASON_OTHER')}</span>
                                        <span className={styles['season-meta']}>{t('SMART_PLAY_DOWNLOADS_FILES', { count: list.length })}</span>
                                        {list.some((item) => item.live) ? <span className={styles['dot']} /> : null}
                                        <span className={styles['season-size']}>{gb(bytes)}</span>
                                    </div>
                                    <div className={styles['slot']} onClick={(event) => event.stopPropagation()}>
                                        <Button className={styles['icon-delete']} title={t('SMART_PLAY_DOWNLOADS_DELETE_SEASON')} onClick={() => ask(list)}>
                                            <Icon className={styles['action-icon']} name={'bin'} />
                                        </Button>
                                    </div>
                                </div>
                                {open ? <div className={styles['season-rows']}>{list.map((item) => row(item, g))}</div> : null}
                            </div>
                        );
                    })
                }
            </div>
        );
    };

    const group = (g: Group) => {
        const keys = g.items.map((item) => item.key);
        const allChecked = keys.every((key) => selected.has(key));
        const open = !collapsed.has(g.key);
        return (
            <section key={g.key} className={styles['group']}>
                <div className={classnames(styles['group-header'], styles['hoverable'])} {...pressable(() => toggleCollapsed(g.key), open ? t('SMART_PLAY_DOWNLOADS_COLLAPSE') : t('SMART_PLAY_DOWNLOADS_EXPAND'))}>
                    <Own><Checkbox className={styles['check']} name={`select-group-${g.key}`} checked={allChecked} onChange={({ checked: on }) => toggle(keys, on)} /></Own>
                    <div className={styles['group-toggle']}>
                        <Icon className={classnames(styles['chevron'], { [styles['closed']]: !open })} name={'chevron-down'} />
                        {
                            g.unknown ?
                                <div className={classnames(styles['poster'], styles['poster-icon'])}><Icon className={styles['poster-glyph']} name={'local-import'} /></div>
                                :
                                <Image className={styles['poster']} src={g.poster ?? undefined} alt={' '} renderFallback={() => <div className={classnames(styles['poster'], styles['poster-icon'])}><Icon className={styles['poster-glyph']} name={'movies'} /></div>} />
                        }
                        <div className={styles['group-text']}>
                            <div className={styles['group-name']} title={g.unknown ? t('SMART_PLAY_DOWNLOADS_UNKNOWN_HINT') : undefined}>{g.unknown ? t('SMART_PLAY_DOWNLOADS_UNKNOWN_GROUP') : g.name}</div>
                            <div className={styles['group-meta']}>
                                {t('SMART_PLAY_DOWNLOADS_FILES', { count: g.items.length })}
                                {g.downloading ? <span className={styles['dot']} /> : null}
                            </div>
                        </div>
                        <div className={styles['group-size']}>{gb(g.onDisk)}</div>
                    </div>
                    <div className={styles['slot']} onClick={(event) => event.stopPropagation()}>
                        <Button className={styles['icon-delete']} title={t('SMART_PLAY_DOWNLOADS_DELETE_ALL')} onClick={() => ask(g.items)}>
                            <Icon className={styles['action-icon']} name={'bin'} />
                        </Button>
                    </div>
                </div>
                {open ? rows(g) : null}
            </section>
        );
    };

    const usedOther = disk !== null && disk.total > 0 ? Math.max(0, disk.total - disk.free - total) : 0;
    const pendingTitle = pending === null ? '' : pending.mode === 'stop' ?
        t('SMART_PLAY_DOWNLOADS_STOP_TITLE', { count: pending.items.length, size: gb(pending.bytes) })
        :
        t('SMART_PLAY_DOWNLOADS_CONFIRM_TITLE', { count: pending.items.length, size: gb(pending.bytes) });

    return (
        <MainNavBars className={styles['downloads-container']} route={'downloads'}>
            <div className={classnames(styles['downloads-content'], 'animation-fade-in')}>
                <div className={styles['header']}>
                    <div className={styles['heading']}>
                        <h1 className={styles['page-title']}>{t('SMART_PLAY_DOWNLOADS')}</h1>
                        <div className={styles['summary']}>
                            {t('SMART_PLAY_DOWNLOADS_SUMMARY', { used: gb(total), count: items.length })}
                            {disk !== null && disk.total > 0 ? ` · ${t('SMART_PLAY_DOWNLOADS_FREE', { free: gb(disk.free), total: gb(disk.total) })}` : null}
                        </div>
                    </div>
                    {
                        disk !== null && disk.total > 0 ?
                            <div className={styles['storage']} title={t('SMART_PLAY_DOWNLOADS_STORAGE_HINT')}>
                                <div className={styles['storage-mine']} style={{ width: `${(total / disk.total) * 100}%` }} />
                                <div className={styles['storage-other']} style={{ width: `${(usedOther / disk.total) * 100}%` }} />
                            </div>
                            :
                            null
                    }
                    {
                        low ?
                            <div className={styles['banner']}>
                                <Icon className={styles['banner-icon']} name={'warning'} />
                                {t('SMART_PLAY_DOWNLOADS_LOW_BANNER', { free: gb(disk!.free) })}
                            </div>
                            :
                            null
                    }
                    <div className={styles['toolbar']}>
                        <Chips {...filterChips} />
                        <div className={styles['quick']}>
                            {
                                watched.length > 0 ?
                                    <Button className={styles['quick-action']} onClick={() => ask(watched)}>
                                        {t('SMART_PLAY_DOWNLOADS_DELETE_WATCHED', { size: gb(watched.reduce((sum, item) => sum + item.onDisk, 0)) })}
                                    </Button>
                                    :
                                    null
                            }
                            <MultiselectMenu className={styles['sort']} {...sortMenu} />
                        </div>
                    </div>
                </div>
                <div className={classnames(styles['list'], { [styles['with-bulk']]: selectedItems.length > 0 })}>
                    {
                        downloads === null ?
                            <div className={styles['empty']}>{t('SMART_PLAY_DOWNLOADS_UNAVAILABLE')}</div>
                            :
                            groups.length === 0 ?
                                <div className={styles['empty']}>
                                    <Icon className={styles['empty-icon']} name={'download'} />
                                    <div>{filter === 'all' ? t('SMART_PLAY_DOWNLOADS_EMPTY') : t('SMART_PLAY_DOWNLOADS_EMPTY_FILTER')}</div>
                                </div>
                                :
                                groups.map(group)
                    }
                </div>
                {
                    selectedItems.length > 0 ?
                        <div className={styles['bulk']}>
                            <div className={styles['bulk-count']}>
                                <span className={styles['bulk-number']}>{t('SMART_PLAY_DOWNLOADS_SELECTED_COUNT', { count: selectedItems.length })}</span>
                                <span className={styles['bulk-size']}>{gb(selectedBytes)}</span>
                            </div>
                            <div className={styles['bulk-actions']}>
                                <Button className={styles['bulk-clear']} onClick={() => setSelected(new Set())}>{t('SMART_PLAY_DOWNLOADS_CLEAR')}</Button>
                                <Button className={styles['bulk-delete']} onClick={() => ask(selectedItems)}>
                                    <Icon className={styles['delete-icon']} name={'bin'} />
                                    <span>{t('SMART_PLAY_DOWNLOADS_DELETE')}</span>
                                </Button>
                            </div>
                        </div>
                        :
                        null
                }
                {
                    pending !== null ?
                        <ModalDialog
                            className={styles['modal']}
                            checkboxes={[]}
                            dataset={{}}
                            background={undefined}
                            title={pendingTitle}
                            buttons={[
                                { className: styles['modal-cancel'], label: t('SMART_PLAY_CANCEL'), props: { onClick: () => setPending(null) } },
                                { className: styles['modal-delete'], label: busy ? t('SMART_PLAY_DOWNLOADS_DELETING') : pending.mode === 'stop' ? t('SMART_PLAY_DOWNLOADS_STOP_AND_DELETE') : t('SMART_PLAY_DOWNLOADS_DELETE'), props: { 'data-autofocus': true, disabled: busy, onClick: confirm } },
                            ]}
                            onCloseRequest={() => !busy && setPending(null)}
                        >
                            <div className={styles['modal-body']}>{pending.mode === 'stop' ? t('SMART_PLAY_DOWNLOADS_STOP_BODY') : t('SMART_PLAY_DOWNLOADS_CONFIRM_BODY')}</div>
                        </ModalDialog>
                        :
                        null
                }
            </div>
        </MainNavBars>
    );
};

export default Downloads;
