// Smart play: pick a quality, play its healthiest source (fork feature).

import QualityRows from './QualityRows';
import useSmartPlay, { requestAutoplay, hasRememberedQuality, offersRememberedQuality } from './useSmartPlay';
import useStartBuffer, { START_BUFFER_MS } from './startBuffer';
import { useBoardWarmUp, useWarmUpToggle } from './warmup';
import { useDownloadAhead, useAheadToggle } from './ahead';
import useEpisodeDownloads from './episodeDownloads';
import type { Download as EpisodeDownload } from './episodeDownloads';

const START_BUFFER_SECONDS = START_BUFFER_MS / 1000;

export type { EpisodeDownload };
export { useEpisodeDownloads, QualityRows, useSmartPlay, useStartBuffer, useBoardWarmUp, useWarmUpToggle, useDownloadAhead, useAheadToggle, START_BUFFER_SECONDS, requestAutoplay, hasRememberedQuality, offersRememberedQuality };
