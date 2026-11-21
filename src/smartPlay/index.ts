// Copyright (C) 2017-2026 Smart code 203358507

// Smart play: pick a quality, play its healthiest source (fork feature).

import QualityRows from './QualityRows';
import useSmartPlay, { requestAutoplay, hasRememberedQuality, offersRememberedQuality } from './useSmartPlay';
import useStartBuffer, { START_BUFFER_MS } from './startBuffer';
import { useAheadToggle } from './next';
import useEpisodeDownloads from './episodeDownloads';
import PortCheck from './PortCheck';
import DiskCheck from './DiskCheck';
import ManualDownloadToasts from './ManualDownloadToasts';
import useEpisodeDownload from './useEpisodeDownload';
import SeasonDownload from './SeasonDownload';
import usePlayerSmartPlay from './usePlayerSmartPlay';
import type { Download as EpisodeDownload } from './episodeDownloads';

const START_BUFFER_SECONDS = START_BUFFER_MS / 1000;

export type { EpisodeDownload };
export { PortCheck, DiskCheck, ManualDownloadToasts, useEpisodeDownload, SeasonDownload, usePlayerSmartPlay, useEpisodeDownloads, QualityRows, useSmartPlay, useStartBuffer, useAheadToggle, START_BUFFER_SECONDS, requestAutoplay, hasRememberedQuality, offersRememberedQuality };
