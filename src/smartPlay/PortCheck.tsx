// Warns once per session when no peer has connected in to the streaming
// server while torrents have been loaded for a while: its listening port is
// unreachable (no router forward), which on small swarms costs most of the
// speed, since their seeds can often only connect in (findings/30).

import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import useStreamingServer from 'stremio/common/useStreamingServer';
import useToast from 'stremio/common/Toast/useToast';

const POLL = 60 * 1000;
// How long torrents must have been loaded with no incoming connection.
const GRACE = 5 * 60 * 1000;

type Incoming = {
    count: number,
    listenPort: number,
    activeSince: string | null,
};

let warned = false;

const PortCheck = () => {
    const { t } = useTranslation();
    const toast = useToast();
    const baseUrl = useStreamingServer().selected?.transportUrl ?? null;
    const toastRef = useRef(toast);
    toastRef.current = toast;

    useEffect(() => {
        if (baseUrl === null || warned) {
            return;
        }
        const check = () => {
            fetch(new URL('network-info', baseUrl))
                .then((response) => response.ok ? response.json() : null)
                .then((body: { incoming?: Incoming } | null) => {
                    const incoming = body?.incoming;
                    if (warned || incoming === undefined || incoming.count > 0 || incoming.activeSince === null) {
                        return;
                    }
                    if (Date.now() - Date.parse(incoming.activeSince) >= GRACE) {
                        warned = true;
                        toastRef.current.show({ type: 'alert', title: t('SMART_PLAY_PORT_UNREACHABLE', { port: incoming.listenPort }), timeout: 15000 });
                    }
                })
                .catch(() => undefined);
        };
        check();
        const timer = setInterval(check, POLL);
        return () => clearInterval(timer);
    }, [baseUrl, t]);

    return null;
};

export default PortCheck;
