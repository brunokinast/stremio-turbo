import React, { forwardRef, useMemo } from 'react';
import classNames from 'classnames';
import { Image } from 'stremio/components';
import styles from './Buffering.less';

type Props = {
    className: string,
    logo: string,
    progress: number,
    // Why playback waits, shown under the logo (fork: smart play's start buffer).
    message?: string | null,
};

const Buffering = forwardRef<HTMLDivElement, Props>(({ className, logo, progress, message }, ref) => {
    const style = useMemo(() => {
        return {
            clipPath: `inset(0 ${100 - progress}% 0 0)`,
        };
    }, [progress]);

    return (
        <div ref={ref} className={classNames(className, styles['buffering'])}>
            <Image
                className={styles['logo']}
                style={style}
                src={logo}
                alt={' '}
                fallbackSrc={require('/assets/images/stremio_symbol.png')}
            />
            <Image
                className={classNames(styles['logo'], styles['background'])}
                src={logo}
                alt={' '}
                fallbackSrc={require('/assets/images/stremio_symbol.png')}
            />
            {
                message ?
                    <div className={styles['message']}>{message}</div>
                    :
                    null
            }
        </div>
    );
});

export default Buffering;
