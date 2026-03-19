package bridge

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/meshtastic-relay/relay/identity"
	"github.com/meshtastic-relay/relay/transport"
)

// PresenceMode indicates how a user is currently reachable on the network.
type PresenceMode string

const (
	// PresenceWebSocket means the user has an active WebSocket session to a
	// relay node.
	PresenceWebSocket PresenceMode = "websocket"
	// PresenceLoRa means the user is reachable via a registered Meshtastic
	// device on the LoRa mesh.
	PresenceLoRa PresenceMode = "lora"
	// PresenceOffline means the user has no active session or device.
	PresenceOffline PresenceMode = "offline"
)

// BridgeConfig holds configuration for the LoRa gateway bridge.
type BridgeConfig struct {
	// Enabled controls whether the bridge is active.
	Enabled bool
	// ActiveTimeout is how long a device can be silent before it is no
	// longer considered active for outbound delivery.
	ActiveTimeout time.Duration
	// DedupWindow is how long to remember packet IDs for deduplication.
	DedupWindow time.Duration
}

// Bridge is the LoRa gateway bridge that translates between Meshtastic mesh
// traffic and the relay identity network. It maps Meshtastic node numbers to
// relay identity hashes, deduplicates rebroadcast packets, and formats
// messages for bidirectional delivery.
type Bridge struct {
	cfg      BridgeConfig
	registry *Registry
	dedup    *DedupCache
	nodeID   identity.Hash

	mu     sync.Mutex
	cancel context.CancelFunc
}

// New creates a Bridge. The nodeID is the relay node's own identity hash,
// used to stamp outbound packets.
func New(cfg BridgeConfig, registry *Registry, nodeID identity.Hash) *Bridge {
	return &Bridge{
		cfg:      cfg,
		registry: registry,
		dedup:    NewDedupCache(cfg.DedupWindow),
		nodeID:   nodeID,
	}
}

// ProcessInbound is called when a LoRa packet arrives from the mesh radio.
// It performs the following steps:
//
//  1. Checks that the source node is a registered device.
//  2. Deduplicates the packet using (nodeNum, packetID).
//  3. Parses the "@<hash> <message>" addressing format from the text payload.
//  4. Remaps the source identity from the Meshtastic node number to the
//     registered relay user's identity hash.
//  5. Updates the device's last-heard state.
//
// Returns a relay-ready transport.Packet with the remapped source and parsed
// destination, or nil if the packet cannot be bridged (unregistered device,
// duplicate, or unparseable format).
func (b *Bridge) ProcessInbound(pkt *transport.Packet) *transport.Packet {
	if pkt == nil {
		return nil
	}

	nodeNum := pkt.MeshNodeNum
	packetID := pkt.MeshPacketID

	// Step 1: Check registration.
	device := b.registry.LookupByNodeNum(nodeNum)
	if device == nil {
		return nil
	}

	// Step 2: Deduplicate.
	if packetID != 0 && b.dedup.IsDuplicate(nodeNum, packetID) {
		return nil
	}

	// Step 3: Parse "@<hash> <message>" format.
	text := string(pkt.Data)
	destHashHex, body, ok := ParseBridgeMessage(text)
	if !ok {
		return nil
	}

	// Resolve the destination hash hex to an identity.Hash.
	var destHash identity.Hash
	switch len(destHashHex) {
	case fullHashHexLen:
		parsed, err := identity.HashFromHex(destHashHex)
		if err != nil {
			return nil
		}
		destHash = parsed
	case shortHashHexLen:
		// For short hashes, decode to bytes and zero-pad into a Hash.
		// The caller can use ResolveShortHash for full resolution against
		// the presence table, but for ProcessInbound we store the prefix
		// so the router can attempt delivery.
		decoded, err := hex.DecodeString(destHashHex)
		if err != nil {
			return nil
		}
		copy(destHash[:], decoded)
	default:
		return nil
	}

	// Step 4: Remap source to the relay user's identity.
	// Step 5: Update last-heard state.
	b.registry.UpdateLastHeard(nodeNum, b.nodeID, packetID)

	return &transport.Packet{
		DestHash:     destHash,
		SourceHash:   device.UserHash,
		Data:         []byte(body),
		Via:          "lora-bridge",
		MeshNodeNum:  nodeNum,
		MeshPacketID: packetID,
	}
}

// FormatOutbound formats a relay message for LoRa delivery to a specific
// user. It looks up the user's active device and returns the formatted text
// payload and the target Meshtastic node number.
//
// Returns an error if the user has no active device.
func (b *Bridge) FormatOutbound(destHash identity.Hash, senderHash identity.Hash, data []byte) (payload []byte, targetNodeNum uint32, err error) {
	device := b.registry.ActiveDeviceForUser(destHash)
	if device == nil {
		return nil, 0, fmt.Errorf("bridge: no active LoRa device for user %s", destHash)
	}

	formatted := FormatOutboundMessage(senderHash, string(data))
	return []byte(formatted), device.MeshNodeNum, nil
}

// UserPresenceMode returns the current presence mode for a user based on
// their WebSocket session status and LoRa device activity.
//
// Priority order: WebSocket > LoRa > Offline. If the user has an active
// WebSocket session, that takes precedence because it offers higher
// bandwidth and lower latency than LoRa.
func (b *Bridge) UserPresenceMode(userHash identity.Hash, wsConnected bool) PresenceMode {
	if wsConnected {
		return PresenceWebSocket
	}

	device := b.registry.ActiveDeviceForUser(userHash)
	if device != nil {
		return PresenceLoRa
	}

	return PresenceOffline
}

// ResolveShortHash attempts to resolve a 12-character short hash hex string
// to a full identity hash. The resolver function is called with the decoded
// prefix bytes and should return all identity hashes that start with those
// bytes (typically by scanning the presence table).
//
// Returns an error if the short hash is ambiguous (matches multiple
// identities) or if no match is found.
func (b *Bridge) ResolveShortHash(shortHex string, resolver func(prefix []byte) []identity.Hash) (identity.Hash, error) {
	if len(shortHex) != shortHashHexLen {
		return identity.Hash{}, fmt.Errorf("bridge: short hash must be %d hex chars, got %d", shortHashHexLen, len(shortHex))
	}

	prefix, err := hex.DecodeString(shortHex)
	if err != nil {
		return identity.Hash{}, fmt.Errorf("bridge: invalid short hash hex: %w", err)
	}

	matches := resolver(prefix)
	switch len(matches) {
	case 0:
		return identity.Hash{}, fmt.Errorf("bridge: no identity found for short hash %s", shortHex)
	case 1:
		return matches[0], nil
	default:
		return identity.Hash{}, fmt.Errorf("bridge: ambiguous short hash %s matches %d identities", shortHex, len(matches))
	}
}

// Start begins the background dedup cleanup goroutine. The goroutine runs
// until the context is canceled or Stop is called.
func (b *Bridge) Start(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	b.cancel = cancel

	go b.cleanupLoop(ctx)
	log.Printf("bridge: started (dedup_window=%v, active_timeout=%v)", b.cfg.DedupWindow, b.cfg.ActiveTimeout)
}

// Stop signals the bridge to shut down its background goroutines.
func (b *Bridge) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	log.Printf("bridge: stopped")
}

// cleanupLoop periodically purges expired entries from the dedup cache.
func (b *Bridge) cleanupLoop(ctx context.Context) {
	interval := b.cfg.DedupWindow
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.dedup.Cleanup()
		}
	}
}
