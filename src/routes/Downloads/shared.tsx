// Copyright (C) 2017-2026 Smart code 203358507

// Pieces the Downloads screen's rows and headers share.

import React from 'react';
import type { Item } from './groups';
import styles from './Downloads.less';

// Controls inside a clickable row or header (checkbox, buttons) keep their
// click to themselves.
const Own = ({ children }: { children: React.ReactNode }) => (
    <div className={styles['own']} onClick={(event) => event.stopPropagation()}>{children}</div>
);

// A whole header or row as one control: click, Enter or Space. (Not the
// common Button: its Enter clicks the row even from a nested checkbox or
// button, and it blurs on mouse down.)
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

const isMoving = (item: Item) => item.record.state === 'downloading' || item.record.state === 'queued';

export { Own, pressable, isMoving };
