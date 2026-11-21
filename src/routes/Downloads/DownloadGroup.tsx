// Copyright (C) 2017-2026 Smart code 203358507

// A show on the Downloads screen: its header, then its episodes in a card per
// season (show > season > episode). A show's unnumbered files (a movie) are
// listed without one.

import React from 'react';
import { useTranslation } from 'react-i18next';
import classnames from 'classnames';
import Icon from '@stremio/stremio-icons/react';
import { Button, Checkbox, Image } from 'stremio/components';
import { size as gb } from 'stremio/smartPlay/format';
import type { Group, Item } from './groups';
import { Own, pressable } from './shared';
import styles from './Downloads.less';

type Props = {
    group: Group,
    selected: ReadonlySet<string>,
    collapsed: ReadonlySet<string>,
    renderRow: (item: Item, group: Group) => React.ReactNode,
    onToggle: (keys: string[], on: boolean) => void,
    onToggleCollapsed: (key: string) => void,
    onDelete: (items: Item[]) => void,
};

const DownloadGroup = ({ group: g, selected, collapsed, renderRow, onToggle, onToggleCollapsed, onDelete }: Props) => {
    const { t } = useTranslation();

    const rows = () => {
        const seasons = g.unknown ? [] : Array.from(new Set(g.items.map((item) => item.season)));
        if (seasons.length === 0 || (seasons.length === 1 && seasons[0] === 0)) {
            return <div className={styles['rows']}>{g.items.map((item) => renderRow(item, g))}</div>;
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
                                <div className={classnames(styles['season-header'], styles['hoverable'])} {...pressable(() => onToggleCollapsed(key), open ? t('SMART_PLAY_DOWNLOADS_COLLAPSE') : t('SMART_PLAY_DOWNLOADS_EXPAND'))}>
                                    <Own><Checkbox className={styles['check']} name={`select-season-${key}`} checked={allChecked} onChange={({ checked: on }) => onToggle(keys, on)} /></Own>
                                    <div className={styles['season-toggle']}>
                                        <Icon className={classnames(styles['chevron'], { [styles['closed']]: !open })} name={'chevron-down'} />
                                        <span className={styles['season-name']}>{season > 0 ? t('SMART_PLAY_DOWNLOADS_SEASON', { season }) : t('SMART_PLAY_DOWNLOADS_SEASON_OTHER')}</span>
                                        <span className={styles['season-meta']}>{t('SMART_PLAY_DOWNLOADS_FILES', { count: list.length })}</span>
                                        {list.some((item) => item.live) ? <span className={styles['dot']} /> : null}
                                        <span className={styles['season-size']}>{gb(bytes)}</span>
                                    </div>
                                    <div className={styles['slot']} onClick={(event) => event.stopPropagation()}>
                                        <Button className={styles['icon-delete']} title={t('SMART_PLAY_DOWNLOADS_DELETE_SEASON')} onClick={() => onDelete(list)}>
                                            <Icon className={styles['action-icon']} name={'bin'} />
                                        </Button>
                                    </div>
                                </div>
                                {open ? <div className={styles['season-rows']}>{list.map((item) => renderRow(item, g))}</div> : null}
                            </div>
                        );
                    })
                }
            </div>
        );
    };

    const keys = g.items.map((item) => item.key);
    const allChecked = keys.every((key) => selected.has(key));
    const open = !collapsed.has(g.key);
    return (
        <section className={styles['group']}>
            <div className={classnames(styles['group-header'], styles['hoverable'])} {...pressable(() => onToggleCollapsed(g.key), open ? t('SMART_PLAY_DOWNLOADS_COLLAPSE') : t('SMART_PLAY_DOWNLOADS_EXPAND'))}>
                <Own><Checkbox className={styles['check']} name={`select-group-${g.key}`} checked={allChecked} onChange={({ checked: on }) => onToggle(keys, on)} /></Own>
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
                    <Button className={styles['icon-delete']} title={t('SMART_PLAY_DOWNLOADS_DELETE_ALL')} onClick={() => onDelete(g.items)}>
                        <Icon className={styles['action-icon']} name={'bin'} />
                    </Button>
                </div>
            </div>
            {open ? rows() : null}
        </section>
    );
};

export default DownloadGroup;
