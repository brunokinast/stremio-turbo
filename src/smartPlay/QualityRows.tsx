import React, { useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';
import classNames from 'classnames';
import Icon from '@stremio/stremio-icons/react';
import Button from 'stremio/components/Button';
import ProgressBar from 'stremio/components/ProgressBar';
import StreamCard from 'stremio/routes/Details/Streams/Stream';
import { LISTED_SEEDERS, WEAK_SEEDERS } from './groupByQuality';
import type { RankedTier } from './groupByQuality';
import styles from './QualityRows.less';

type Props = {
    tiers: RankedTier[],
    // Every live count has come in; until then figures may still grow.
    settled: boolean,
    remembered: string | null,
    racing: { tierKey: string, label: string, weak: boolean } | null,
    videoId?: string | null,
    videoReleased?: string | null,
    canMarkWatched: boolean,
    onPlay: (tierKey: string) => void,
    onCancel: () => void,
};

const QualityRows = ({ tiers, settled, remembered, racing, videoId, videoReleased, canMarkWatched, onPlay, onCancel }: Props) => {
    const { t } = useTranslation();
    const [expanded, setExpanded] = useState<string | null>(null);

    // Live count: a dot coloured by health (pulsing while counts are still
    // coming in) and the number; the tooltip spells it out.
    const renderSeeders = useCallback((seeders: number | null) => {
        const weak = seeders !== null && seeders < WEAK_SEEDERS;
        const title = [
            seeders !== null ? t('SMART_PLAY_SEEDERS', { count: seeders }) : t('SMART_PLAY_UNKNOWN'),
            weak ? t('SMART_PLAY_WEAK_HINT') : null,
            settled ? null : t('SMART_PLAY_CHECKING'),
        ].filter((part) => part !== null).join(' · ');
        return (
            <span className={styles['seeders']} title={title}>
                <span className={classNames(styles['dot'], { [styles['weak']]: weak, [styles['unknown']]: seeders === null, [styles['checking']]: !settled })} />
                {seeders !== null ? seeders : '–'}
            </span>
        );
    }, [settled, t]);

    // A source card's description with the addon's (often stale) "👤 n"
    // replaced by our live count, once known.
    const renderDescription = useCallback((description: string, seeders: number | null) => {
        const match = seeders !== null ? LISTED_SEEDERS.exec(description) : null;
        if (match === null) {
            return description;
        }
        return (
            <React.Fragment>
                {description.slice(0, match.index)}
                <span className={styles['inline-seeders']}>{renderSeeders(seeders)}</span>
                {description.slice(match.index + match[0].length)}
            </React.Fragment>
        );
    }, [renderSeeders]);

    if (racing !== null) {
        return (
            <div className={styles['racing']} role={'status'}>
                <div className={styles['racing-label']}>{t('SMART_PLAY_FINDING', { quality: racing.label })}</div>
                {
                    racing.weak ?
                        <div className={styles['racing-warning']}>{t('SMART_PLAY_WEAK_WARNING', { quality: racing.label })}</div>
                        :
                        null
                }
                <div className={styles['racing-line']} />
                <Button className={styles['racing-cancel']} onClick={onCancel}>{t('SMART_PLAY_CANCEL')}</Button>
            </div>
        );
    }

    return (
        <div className={styles['quality-rows']}>
            {
                tiers.map((tier) => (
                    <div key={tier.key} className={styles['tier']}>
                        <div className={styles['row']}>
                            <Button className={styles['play']} title={tier.label} onClick={() => onPlay(tier.key)}>
                                <div className={styles['label']}>{tier.label}</div>
                                {
                                    tier.key === remembered ?
                                        <div className={styles['flag']} title={t('SMART_PLAY_LAST_PLAYED_HINT')}>{t('SMART_PLAY_LAST_PLAYED')}</div>
                                        :
                                        null
                                }
                                {renderSeeders(tier.best)}
                                <div className={styles['play-icon']} aria-hidden={'true'}>
                                    <Icon className={styles['icon']} name={'play'} />
                                </div>
                            </Button>
                            {
                                tier.progress !== null ?
                                    <ProgressBar className={styles['progress']} value={tier.progress} />
                                    :
                                    null
                            }
                            <Button
                                className={styles['expand']}
                                title={t('SMART_PLAY_SOURCES')}
                                aria-expanded={expanded === tier.key}
                                onClick={() => setExpanded(expanded === tier.key ? null : tier.key)}
                            >
                                <Icon className={classNames(styles['icon'], { [styles['open']]: expanded === tier.key })} name={'caret-down'} />
                            </Button>
                        </div>
                        {
                            expanded === tier.key ?
                                <div className={styles['sources']}>
                                    {
                                        tier.sources.map((source) => (
                                            <div key={`${source.infoHash}:${source.fileIdx}`} className={styles['source']}>
                                                <StreamCard
                                                    stream={source.stream}
                                                    renderDescription={(description) => renderDescription(description, source.seeders)}
                                                    addonName={source.addonName}
                                                    videoId={videoId}
                                                    videoReleased={videoReleased}
                                                    canMarkWatched={canMarkWatched}
                                                />
                                            </div>
                                        ))
                                    }
                                </div>
                                :
                                null
                        }
                    </div>
                ))
            }
        </div>
    );
};

export default QualityRows;
