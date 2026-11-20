// Once per app start: warns when the disk holding the server's data is low.
// The toast opens the Downloads screen, the only place data is deleted.

import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import useStreamingServer from 'stremio/common/useStreamingServer';
import useToast from 'stremio/common/Toast/useToast';
import { getDisk } from './downloads';
import type { DiskInfo } from './downloads';

// Low: under 10% of the volume or under 20 GB free.
const LOW_SHARE = 0.1;
const LOW_BYTES = 20e9;
// The old "data found" notice's memory (that data is now listed as stopped
// files): removed once.
const OLD_SEEN_KEY = 'smartPlay.leftoversSeen';

const isLowDisk = (disk: DiskInfo) => disk.total > 0 && (disk.free < LOW_SHARE * disk.total || disk.free < LOW_BYTES);

const gb = (bytes: number) => (bytes / 1e9).toFixed(1);

let checked = false;

const DiskCheck = () => {
    const { t } = useTranslation();
    const toast = useToast() as ReturnType<typeof useToast> & { show: (options: Record<string, unknown>) => void };
    const baseUrl = useStreamingServer().selected?.transportUrl ?? null;
    const ref = useRef({ toast, t });
    ref.current = { toast, t };

    useEffect(() => {
        if (baseUrl === null || checked) {
            return;
        }
        checked = true;
        try {
            localStorage.removeItem(OLD_SEEN_KEY);
        } catch (_e) {
            // Storage unavailable: nothing to clean.
        }
        // Mounted in App beside the routes: useNavigate there re-rendered the
        // whole tree in a loop (measured), so the toast sets the hash (the app
        // uses a hash router).
        const open = () => {
            window.location.hash = '#/downloads';
        };
        getDisk(baseUrl).then((disk) => {
            if (disk !== null && isLowDisk(disk)) {
                ref.current.toast.show({ type: 'alert', title: ref.current.t('SMART_PLAY_DOWNLOADS_LOW_TOAST', { free: gb(disk.free) }), timeout: 20000, onSelect: open });
            }
        });
    }, [baseUrl]);

    return null;
};

export { isLowDisk };
export default DiskCheck;
