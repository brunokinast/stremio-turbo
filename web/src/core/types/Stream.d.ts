
type StreamDeepLinks = {
    player: string,
    externalPlayer: ExternalPlayerLinks,
};

type StreamBehaviorHints = {
    bingeGroup?: string,
    filename?: string,
    videoSize?: number,
};

type Stream = {
    ytId?: string,
    name: string,
    description: string,
    infoHash?: string,
    fileIdx?: number,
    announce?: string[],
    behaviorHints?: StreamBehaviorHints,
    url?: string,
    externalUrl?: string,
    thumbnail?: string | null,
    progress?: number | null,
    lastUsed?: boolean | null,
    deepLinks?: StreamDeepLinks,
};
