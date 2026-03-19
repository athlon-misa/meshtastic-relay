// Package transport defines the interface for message transport backends.
//
// The relay daemon uses multiple transports (MQTT over internet, Meshtastic
// serial over LoRa) and a router selects the best path for each message.
package transport

import (
	"context"
	"fmt"

	"github.com/meshtastic-relay/relay/identity"
)

// PacketType discriminates between regular messages and presence announcements
// flowing through the transport layer.
type PacketType int

const (
	// PacketMessage is a regular user-to-user message (default zero value).
	PacketMessage PacketType = 0
	// PacketPresence is a presence announcement.
	PacketPresence PacketType = 1
	// PacketAlert is a high-priority emergency broadcast alert.
	PacketAlert PacketType = 2
	// PacketAdmin is a control message on the admin channel (e.g., OTA updates).
	PacketAdmin PacketType = 3
)

// Packet is a transport-level packet containing a raw message and routing metadata.
type Packet struct {
	// Type discriminates message packets from presence packets.
	Type PacketType
	// DestHash is the intended recipient's identity hash.
	DestHash identity.Hash
	// SourceHash is the sender's identity hash.
	SourceHash identity.Hash
	// Data is the raw wire-format message bytes.
	Data []byte
	// Via indicates which transport delivered this packet (for logging/metrics).
	Via string
	// MeshNodeNum is the Meshtastic node number of the originating radio.
	// Zero if the packet did not arrive via LoRa mesh.
	MeshNodeNum uint32
	// MeshPacketID is the Meshtastic packet ID, used for deduplication.
	// Zero if the packet did not arrive via LoRa mesh.
	MeshPacketID uint32
}

// Transport is the interface that all transport backends must implement.
// Each transport provides a way to send packets to the network and receive
// packets from it.
type Transport interface {
	// Name returns a human-readable name for this transport (e.g., "mqtt", "meshradio").
	Name() string

	// Start initializes the transport and begins receiving packets.
	// The context controls the transport lifecycle; canceling it should
	// trigger a graceful shutdown.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the transport.
	Stop() error

	// Send transmits a packet via this transport.
	Send(ctx context.Context, pkt *Packet) error

	// Receive returns a channel that delivers incoming packets.
	// The channel is closed when the transport stops.
	Receive() <-chan *Packet

	// Health returns a score from 0 (completely down) to 100 (fully healthy)
	// indicating the current health of this transport.
	Health() int
}

// ErrTransportDown is returned when a transport cannot send because it is
// not connected or has failed health checks.
var ErrTransportDown = fmt.Errorf("transport: transport is down")

// ErrPayloadTooLarge is returned when a packet exceeds the transport's
// maximum payload size.
var ErrPayloadTooLarge = fmt.Errorf("transport: payload too large")
