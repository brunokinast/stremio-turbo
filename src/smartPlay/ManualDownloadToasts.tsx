// Says how each manual download job ended (manualDownload.ts): what was
// queued, any episode taken below the chosen quality, any that found no
// source. Clicking the toast undoes the job. Mounted in App, so a race that
// outlives its chooser still reports.

import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import useToast from 'stremio/common/Toast/useToast';
import { useDownloads } from './downloads';
import { takeEnded, undoDownload, useManualDownloads } from './manualDownload';
import { episodeRuns } from './seasonPlan';

const ManualDownloadToasts = () => {
    const { t } = useTranslation();
    const toast = useToast() as ReturnType<typeof useToast> & { show: (options: Record<string, unknown>) => void };
    const { baseUrl } = useDownloads(10000, false);
    const jobs = useManualDownloads();
    const ref = useRef({ t, toast, baseUrl });
    ref.current = { t, toast, baseUrl };

    useEffect(() => {
        takeEnded().forEach((job) => {
            const { t: tr, toast: toaster, baseUrl: base } = ref.current;
            const queued = job.episodes.filter((ep) => ep.state === 'queued');
            const failed = job.episodes.filter((ep) => ep.state === 'failed');
            const stepped = queued.filter((ep) => ep.planned.stepped);
            if (queued.length === 0) {
                toaster.show({ type: 'error', title: tr('SMART_PLAY_MANUAL_NONE', { episodes: episodeRuns(failed.map((ep) => ep.planned.episode)) }), timeout: 10000 });
                return;
            }
            const parts = [
                tr('SMART_PLAY_MANUAL_QUEUED', {
                    episodes: episodeRuns(queued.map((ep) => ep.planned.episode)),
                    season: job.season,
                    quality: job.quality,
                    size: (job.bytes / 1e9).toFixed(1),
                }),
                ...stepped.map((ep) => tr('SMART_PLAY_MANUAL_STEPPED', { episode: `E${ep.planned.episode}`, quality: ep.planned.label })),
                failed.length > 0 ? tr('SMART_PLAY_MANUAL_FAILED', { episodes: episodeRuns(failed.map((ep) => ep.planned.episode)) }) : null,
                job.created.length > 0 ? tr('SMART_PLAY_MANUAL_UNDO') : null,
            ].filter((part) => part !== null);
            toaster.show({
                type: 'success',
                title: parts.join(' · '),
                timeout: 12000,
                onSelect: job.created.length > 0 && base !== null ? () => undoDownload(base, job.id) : undefined,
            });
        });
    }, [jobs]);

    return null;
};

export default ManualDownloadToasts;
