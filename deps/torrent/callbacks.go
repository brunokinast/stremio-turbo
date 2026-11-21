package torrent

import (
	"time"

	"github.com/anacrolix/torrent/mse"
	pp "github.com/anacrolix/torrent/peer_protocol"
)

// These are called synchronously, and do not pass ownership of arguments (do not expect to retain
// data after returning from the callback). The Client and other locks may still be held. nil
// functions are not called.
type Callbacks struct {
	// Called after a peer connection completes the BitTorrent handshake. The Client lock is not
	// held.
	CompletedHandshake func(*PeerConn, InfoHash)
	ReadMessage        func(*PeerConn, *pp.Message)
	// This can be folded into the general case below.
	ReadExtendedHandshake func(*PeerConn, *pp.ExtendedHandshakeMessage)
	PeerConnClosed        func(*PeerConn)
	// BEP 10 message. Not sure if I should call this Ltep universally. Each handler here is called
	// in order.
	PeerConnReadExtensionMessage []func(PeerConnReadExtensionMessageEvent)

	// Provides secret keys to be tried against incoming encrypted connections.
	ReceiveEncryptedHandshakeSkeys mse.SecretKeyIter

	ReceivedUsefulData []func(ReceivedUsefulDataEvent)
	ReceivedRequested  []func(PeerMessageEvent)
	DeletedRequest     []func(PeerRequestEvent)
	SentRequest        []func(PeerRequestEvent)
	PeerClosed         []func(*Peer)
	NewPeer            []func(*Peer)
	// Called when a PeerConn has been added to a Torrent. It's finished all BitTorrent protocol
	// handshakes, and is about to start sending and receiving BitTorrent messages. The extended
	// handshake has not yet occurred. This is a good time to alter the supported extension
	// protocols.
	PeerConnAdded []func(*PeerConn)

	// Sends status event updates. Useful to inform the user of specific events as they happen,
	// for logging or to action on.
	StatusUpdated []func(StatusUpdatedEvent)

	// Called when an outgoing connection attempt finishes, whether it connected and completed the
	// BitTorrent handshake or failed. Useful for diagnosing peer acquisition (which addresses
	// fail, how, and how long that takes). The Client lock is held: handlers must be quick and
	// must not call back into the Client.
	OutgoingConnResult []func(OutgoingConnResultEvent)

	// Called after each received chunk is written to storage, with how long the write took.
	// Useful for finding storage stalls, which stop the connection's read loop for as long as
	// they last. The Client lock is not held.
	ChunkWritten []func(ChunkWrittenEvent)
	// Called after a piece that passed its hash check was marked complete in storage, with how
	// long that took. Readers waiting on the piece are woken only after this returns. The
	// Client lock is not held.
	PieceMarkedComplete []func(PieceMarkedCompleteEvent)

	// Like ReceivedUsefulData, with when the chunk was requested from this peer and its piece's
	// priority: for measuring how long urgent chunks take. Called with the Client lock held.
	ReceivedUsefulChunk []func(ReceivedUsefulChunkEvent)
	// A reader's blocked read waited at least 20 ms, with what held up the chunk it waited for when
	// the wait began. Called without the Client lock.
	ReaderWaited []func(ReaderWaitedEvent)
	// ClientConfig.ReaderCriticalBytes moved a critical request to a peer expected to deliver it
	// sooner. Called with the Client lock held.
	CriticalRequestStolen []func(CriticalRequestStolenEvent)
}

type ChunkWrittenEvent struct {
	Torrent *Torrent
	Piece   int
	// Offset of the chunk within the piece, and its length.
	Begin    int64
	Length   int
	Duration time.Duration
	Err      error
}

type PieceMarkedCompleteEvent struct {
	Torrent  *Torrent
	Piece    int
	Duration time.Duration
	Err      error
}

type OutgoingConnResultEvent struct {
	InfoHash InfoHash
	Peer     PeerInfo
	// The transport that connected, e.g. "tcp4" or "utp6". Empty when the attempt failed.
	Network  string
	Started  time.Time
	Duration time.Duration
	// The part of Duration spent waiting for ClientConfig.DialRateLimiter before dialing.
	DialWait time.Duration
	// Nil when the connection was established and handshaken.
	Err error
}

type ReceivedUsefulDataEvent = PeerMessageEvent

type ReceivedUsefulChunkEvent struct {
	Peer   *Peer
	Piece  int
	Length int
	// The piece's priority when the chunk arrived.
	Priority PiecePriority
	// When the chunk was last requested from this peer; zero if this peer didn't hold the request
	// (another peer did, or it was cancelled).
	RequestedAt time.Time
}

type ReaderWaitedEvent struct {
	Torrent *Torrent
	// Torrent offset of the byte the read waited for, and how long it waited.
	Offset int64
	Waited time.Duration
	// The rest describes the chunk holding Offset when the wait began.
	Wait ChunkWaitState
}

// What a chunk was waiting on: who has it requested, and the expected delivery there and on the
// best other peer. Estimates are -1 when unknown (no recent rate).
type ChunkWaitState struct {
	Requested bool
	// The requesting peer's address and network, when Requested.
	Holder, HolderNetwork string
	RequestedAgo          time.Duration
	// Requests the holder was given before this one and hasn't delivered yet.
	Ahead int
	// The holder's recent useful rate, bytes per second.
	HolderRate     float64
	HolderEstimate time.Duration
	// The unchoked peer with the piece, other than the holder, expected to deliver a new request
	// soonest.
	BestOther, BestOtherNetwork string
	BestOtherEstimate           time.Duration
}

type CriticalRequestStolenEvent struct {
	From, To *Peer
	Request  RequestIndex
	// Expected delivery on the holder that lost the request, and on the peer that took it.
	FromEstimate, ToEstimate time.Duration
	// The request had waited longer than its holder should have needed (FromEstimate is then at
	// least that wait).
	Overdue bool
}

type PeerMessageEvent struct {
	Peer    *Peer
	Message *pp.Message
}

type PeerRequestEvent struct {
	Peer *Peer
	Request
}

type PeerConnReadExtensionMessageEvent struct {
	PeerConn *PeerConn
	// You can look up what protocol this corresponds to using the PeerConn.LocalLtepProtocolMap.
	ExtensionNumber pp.ExtensionNumber
	Payload         []byte
}

type StatusUpdatedEvent struct {
	Event StatusEvent `json:"event"`
	Error error       `json:"error"`
	// The following fields may or may not be populated depending on the event.
	PeerId   PeerID `json:"peer_id"`
	Url      string `json:"url"`
	InfoHash string `json:"info_hash"`
}

type StatusEvent string

const (
	PeerConnected             StatusEvent = "peer_connected"
	PeerDisconnected          StatusEvent = "peer_disconnected"
	TrackerConnected          StatusEvent = "tracker_connected"
	TrackerDisconnected       StatusEvent = "tracker_disconnected"
	TrackerAnnounceSuccessful StatusEvent = "tracker_announce_successful"
	TrackerAnnounceError      StatusEvent = "tracker_announce_error"
)
