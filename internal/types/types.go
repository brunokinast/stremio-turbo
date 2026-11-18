// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

// Package types holds the shared contract between the engine, api, settings and
// media packages. Everything stremio-web depends on is mirrored here so the
// individual packages can be developed independently against a stable surface.
package types

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
)

// Config is the runtime configuration shared by all subsystems.
type Config struct {
	HTTPPort        int    // enginefs HTTP API port (default 11470)
	HTTPSPort       int    // optional HTTPS port (default 12470, 0 = disabled)
	AppPath         string // application/cache root, e.g. ~/.rivulet-engine
	CacheRoot       string // torrent piece cache root (defaults to AppPath)
	MemoryCacheSize int64  // opt-in in-RAM piece cache budget in bytes; 0 = disabled (write pieces to disk)
	// StorageMmap memory-maps torrent files in the disk piece cache instead of
	// classic pread/pwrite (TORRENT_STORAGE_DEFAULT_FILE_IO=mmap). Off by
	// default: mapped pages count as process RSS for the whole streamed file.
	StorageMmap bool
	ListenPort  int    // BitTorrent peer listen port (0 = random)
	WebUI       string // redirect target for "GET /" (e.g. https://web.stremio.com/)
	// PublicURL is this server's externally reachable base URL behind a
	// reverse proxy (RIVULET_PUBLIC_URL), e.g. https://stremio.example.com.
	// When set it is used verbatim for the GET / landing redirect and the
	// /settings baseUrl instead of deriving them from r.TLS / the first local
	// interface, which are wrong when TLS terminates at an edge proxy. It is
	// also the fallback for ProxyPublicURL when that is unset (but never for
	// LocalFilesPublicURL, which must be opted into explicitly).
	PublicURL   string
	Version     string // value reported as settings.serverVersion
	TrackersMax int    // max ranked UDP/HTTP trackers per torrent (RIVULET_TRACKERS_MAX; 0 = default)
	// remote tracker list source (RIVULET_TRACKERS_URL); "" disables the remote
	// fetch (embedded/cached list + DHT/PEX only). Default: a curated public list.
	TrackersURL string
	// disable ALL BitTorrent tracker announces (DHT/PEX/webseeds still used); RIVULET_DISABLE_TRACKERS; default false
	DisableTrackers bool
	// disable WebTorrent/WebRTC peers (pion); cuts ~60% of goroutines + RAM; RIVULET_DISABLE_WEBTORRENT; default true (disabled)
	DisableWebtorrent bool
	// established peer connections per torrent (scales half-open=n/2, high-water=n*10); RIVULET_PEERS_PER_TORRENT; 0 = default 50
	PeersPerTorrent int
	// outgoing connection attempts per second, client-wide (anacrolix DialRateLimiter, burst equal);
	// RIVULET_DIAL_RATE; default 30 (libtorrent's connection_speed); 0 keeps anacrolix's 10
	DialRate int
	// enable DLNA/UPnP casting discovery + control on /casting; RIVULET_ENABLE_DLNA; default false (disabled)
	EnableDLNA bool

	// Stream proxy configuration.
	ProxyPassword    string // api_password for stream proxy; "" = no auth
	ProxySecret      string // hex-encoded 32-byte key for signed-URL tokens
	ProxyIPACL       string // comma-separated CIDR allowlist for proxy clients
	ProxyPrebuffer   int    // number of segments to prefetch (0 = off)
	ProxySegCacheTTL int    // segment cache TTL in seconds (0 = caching off)
	ProxyPublicURL   string // explicit external base URL for proxy; "" = derive
	ProxyUpstream    string // global upstream proxy for stream-proxy fetches; "" = direct (RIVULET_PROXY_UPSTREAM; socks5/http)
	// ProxyPrivateAllow is a comma-separated list of IPs, CIDRs and hostnames the
	// stream proxy may reach even when private destinations are blocked
	// (RIVULET_PROXY_PRIVATE_ALLOW); cloud-metadata is never allowed.
	ProxyPrivateAllow string

	// Declarative /extractor definitions.
	ExtractorsFile   string // local definitions file; "" = <AppPath>/extractors.json (RIVULET_EXTRACTORS_FILE)
	ExtractorsURL    string // remote definitions list; "" disables (RIVULET_EXTRACTORS_URL)
	ExtractorsPubKey string // ed25519 public key (hex/base64) the remote list must be signed with (RIVULET_EXTRACTORS_PUBKEY)

	// Bitmagnet integration (self-hosted DHT index).
	BitmagnetURL string // GraphQL endpoint, e.g. http://localhost:3333/graphql; "" disables stream queries

	// Torznab integration (Prowlarr/Jackett/NZBHydra/Bitmagnet /torznab).
	TorznabURL    string // Torznab endpoint base URL; "" disables stream queries
	TorznabAPIKey string // Optional API key appended as &apikey=; "" = no auth

	// IPTV add-on (/iptv): M3U playlist URLs, one catalog each
	// (RIVULET_IPTV_PLAYLISTS, comma-separated). Empty disables the add-on.
	IPTVPlaylists []string
	// XMLTV programme guides for the /iptv add-on (RIVULET_IPTV_EPG,
	// comma-separated, plain or gzip). Empty = no guide: the manifest then has
	// no epgProvider hint and no guide catalogs.
	IPTVEPG []string

	// Cinemeta-compatible metadata addon base URL used by the /bitmagnet and
	// /torznab add-ons to resolve an IMDB id to a title (RIVULET_METADATA_URL).
	// Default https://v3-cinemeta.strem.io; "" disables the lookup. Accepts any
	// addon speaking /meta/{type}/{id}.json (Cinemeta, a self-hosted mirror, or
	// a TMDB/aiometadata addon's configured base).
	MetadataURL string

	// LocalIMDB enables the local-files add-on's IMDB title->id resolution
	// (queries IMDb's suggestion API; RIVULET_LOCAL_IMDB, default true). When
	// false, local files keep filename-parsed titles and local: ids — no
	// external IMDb call is made.
	LocalIMDB bool
	// LocalFilesPublicURL is the externally reachable base URL used for local-file
	// streams (RIVULET_LOCAL_FILES_PUBLIC_URL). When empty, the local add-on keeps
	// returning file:// URLs for backwards-compatible same-host playback.
	LocalFilesPublicURL string

	// Censorship resistance / anonymity.
	// BitTorrent peer encryption policy (RIVULET_BT_ENCRYPTION): "prefer" (default; encrypt if peer supports, else
	// plaintext), "require" (refuse plaintext — DPI evasion), "disable" (no obfuscation).
	BTEncryption string
	// upstream proxy for BitTorrent tracker announces, HTTP webseeds, metainfo + tracker-list fetch
	// (RIVULET_BT_PROXY; socks5://host:port or http(s)://host:port). "" = direct.
	// NOTE: peer connections are not proxied (anacrolix uses listen-bound sockets for peer dials).
	BTProxy string
	// extra DHT bootstrap nodes appended to defaults (RIVULET_DHT_BOOTSTRAP; comma-separated host:port). "" = defaults only.
	DHTBootstrap string
	// file the DHT routing table is saved to and loaded from across restarts
	// (RIVULET_DHT_NODES_FILE; default <APP_PATH>/dht-nodes.dat, "off" disables).
	DHTNodesFile string
	// hide client version/fingerprint from peers (RIVULET_BT_ANONYMOUS; cc.AnonymousMode). default false.
	BTAnonymous bool
	// IdleTimeout removes a torrent that has had no open stream readers and no
	// access for this long (RIVULET_TORRENT_IDLE_TIMEOUT, seconds; default 300,
	// 0 = disabled). Matches the official server's inactive-torrent reclaim so a
	// stopped torrent is dropped even when cacheSize is unlimited, while staying
	// alive long enough for instant scrub/resume/next-episode.
	IdleTimeout time.Duration
	// MaxSeedRatio stops a torrent from uploading once its share ratio
	// (bytes uploaded / bytes downloaded) reaches this value
	// (RIVULET_MAX_SEED_RATIO; default 0 = unlimited). Enforcement is
	// deliberately independent of IdleTimeout: reaching the ratio only pauses
	// uploading, it never drops the torrent or purges its cache, so seeding can
	// be capped without shortening how long a torrent stays available for
	// instant scrub/resume. Uploading resumes automatically if the ratio falls
	// back below the cap (e.g. more data is downloaded, or the cap is raised).
	MaxSeedRatio float64

	// HTTPLog enables a structured access-log line per request (method, uri,
	// status, duration_ms, bytes, remote); RIVULET_HTTP_LOG, default false.
	HTTPLog bool
	// AllowedOrigins lists extra origins the HTTP API accepts beyond the
	// built-in Stremio-web/localhost/own-IP allowlist (RIVULET_ALLOWED_ORIGINS,
	// comma-separated). Entries are either an exact "scheme://host[:port]" /
	// "host[:port]" origin, or a "*.domain" host wildcard. Ignored when
	// AllowAllOrigins is true.
	AllowedOrigins []string
	// AllowAllOrigins restores the legacy no-Origin-check behavior (every
	// response gets Access-Control-Allow-Origin: *) when
	// RIVULET_ALLOWED_ORIGINS is exactly "*". Default false.
	AllowAllOrigins bool

	// CreateMetadataWait bounds how long /create, /{infoHash}/create, and
	// /{infoHash}/{fileIdx} wait for a newly-added torrent's metadata
	// (Engine.Ready) before returning 504. RIVULET_CREATE_METADATA_TIMEOUT;
	// default 90s. Slow-starting torrents/debrid links may need a longer
	// window than the default.
	CreateMetadataWait time.Duration

	// SelfURL is a loopback base URL (e.g. http://127.0.0.1:11470) on which
	// this process is reachable by its own child processes (ffmpeg). Set by
	// the app after BIND_ADDRESS is resolved; "" = derive from HTTPPort.
	SelfURL string

	// DVR (MediaFlow/EasyProxy-compatible /record + /api/recordings), opt-in.
	// DVREnabled is RIVULET_DVR_ENABLED (default false). DVRDir is
	// RIVULET_DVR_DIR ("" = <AppPath>/recordings). DVRDefaultDuration and
	// DVRMaxDuration (RIVULET_DVR_DEFAULT_DURATION / _MAX_DURATION) bound one
	// recording; DVRMaxActive (RIVULET_DVR_MAX_ACTIVE) caps concurrent ffmpeg
	// recorders; DVRMaxBytes (RIVULET_DVR_MAX_BYTES, 0 = unlimited) caps the
	// total size of the recordings directory; DVRRetentionDays
	// (RIVULET_DVR_RETENTION_DAYS, 0 = keep forever) ages out finished ones.
	DVREnabled         bool
	DVRDir             string
	DVRDefaultDuration time.Duration
	DVRMaxDuration     time.Duration
	DVRMaxActive       int
	DVRMaxBytes        int64
	DVRRetentionDays   int
}

// FileInfo mirrors an entry of stats.files as consumed by stremio-web.
type FileInfo struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Length int64  `json:"length"`
	Offset int64  `json:"offset"`
}

// Wire is one connected, unchoked peer (stats.wires[]).
type Wire struct {
	Requests     int     `json:"requests"`
	Address      string  `json:"address"`
	AmInterested bool    `json:"amInterested"`
	IsSeeder     bool    `json:"isSeeder"`
	DownSpeed    float64 `json:"downSpeed"`
	UpSpeed      float64 `json:"upSpeed"`
}

// Source is one stats.sources entry (tracker announcement).
// All fields are required (non-null) to match stremio-core's Source shape, which
// the strict (deny-missing) Statistics deserializer rejects if any are absent.
// URL carries the "tracker:" prefix the official server uses, e.g.
// "tracker:udp://host:1337/announce"; stremio-core parses it as a Url.
// NumRequests is a documented constant because anacrolix exposes no
// per-tracker announce count.
type Source struct {
	LastStarted  string `json:"lastStarted"`
	URL          string `json:"url"`
	NumFound     int    `json:"numFound"`
	NumFoundUniq int    `json:"numFoundUniq"`
	NumRequests  int    `json:"numRequests"`
}

// Options mirrors stats.opts (the torrent engine options object) consumed by
// stremio-core's strict Statistics.opts deserializer. Optional numeric fields
// are pointers so they marshal as JSON null. A typed struct (vs map[string]any)
// avoids reflect map-encoding + key sorting on every stats poll.
type Options struct {
	Connections      *int       `json:"connections"`
	DHT              bool       `json:"dht"`
	Growler          Growler    `json:"growler"`
	HandshakeTimeout *int       `json:"handshakeTimeout"`
	Path             string     `json:"path"`
	PeerSearch       PeerSearch `json:"peerSearch"`
	SwarmCap         SwarmCap   `json:"swarmCap"`
	Timeout          *int       `json:"timeout"`
	Tracker          bool       `json:"tracker"`
	Virtual          bool       `json:"virtual"`
}

// Growler is stats.opts.growler.
type Growler struct {
	Flood int  `json:"flood"`
	Pulse *int `json:"pulse"`
}

// PeerSearch is stats.opts.peerSearch.
type PeerSearch struct {
	Min     int      `json:"min"`
	Max     int      `json:"max"`
	Sources []string `json:"sources"`
}

// SwarmCap is stats.opts.swarmCap.
type SwarmCap struct {
	MaxSpeed *float64 `json:"maxSpeed"`
	MinPeers *int     `json:"minPeers"`
}

// Stats mirrors getStatistics(engine[, idx]) from the original server.js.
// Field names and JSON tags MUST match exactly. Per-file (idx>=0) fields are
// pointers so they are omitted at torrent level.
type Stats struct {
	InfoHash         string `json:"infoHash"`
	Name             string `json:"name"`
	Peers            int    `json:"peers"`
	Unchoked         int    `json:"unchoked"`
	Queued           int    `json:"queued"`
	Unique           int    `json:"unique"`
	ConnectionTries  int    `json:"connectionTries"`
	SwarmPaused      bool   `json:"swarmPaused"`
	SwarmConnections int    `json:"swarmConnections"`
	SwarmSize        int    `json:"swarmSize"`
	// Selections holds piece-selection ranges (null or an array). The element
	// shape is anacrolix-internal and never populated by the engine today, so
	// []any preserves the null/empty-array wire semantics without constraining
	// the unknown element type.
	Selections    []any      `json:"selections"`
	Wires         []Wire     `json:"wires"`
	Files         []FileInfo `json:"files"`
	Downloaded    int64      `json:"downloaded"`
	Uploaded      int64      `json:"uploaded"`
	DownloadSpeed float64    `json:"downloadSpeed"`
	UploadSpeed   float64    `json:"uploadSpeed"`
	Sources       []Source   `json:"sources"`
	Opts          Options    `json:"opts"`
	// PeerSearchRunning is REQUIRED (non-null) by stremio-core's Statistics.
	PeerSearchRunning bool `json:"peerSearchRunning"`

	// Per-file extras (only when a file index is requested).
	StreamLen      *int64   `json:"streamLen,omitempty"`
	StreamName     *string  `json:"streamName,omitempty"`
	StreamProgress *float64 `json:"streamProgress,omitempty"`

	// GuessedFileIdx is set only in the /create response when the client asked
	// the server to guess (guessFileIdx) or a fileMustInclude matched.
	GuessedFileIdx *int `json:"guessedFileIdx,omitempty"`
}

// AddOptions carries everything needed to add/locate a torrent. It is derived
// by the api layer from the create body and/or the streaming URL query string.
type AddOptions struct {
	// MetaInfo holds raw .torrent bytes when available (from /create from=..).
	MetaInfo []byte
	// Torrent is the raw "torrent" object from a /:infoHash/create body, if any
	// (stremio-web sends parsed torrent metadata: {infoHash, announce, files...}).
	Torrent json.RawMessage
	// Trackers / Sources are announce URLs (?tr=) or peerSearch sources
	// ("tracker:<url>", "dht:<ih>"). The engine should extract real tracker URLs.
	Trackers []string
	Sources  []string
}

// Engine is a single torrent handle.
type Engine interface {
	InfoHash() string
	// Ready blocks until metadata (file list) is available, the context is
	// cancelled, or the engine errors.
	Ready(ctx context.Context) error
	Files() []FileInfo
	// Stats returns torrent-level stats when idx < 0, otherwise augmented with
	// streamLen/streamName/streamProgress for that file.
	Stats(idx int) *Stats
	// NewReader returns a streaming reader for file idx plus the file length.
	// The reader MUST support Seek (for HTTP Range) and prioritise pieces near
	// the current read offset.
	NewReader(idx int) (io.ReadSeekCloser, int64, error)
	// NewProbeReader is NewReader for a client measuring the source before its
	// player opens it (GET ?probe=1): the file stays prepared for the player.
	NewProbeReader(idx int) (io.ReadSeekCloser, int64, error)
	// GuessFileIdx returns the index of the most likely playable file
	// (largest video file), or -1 when none.
	GuessFileIdx() int
	// PrimeFile names the file that will be played, so its boundary (head and
	// tail) pieces are prioritized instead of the largest file's (POST
	// /:infoHash/create with fileIdx, or a fileMustInclude match). A
	// startFraction in (0,1) is where playback will start (a resume): the
	// pieces around it are prioritized too.
	PrimeFile(idx int, startFraction float64)
	// Peers lists the torrent's live peer connections (GET /:infoHash/peers).
	Peers() []PeerConn
}

// DialRecord is one entry of a torrent's connection log (GET
// /debug/dials/:infoHash).
type DialRecord struct {
	Address string `json:"address"`
	// Source is how the peer was found (anacrolix code, e.g. "Hg" DHT, "Tr"
	// tracker, "X" PEX, "I" incoming).
	Source string `json:"source"`
	// Network is the transport that connected (e.g. "tcp4", "utp6"); empty
	// for a failed attempt.
	Network  string `json:"network"`
	Outgoing bool   `json:"outgoing"`
	// StartedMs is when the attempt started, in ms since the torrent was added.
	StartedMs  int64 `json:"startedMs"`
	DurationMs int64 `json:"durationMs"`
	// WaitMs is the part of DurationMs spent waiting for the client-wide dial
	// rate limit before dialing (RIVULET_DIAL_RATE).
	WaitMs int64  `json:"waitMs"`
	Result string `json:"result"` // DialConnected, DialRefused, DialTimeout, DialHandshake or DialOther
	Error  string `json:"error,omitempty"`
}

// Trace is a torrent's diagnostic record (GET /debug/trace/:infoHash): what
// happened during a play, second by second, so a slow start or a stall can be
// explained from one capture. Times are ms since the torrent was added
// (Created); each list is bounded and keeps the newest entries.
type Trace struct {
	Created     time.Time      `json:"created"`
	Events      []TraceEvent   `json:"events"`
	Stalls      []ReadStall    `json:"stalls"`
	Requests    []TraceRequest `json:"requests"`
	PeerSamples []PeerSample   `json:"peerSamples"`
	Storage     StorageTrace   `json:"storage"`
	// Start-up instruments: every read wait with what held up its chunk, critical requests moved
	// between peers, the progress of pieces stream reads were blocked on, useful bytes per file
	// per second, and reads' waits for in-flight piece writes.
	ReaderWaits       []ReaderWait         `json:"readerWaits"`
	CriticalSteals    []CriticalSteal      `json:"criticalSteals"`
	BlockedPieces     []BlockedPieceSample `json:"blockedPieces"`
	FileBytes         []FileBytes          `json:"fileBytes"`
	PendingWriteWaits PendingWriteWaits    `json:"pendingWriteWaits"`
}

// ReaderWait is a read that waited at least 20 ms for data, with the state of
// the chunk it waited for when the wait began. Estimates are -1 when unknown.
type ReaderWait struct {
	AtMs      int64   `json:"atMs"`
	File      int     `json:"file"`
	Offset    int64   `json:"offset"`
	WaitedMs  float64 `json:"waitedMs"`
	Requested bool    `json:"requested"`
	// The peer the chunk was requested from, how long before, how many of its
	// requests were ahead, its rate (KiB/s) and its expected delivery.
	Holder           string  `json:"holder,omitempty"`
	HolderNetwork    string  `json:"holderNetwork,omitempty"`
	RequestedAgoMs   float64 `json:"requestedAgoMs"`
	Ahead            int     `json:"ahead"`
	HolderRateKiBs   float64 `json:"holderRateKiBs"`
	HolderEstimateMs float64 `json:"holderEstimateMs"`
	// The other peer expected to deliver a new request soonest.
	BestOther           string  `json:"bestOther,omitempty"`
	BestOtherNetwork    string  `json:"bestOtherNetwork,omitempty"`
	BestOtherEstimateMs float64 `json:"bestOtherEstimateMs"`
}

// CriticalSteal is a request a blocked reader needed, moved to a peer expected
// to deliver it sooner.
type CriticalSteal struct {
	AtMs           int64   `json:"atMs"`
	From           string  `json:"from"`
	To             string  `json:"to"`
	FromEstimateMs float64 `json:"fromEstimateMs"`
	ToEstimateMs   float64 `json:"toEstimateMs"`
}

// BlockedPieceSample is a once-a-second snapshot of a piece a stream read has
// been waiting on for at least 250 ms.
type BlockedPieceSample struct {
	AtMs         int64 `json:"atMs"`
	File         int   `json:"file"`
	Piece        int   `json:"piece"`
	BlockedMs    int64 `json:"blockedMs"`
	Chunks       int   `json:"chunks"`
	Have         int   `json:"have"`
	Outstanding  int   `json:"outstanding"`
	Requesters   int   `json:"requesters"`
	Availability int   `json:"availability"`
	Priority     int   `json:"priority"`
}

// FileBytes is the useful bytes received for one file in each second since the
// trace began.
type FileBytes struct {
	File      int     `json:"file"`
	PerSecond []int64 `json:"perSecond"`
}

// PendingWriteWaits is the time reads waited for a piece's in-flight writes.
type PendingWriteWaits struct {
	Count   int64   `json:"count"`
	TotalMs float64 `json:"totalMs"`
	MaxMs   float64 `json:"maxMs"`
}

// TraceEvent is a milestone such as "metadata", "first-unchoke" or
// "first-byte-read"; each is recorded once per torrent.
type TraceEvent struct {
	AtMs   int64  `json:"atMs"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

// ReadStall is a stream read that waited at least 250 ms for data: the
// server-side definition of buffering. First marks the reader's first read
// (time to first byte), which is recorded whatever its length.
type ReadStall struct {
	AtMs       int64  `json:"atMs"`
	DurationMs int64  `json:"durationMs"`
	File       int    `json:"file"`
	Offset     int64  `json:"offset"`
	Piece      int    `json:"piece"`
	Request    uint64 `json:"request,omitempty"`
	First      bool   `json:"first,omitempty"`
}

// TraceRequest is one HTTP request under the torrent's routes (stream,
// create, remove, subtitles), for seeing exactly what a player and the
// server's own helpers read and how long each waited for its first byte.
type TraceRequest struct {
	ID        uint64 `json:"id"`
	AtMs      int64  `json:"atMs"`
	Class     string `json:"class"`
	Method    string `json:"method"`
	Range     string `json:"range,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
	Status    int    `json:"status"`
	// FirstByteMs is the delay to the first body byte; -1 if none was sent.
	FirstByteMs int64 `json:"firstByteMs"`
	Bytes       int64 `json:"bytes"`
	DurationMs  int64 `json:"durationMs"`
	// End is "done", "client-closed" or "error".
	End string `json:"end"`
}

// PeerSample is one peer connection's state, sampled once a second while a
// stream reader is open.
type PeerSample struct {
	AtMs           int64      `json:"atMs"`
	Address        string     `json:"address"`
	Client         string     `json:"client,omitempty"`
	Network        string     `json:"network"`
	Source         string     `json:"source"`
	Outgoing       bool       `json:"outgoing"`
	Downloaded     int64      `json:"downloaded"`
	Pieces         int        `json:"pieces"`
	Outstanding    int        `json:"outstanding"`
	Cancelled      int        `json:"cancelled"`
	MaxRequests    int        `json:"maxRequests"`
	Interested     bool       `json:"interested"`
	PeerChoking    bool       `json:"peerChoking"`
	PeerInterested bool       `json:"peerInterested"`
	Choking        bool       `json:"choking"`
	Snubbed        bool       `json:"snubbed"`
	QueueMs        int64      `json:"queueMs"` // the queue time the request limit is sized to
	RTTMs          int64      `json:"rttMs"`   // round-trip estimate, 0 until known
	UTP            *UTPSample `json:"utp,omitempty"`
}

// UTPSample is a uTP connection's transport state (cumulative counters).
type UTPSample struct {
	RTTMs                  int64  `json:"rttMs"`
	RTOMs                  int64  `json:"rtoMs"`
	InOrder                uint64 `json:"inOrder"`
	Duplicates             uint64 `json:"duplicates"`
	ReorderInserts         uint64 `json:"reorderInserts"`
	ReorderTooFar          uint64 `json:"reorderTooFar"`
	ReorderPeak            int    `json:"reorderPeak"`
	ReorderCount           int    `json:"reorderCount"`
	ReorderBytes           int    `json:"reorderBytes"`
	ReorderSpan            int    `json:"reorderSpan"`
	PayloadEstimate        int    `json:"payloadEstimate"`
	AcksSent               uint64 `json:"acksSent"`
	SelectiveAcks          uint64 `json:"selectiveAcks"`
	DuplicateAcks          uint64 `json:"duplicateAcks"`
	WindowUpdatesImmediate uint64 `json:"windowUpdatesImmediate"`
	WindowUpdatesScheduled uint64 `json:"windowUpdatesScheduled"`
	CurWindow              int    `json:"curWindow"`
	MaxWindow              int    `json:"maxWindow"`
	LastRcvWindow          int    `json:"lastRcvWindow"`
	ReadBuffered           int    `json:"readBuffered"`
}

// StorageTrace times piece-data writes and piece completion. A chunk write
// stalls its connection's read loop; MarkComplete runs between a piece
// passing its hash check and readers being woken.
type StorageTrace struct {
	ChunkWrites     int64         `json:"chunkWrites"`
	ChunkWriteMs    float64       `json:"chunkWriteMs"`
	SlowWrites      []SlowWrite   `json:"slowWrites"`
	MarkCompletions []PieceTiming `json:"markCompletions"`
}

// SlowWrite is a chunk write that took at least 10 ms.
type SlowWrite struct {
	AtMs       int64   `json:"atMs"`
	Piece      int     `json:"piece"`
	Begin      int64   `json:"begin"`
	DurationMs float64 `json:"durationMs"`
	Error      string  `json:"error,omitempty"`
}

// PieceTiming is one verified piece's MarkComplete.
type PieceTiming struct {
	AtMs       int64   `json:"atMs"`
	Piece      int     `json:"piece"`
	DurationMs float64 `json:"durationMs"`
}

// LockHold is a hold of the BitTorrent client's lock of at least 50 ms (GET
// /debug/locks). While held, peer connections cannot process messages.
type LockHold struct {
	At       time.Time `json:"at"`
	HeldMs   float64   `json:"heldMs"`
	LockedAt string    `json:"lockedAt"`
}

// DialRecord results.
const (
	DialConnected = "connected"
	DialRefused   = "refused"
	DialTimeout   = "timeout"
	DialHandshake = "handshake"
	DialOther     = "other"
)

// PeerConn describes one live peer connection, for diagnosing why a swarm
// does or doesn't deliver (e.g. connected peers that never send metadata).
type PeerConn struct {
	Address string `json:"address"`
	// Network is the transport, e.g. "tcp4", "tcp6", "utp4", "utp6".
	Network string `json:"network"`
	// Outgoing is true when we dialled the peer, false when it connected to us.
	Outgoing bool `json:"outgoing"`
	// Source is how the peer was found (anacrolix peer source code, e.g. "Hg"
	// DHT, "Tr" tracker, "X" PEX, "I" incoming).
	Source string `json:"source"`
	Client string `json:"client"`
	// Extended is whether the peer supports the extension protocol, needed
	// to fetch metadata from it.
	Extended       bool    `json:"extended"`
	MetadataChunks int64   `json:"metadataChunks"`
	Downloaded     int64   `json:"downloaded"`
	DownloadSpeed  float64 `json:"downloadSpeed"`
	// Pieces the peer has (0 until it tells us).
	Pieces int `json:"pieces"`
}

// EngineManager owns the anacrolix client and the set of live engines.
type EngineManager interface {
	// EnsureEngine adds (or returns the existing) torrent for infoHash. It is
	// idempotent: a second call with more trackers should merge them.
	EnsureEngine(infoHash string, opts AddOptions) (Engine, error)
	GetEngine(infoHash string) (Engine, bool)
	RemoveEngine(infoHash string) error
	RemoveAll()
	ListEngines() []string
	// AllStats returns torrent-level stats keyed by infoHash (for /stats.json).
	AllStats() map[string]*Stats
	// Scrape estimates swarm sizes without joining the swarms (POST /scrape):
	// trackers, DHT scrapes (BEP 33) and, for torrents already open, live
	// peers. onUpdate (may be nil) receives a torrent's merged figures each
	// time a source improves them, serialized; it is not called after Scrape
	// returns. Results are in request order, one per request.
	Scrape(ctx context.Context, reqs []ScrapeRequest, onUpdate func(ScrapeUpdate)) []ScrapeResult
	// WriteStatus writes the BitTorrent client's human-readable diagnostic
	// status (every torrent, peer connection and piece state) to w.
	WriteStatus(w io.Writer)
	// Dials returns a torrent's connection log (outgoing attempts and their
	// outcome, accepted incoming connections), oldest first; false when the
	// torrent is unknown.
	Dials(infoHash string) ([]DialRecord, bool)
	// Trace returns a torrent's diagnostic record (milestones, read stalls,
	// peer samples, storage timings); false when the torrent is unknown. The
	// API layer adds the request log.
	Trace(infoHash string) (Trace, bool)
	// LockHolds returns the recent long holds of the client lock, oldest first.
	LockHolds() []LockHold
	Close() error
}

// ScrapeRequest is one torrent to scrape: its info hash plus the trackers the
// caller knows for it (merged with the engine's own tracker list).
type ScrapeRequest struct {
	InfoHash string   `json:"infoHash"`
	Trackers []string `json:"trackers,omitempty"`
}

// ScrapeResult reports the best (largest) swarm figures any source returned
// for a torrent. Trackers is how many trackers answered, DHT whether DHT
// nodes returned scrape filters, Live whether the figures include an open
// engine's connected peers. With none of them the counts are unknown rather
// than zero.
type ScrapeResult struct {
	InfoHash  string `json:"infoHash"`
	Seeders   int    `json:"seeders"`
	Leechers  int    `json:"leechers"`
	Completed int    `json:"completed"`
	Trackers  int    `json:"trackers"`
	DHT       bool   `json:"dht,omitempty"`
	Live      bool   `json:"live,omitempty"`
}

// Known reports whether any source answered for the torrent.
func (r ScrapeResult) Known() bool { return r.Trackers > 0 || r.DHT || r.Live }

// ScrapeUpdate is one streamed improvement of a torrent's ScrapeResult, with
// the source that caused it: "cache", "tracker", "dht" or "live".
type ScrapeUpdate struct {
	ScrapeResult
	Source string `json:"source"`
}

// SettingsStore persists the user-facing server settings (cacheSize, bt*, ...).
type SettingsStore interface {
	// Values returns the current settings.values object.
	Values() map[string]interface{}
	// OptionsSchema returns the settings.options array (GUI schema). The list of
	// available interface addresses feeds the remoteHttps selector.
	OptionsSchema(availableInterfaces []string) []map[string]interface{}
	// Extend merges a patch (POST /settings body) into the values.
	Extend(patch map[string]interface{})
	// Get returns a single value.
	Get(key string) interface{}
	// Save persists to disk.
	Save() error
}

// HLSSessionOptions carries the optional per-session overrides a client may
// pass as query parameters on GET /hlsv2/{id}/master.m3u8 (ttl, maxWidth,
// maxHeight, bitrate, maxRate, bufSize). Fields hold the raw, unvalidated
// query values; "" means "not given". The MediaProber owns parsing, bounds
// checking and the RIVULET_HLS_SESSION_OVERRIDES opt-in gate — with the gate
// off every field is ignored, so callers can always pass the query through.
type HLSSessionOptions struct {
	TTL       string // idle-eviction TTL: whole seconds or a Go duration string
	MaxWidth  string // downscale cap, pixels
	MaxHeight string // downscale cap, pixels
	Bitrate   string // -b:v, ffmpeg bitrate syntax (e.g. "6M")
	MaxRate   string // -maxrate; defaults to Bitrate when only Bitrate is given
	BufSize   string // -bufsize; defaults to 2x Bitrate when only Bitrate is given
}

// ErrInvalidHLSOption is wrapped by MediaProber.StartHLS when a per-session
// override in HLSSessionOptions is malformed or out of range. The API layer
// maps it to 400 Bad Request (every other StartHLS error stays a 500).
var ErrInvalidHLSOption = errors.New("invalid HLS session option")

// ErrInvalidMediaURL is matched (errors.Is) by MediaProber errors caused by a
// media URL the caller supplied: empty, malformed, a non-http(s) scheme, a
// refused (private/self-origin) target or an unresolvable host. The API layer
// maps it to 400 Bad Request.
var ErrInvalidMediaURL = errors.New("invalid media URL")

// ErrSubtitleNotFound is matched (errors.Is) by MediaProber.WriteSubtitles when
// the subtitle source answered 404/410. The API layer maps it to 404 Not Found.
var ErrSubtitleNotFound = errors.New("subtitle source not found")

// MediaProber backs the ffmpeg-based helper routes. Implementations shell out
// to ffprobe/ffmpeg. Methods return values that are JSON-encoded verbatim.
type MediaProber interface {
	Probe(streamURL string) (interface{}, error)
	Tracks(rawURL string) (interface{}, error)
	OpenSubHash(videoURL string) (result interface{}, err error)
	SubtitlesTracks(subsURL string) (result interface{}, err error)
	// WriteSubtitles converts the subtitle track at `from` into SRT (ext=="srt")
	// or WEBVTT (ext=="vtt"), applying offsetMs, writing to w.
	WriteSubtitles(w io.Writer, from, ext string, offsetMs int) error
	// StartHLS starts (or reuses) an ffmpeg HLS transcode for session id and
	// returns the master playlist text. opts carries the optional per-session
	// overrides from the master.m3u8 query string (zero value = none); an
	// invalid override is reported as an error wrapping ErrInvalidHLSOption.
	StartHLS(id, mediaURL string, opts HLSSessionOptions) (string, error)
	// HLSFile returns the filesystem path and content-type for a file in the
	// HLS session (playlist or .ts segment).
	HLSFile(ctx context.Context, id, name string) (path, contentType string, err error)
	// DeleteHLS removes HLS session id: it is unregistered immediately (so
	// later StartHLS/HLSFile calls see an unknown session) and its segment
	// cache is removed from disk once no HLSFile call for it is still in
	// flight. Returns an error wrapping ErrHLSInvalidSessionID for an id
	// StartHLS would reject, or ErrHLSSessionNotFound when no such session
	// exists.
	DeleteHLS(id string) error
}

// Sentinel errors returned (wrapped) by MediaProber's HLS methods, so the
// HTTP layer can map them to status codes with errors.Is without importing
// the concrete media package.
var (
	// ErrHLSSessionNotFound reports that no HLS session exists for an id.
	ErrHLSSessionNotFound = errors.New("unknown session")
	// ErrHLSInvalidSessionID reports an HLS session id that is empty or could
	// escape the HLS working directory (path traversal).
	ErrHLSInvalidSessionID = errors.New("invalid session id")
	// ErrHLSSourceFailed reports that a segment could not be produced
	// because its source failed while it was being transcoded (the origin
	// answered with an error status such as 429, the fetch failed, or the
	// body broke off). The HTTP layer answers 502.
	ErrHLSSourceFailed = errors.New("source failed")
	// ErrHLSSegmentTimeout reports that a segment could not be produced in
	// time: its origin timed out, or the transcode ran past its overall
	// deadline (slow encodes count too). The HTTP layer answers 504.
	ErrHLSSegmentTimeout = errors.New("segment timed out")
	// ErrHLSDurationUnknown reports that a session cannot be prepared
	// because its media duration is unknown (the probe failed). The HTTP
	// layer answers 409.
	ErrHLSDurationUnknown = errors.New("session duration unknown")
	// ErrHLSPrepareTooLarge reports that a prepared session would not fit
	// in the configured per-session segment cache cap
	// (RIVULET_HLS_SEGMENT_CACHE_BYTES). The HTTP layer answers 507.
	ErrHLSPrepareTooLarge = errors.New("prepared session would exceed the segment cache cap")
)

// HLSPreparer is implemented by a MediaProber that supports engine-side
// preparation (POST /hlsv2/{id}/prepare, RIVULET_HLS_PREPARE). The HTTP layer
// finds it with a type assertion; while HLSPrepareEnabled reports false the
// prepare routes answer exactly as they did before the feature existed.
type HLSPreparer interface {
	HLSPrepareEnabled() bool
	// PrepareHLS starts (or keeps running) the preparation job of session
	// id. audio is "default" (or "") or "all", and selects which audio
	// renditions of a multi-audio session are prepared besides the video.
	PrepareHLS(id, audio string) (HLSPrepareStatus, error)
	// HLSPrepareStatus reports session id's preparation progress and the
	// cache status of each segment, whether or not a job was started.
	HLSPrepareStatus(id string) (HLSPrepareStatus, error)
	// StopPrepareHLS stops session id's preparation job, if any, and unpins
	// its segments; those already cached stay cached.
	StopPrepareHLS(id string) error
}

// HLSPrepareStatus is the JSON body of the /hlsv2/{id}/prepare routes.
type HLSPrepareStatus struct {
	// State is "idle" (no job requested, or stopped), "running", "done"
	// (every segment was attempted; see Cached against Total) or "failed"
	// (stopped after repeated transcode errors; see Error).
	State string `json:"state"`
	// Audio is the audio selection of the requested job ("default" or
	// "all"); empty while idle.
	Audio string `json:"audio,omitempty"`
	// Segments is the number of segments in each rendition.
	Segments int `json:"segments"`
	// Total counts the segment files to prepare across Renditions; Cached,
	// Uncached and Failed count them by their Map status.
	Total    int `json:"total"`
	Cached   int `json:"cached"`
	Uncached int `json:"uncached"`
	Failed   int `json:"failed"`
	// CachedBytes is the size on disk of the cached segments counted in
	// Cached; EstimatedBytes is the expected size of all Total of them at
	// the session's peak bitrate.
	CachedBytes    int64 `json:"cachedBytes"`
	EstimatedBytes int64 `json:"estimatedBytes"`
	// Error is why a failed job stopped.
	Error      string                `json:"error,omitempty"`
	Renditions []HLSPrepareRendition `json:"renditions"`
	// Issues lists, for up to 100 segments, why a segment is uncached or
	// failed.
	Issues []HLSPrepareIssue `json:"issues,omitempty"`
}

// HLSPrepareRendition is one prepared playlist and the status of each of its
// segments.
type HLSPrepareRendition struct {
	// Playlist is the media playlist the segments belong to
	// (playlist.m3u8, video.m3u8 or audio<k>.m3u8).
	Playlist string `json:"playlist"`
	// Map has one character per segment, in order: 'c' cached, '-' not
	// cached (not transcoded yet, or evicted), 'u' transcoded but not
	// cached because the output failed its check (see Issues), 'f' the
	// transcode failed (see Issues).
	Map string `json:"map"`
}

// HLSPrepareIssue explains one uncached or failed segment.
type HLSPrepareIssue struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}
