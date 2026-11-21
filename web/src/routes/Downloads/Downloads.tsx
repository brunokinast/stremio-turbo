// Copyright (C) 2017-2026 Smart code 203358507

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
import { Button, Chips, MainNavBars, ModalDialog, MultiselectMenu } from 'stremio/components';
import { useDownloads } from 'stremio/smartPlay/downloads';
import { retryElsewhere } from 'stremio/smartPlay/manualDownload';
import { useProfile } from 'stremio/common';
import type { Download } from 'stremio/smartPlay/downloads';
import { cancelAhead } from 'stremio/smartPlay/next';
import { isLowDisk } from 'stremio/smartPlay/DiskCheck';
import { groupDownloads, recordKey } from './groups';
import DownloadRow from './DownloadRow';
import DownloadGroup from './DownloadGroup';
import { isMoving } from './shared';
import useShowInfo from './useShowInfo';
import type { Filter, Group, Item, Sort } from './groups';
import styles from './Downloads.less';
import { size as gb } from 'stremio/smartPlay/format';

const POLL = 3000;

// Shows and seasons the user collapsed, kept while the app runs.
const collapsedMemory = new Set<string>();

// What the confirm dialog deletes; stop: some of it is still downloading.
type Pending = { mode: 'delete' | 'stop', items: Item[], bytes: number } | null;

const Downloads = () => {
    const { t } = useTranslation();
    const { downloads, disk, loaded, remove, pause, resume, baseUrl } = useDownloads(POLL);
    const profile = useProfile();
    // Stalled files whose other-source race is running.
    const [retrying, setRetrying] = useState<Set<string>>(() => new Set());
    const retry = useCallback((record: Download) => {
        const key = `${record.infoHash}:${record.fileIdx}`;
        if (baseUrl === null || retrying.has(key)) {
            return;
        }
        setRetrying((previous) => new Set(previous).add(key));
        retryElsewhere(baseUrl, profile.addons, record).finally(() => setRetrying((previous) => {
            const next = new Set(previous);
            next.delete(key);
            return next;
        }));
    }, [baseUrl, profile.addons, retrying]);
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

    const row = (item: Item, g: Group) => (
        <DownloadRow
            key={item.key}
            item={item}
            group={g}
            downloads={downloads ?? []}
            checked={selected.has(item.key)}
            retrying={retrying.has(`${item.record.infoHash}:${item.record.fileIdx}`)}
            onToggle={(on) => toggle([item.key], on)}
            onPause={(record) => pause(record, true)}
            onResume={resume}
            onRetry={retry}
            onDelete={(one) => ask([one])}
        />
    );
    const group = (g: Group) => (
        <DownloadGroup key={g.key} group={g} selected={selected} collapsed={collapsed} renderRow={row} onToggle={toggle} onToggleCollapsed={toggleCollapsed} onDelete={ask} />
    );

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
                            <React.Fragment>
                                <div className={styles['storage']}>
                                    <div className={styles['storage-mine']} style={{ width: `${(total / disk.total) * 100}%` }} />
                                    <div className={styles['storage-other']} style={{ width: `${(usedOther / disk.total) * 100}%` }} />
                                </div>
                                <div className={styles['legend']}>
                                    <span className={styles['legend-item']}><span className={classnames(styles['key'], styles['storage-mine'])} />{t('SMART_PLAY_DOWNLOADS_LEGEND_MINE', { size: gb(total) })}</span>
                                    <span className={styles['legend-item']}><span className={classnames(styles['key'], styles['storage-other'])} />{t('SMART_PLAY_DOWNLOADS_LEGEND_OTHER', { size: gb(usedOther) })}</span>
                                    <span className={styles['legend-item']}><span className={styles['key']} />{t('SMART_PLAY_DOWNLOADS_LEGEND_FREE', { size: gb(disk.free) })}</span>
                                </div>
                            </React.Fragment>
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
                            <div className={styles['empty']}>{loaded ? t('SMART_PLAY_DOWNLOADS_UNAVAILABLE') : t('SMART_PLAY_DOWNLOADS_LOADING')}</div>
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
                                { className: styles['modal-cancel'], label: t('SMART_PLAY_CANCEL'), props: { 'data-autofocus': true, onClick: () => setPending(null) } },
                                { className: styles['modal-delete'], label: busy ? t('SMART_PLAY_DOWNLOADS_DELETING') : pending.mode === 'stop' ? t('SMART_PLAY_DOWNLOADS_STOP_AND_DELETE') : t('SMART_PLAY_DOWNLOADS_DELETE'), props: { disabled: busy, onClick: confirm } },
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
