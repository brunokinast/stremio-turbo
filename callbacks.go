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
	// Nil when the connection was established and handshaken.
	Err error
}

type ReceivedUsefulDataEvent = PeerMessageEvent

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
