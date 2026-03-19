// Package node implements the relay node daemon that manages user connections,
// message routing, and transport coordination.
//
// The node daemon is the central component that ties together identity,
// announce, message, transport, and store packages into a running relay.
package node

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/meshtastic-relay/relay/alert"
	"github.com/meshtastic-relay/relay/announce"
	"github.com/meshtastic-relay/relay/config"
	"github.com/meshtastic-relay/relay/identity"
	"github.com/meshtastic-relay/relay/message"
	"github.com/meshtastic-relay/relay/power"
	"github.com/meshtastic-relay/relay/store"
	"github.com/meshtastic-relay/relay/transport"
	"github.com/meshtastic-relay/relay/transport/router"
	"github.com/meshtastic-relay/relay/update"
)

// SessionState represents the lifecycle state of a user session.
type SessionState int

const (
	// SessionConnecting means the user has initiated a connection but
	// has not completed authentication.
	SessionConnecting SessionState = iota
	// SessionAuthenticated means the user has completed challenge-response auth.
	SessionAuthenticated
	// SessionActive means the user is fully connected and can send/receive.
	SessionActive
	// SessionDisconnecting means the user is in the process of disconnecting.
	SessionDisconnecting
)

// String returns the human-readable session state name.
func (s SessionState) String() string {
	switch s {
	case SessionConnecting:
		return "connecting"
	case SessionAuthenticated:
		return "authenticated"
	case SessionActive:
		return "active"
	case SessionDisconnecting:
		return "disconnecting"
	default:
		return "unknown"
	}
}

// Session represents a connected user's session on this relay node.
type Session struct {
	// UserHash is the authenticated user's identity hash.
	UserHash identity.Hash
	// Identity is the user's public-key-only identity (for verification).
	Identity *identity.Identity
	// State is the current session lifecycle state.
	State SessionState
	// ConnectedAt is when the session was established.
	ConnectedAt time.Time
	// LastHeartbeat is the last time a heartbeat was received.
	LastHeartbeat time.Time
	// SeqNum tracks the next outbound sequence number for this user.
	SeqNum *message.NextSeqNum
	// challenge is the pending authentication challenge (nil after auth).
	challenge *identity.Challenge
}

// DeliveryFunc is a callback invoked by the Node when a message arrives for
// a locally connected user. The server package registers one of these to push
// messages over the user's WebSocket connection.
type DeliveryFunc func(userHash identity.Hash, pkt *transport.Packet) error

// BridgeDeliveryFunc is called when a message needs to be delivered to a user
// who is reachable via LoRa (not WebSocket). The bridge translates relay
// identity hashes to Meshtastic node numbers and sends via the radio.
type BridgeDeliveryFunc func(userHash identity.Hash, senderHash identity.Hash, data []byte) error

// InboundInterceptor is called before the node processes an incoming packet.
// It may return a modified packet (e.g., with remapped identity hashes from
// the bridge), the original packet unchanged, or nil to drop the packet.
type InboundInterceptor func(pkt *transport.Packet) *transport.Packet

// PresenceSender publishes and clears presence announcements via MQTT.
type PresenceSender interface {
	SendPresence(ctx context.Context, userHash identity.Hash, data []byte) error
	ClearPresence(ctx context.Context, userHash identity.Hash) error
}

// PresenceNotifyFunc is called when the presence table changes. It pushes
// presence updates to connected WebSocket clients.
type PresenceNotifyFunc func(userHash identity.Hash, nodeID identity.Hash, online bool)

// AlertBroadcastFunc is called when an alert needs to be pushed to all
// connected WebSocket clients.
type AlertBroadcastFunc func(a *alert.Alert)

// Node is the relay daemon that manages user sessions, routes messages,
// and coordinates transports.
type Node struct {
	cfg              *config.Config
	identity         *identity.Identity
	router           *router.Router
	store            *store.Store
	presence         *announce.Table
	sessions         map[identity.Hash]*Session
	seqTracker       *message.SeqTracker
	deliverLocal     DeliveryFunc
	deliverBridge    BridgeDeliveryFunc
	interceptInbound InboundInterceptor
	presenceSender   PresenceSender
	presenceNotify   PresenceNotifyFunc
	alertBroadcast   AlertBroadcastFunc
	seenAlerts       map[string]time.Time
	powerManager     *power.Manager
	updateManager    *update.Manager
	mu               sync.RWMutex
	cancel           context.CancelFunc
	announceStop     chan struct{}
}

// New creates a new relay Node from configuration.
func New(cfg *config.Config, nodeIdentity *identity.Identity, r *router.Router, s *store.Store) *Node {
	return &Node{
		cfg:        cfg,
		identity:   nodeIdentity,
		router:     r,
		store:      s,
		presence:   announce.NewTable(5 * time.Minute),
		sessions:   make(map[identity.Hash]*Session),
		seqTracker: message.NewSeqTracker(),
		seenAlerts: make(map[string]time.Time),
	}
}

// SetDeliveryFunc registers a callback that the node invokes whenever a
// message arrives for a locally connected user. This must be called before
// Start so that messages are delivered rather than silently dropped.
func (n *Node) SetDeliveryFunc(fn DeliveryFunc) {
	n.deliverLocal = fn
}

// SetBridgeDeliveryFunc registers a callback for delivering messages to users
// who are reachable via LoRa bridge rather than WebSocket. Must be called
// before Start.
func (n *Node) SetBridgeDeliveryFunc(fn BridgeDeliveryFunc) {
	n.deliverBridge = fn
}

// SetInboundInterceptor registers a function that is called on every incoming
// packet before the node processes it. The interceptor may remap fields (e.g.,
// translating Meshtastic node numbers to relay identity hashes via the bridge),
// pass the packet through unchanged, or return nil to drop it.
func (n *Node) SetInboundInterceptor(fn InboundInterceptor) {
	n.interceptInbound = fn
}

// SetPresenceSender registers the transport used to publish and clear
// presence announcements via MQTT. Must be called before Start.
func (n *Node) SetPresenceSender(ps PresenceSender) {
	n.presenceSender = ps
}

// SetPresenceNotifyFunc registers a callback that fires when the presence
// table changes. Typically wired to server.BroadcastPresence.
func (n *Node) SetPresenceNotifyFunc(fn PresenceNotifyFunc) {
	n.presenceNotify = fn
	// Wire the announce table's onChange callback to drive notifications.
	n.presence.SetOnChange(func(entry announce.Entry, online bool) {
		fn(entry.UserHash, entry.NodeID, online)
	})
}

// SetAlertBroadcastFunc registers a callback that the node invokes to push
// emergency alerts to all connected WebSocket clients. Must be called
// before Start.
func (n *Node) SetAlertBroadcastFunc(fn AlertBroadcastFunc) {
	n.alertBroadcast = fn
}

// SetPowerManager sets the power manager for the node. When the power tier
// changes, the manager's onChange callback logs the transition and disconnects
// excess sessions if MaxSessions decreased. Must be called before Start.
func (n *Node) SetPowerManager(pm *power.Manager) {
	n.powerManager = pm
	pm.SetOnChange(func(p power.Profile) {
		log.Printf("node: power tier changed to %s (announce=%v, cleanup=%v, maxSessions=%d)",
			p.Tier, p.AnnounceInterval, p.CleanupInterval, p.MaxSessions)

		// If MaxSessions decreased, disconnect excess sessions (oldest first).
		n.mu.Lock()
		excess := len(n.sessions) - p.MaxSessions
		if excess > 0 {
			// Collect sessions to find the oldest ones to evict.
			type sessionEntry struct {
				hash identity.Hash
				ts   time.Time
			}
			entries := make([]sessionEntry, 0, len(n.sessions))
			for h, s := range n.sessions {
				entries = append(entries, sessionEntry{hash: h, ts: s.ConnectedAt})
			}
			// Disconnect the oldest excess sessions.
			for i := 0; i < excess; i++ {
				oldest := 0
				for j := 1; j < len(entries); j++ {
					if entries[j].ts.Before(entries[oldest].ts) {
						oldest = j
					}
				}
				log.Printf("node: disconnecting excess session %s (power tier reduced max sessions)", entries[oldest].hash)
				n.disconnectSessionLocked(entries[oldest].hash)
				entries = append(entries[:oldest], entries[oldest+1:]...)
			}
		}
		n.mu.Unlock()
	})
}

// SetUpdateManager sets the OTA update manager for the node. Must be called
// before Start.
func (n *Node) SetUpdateManager(um *update.Manager) {
	n.updateManager = um
}

// UpdateStatus returns the current OTA update status. If no update manager
// is configured, it returns an empty UpdateInfo.
func (n *Node) UpdateStatus() update.UpdateInfo {
	if n.updateManager == nil {
		return update.UpdateInfo{}
	}
	return n.updateManager.Status()
}

// PowerProfile returns the current power profile. If no power manager is set,
// it returns the default full-power profile.
func (n *Node) PowerProfile() power.Profile {
	if n.powerManager != nil {
		return n.powerManager.Current()
	}
	return power.DefaultProfile(power.TierFull)
}

// BroadcastAlert creates a new emergency alert and sends it via dual-path
// transport (all healthy transports simultaneously) for maximum reliability.
// It also pushes the alert to all locally connected WebSocket clients.
func (n *Node) BroadcastAlert(senderHash identity.Hash, text string, priority alert.Priority) (*alert.Alert, error) {
	a := alert.New(senderHash.String(), n.identity.Hash().String(), text, priority, 30*time.Minute)

	data, err := alert.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("node: marshal alert: %w", err)
	}

	// Record alert ID so we don't re-broadcast our own alert when it echoes back.
	n.mu.Lock()
	n.seenAlerts[a.ID] = time.Now()
	n.mu.Unlock()

	// Build a broadcast packet (zero DestHash = broadcast).
	pkt := &transport.Packet{
		Type:       transport.PacketAlert,
		SourceHash: senderHash,
		Data:       data,
	}

	// Send via dual-path for maximum reliability.
	errs := n.router.SendDualPath(context.Background(), pkt)
	if len(errs) > 0 {
		// Log errors but don't fail -- local delivery still works.
		for _, e := range errs {
			log.Printf("node: alert dual-path send error: %v", e)
		}
	}

	// Push to all locally connected WebSocket clients.
	if n.alertBroadcast != nil {
		n.alertBroadcast(a)
	}

	log.Printf("node: alert broadcast id=%s priority=%d from=%s", a.ID, a.Priority, senderHash)
	return a, nil
}

// Start initializes the node daemon and begins processing messages.
func (n *Node) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	n.cancel = cancel
	n.announceStop = make(chan struct{})

	// Start the router (which starts all transports)
	if err := n.router.Start(ctx); err != nil {
		cancel()
		return fmt.Errorf("node: start router: %w", err)
	}

	// Start the message processing loop
	go n.processIncoming(ctx)

	// Start periodic announce broadcasts
	go n.announceLoop(ctx)

	// Start session health monitoring
	go n.sessionMonitor(ctx)

	// Start periodic store cleanup
	go n.storeCleanup(ctx)

	// Start periodic alert dedup map cleanup
	go n.alertDedupCleanup(ctx)

	log.Printf("node: started (id=%s, name=%s, region=%s)",
		n.identity.Hash(), n.cfg.Node.Name, n.cfg.Node.Region)

	return nil
}

// Stop gracefully shuts down the node daemon.
func (n *Node) Stop() error {
	log.Printf("node: shutting down...")

	if n.cancel != nil {
		n.cancel()
	}

	// Disconnect all sessions
	n.mu.Lock()
	for hash := range n.sessions {
		n.disconnectSessionLocked(hash)
	}
	n.mu.Unlock()

	// Stop the router (stops all transports)
	if err := n.router.Stop(); err != nil {
		log.Printf("node: router stop error: %v", err)
	}

	// Close the store
	if n.store != nil {
		if err := n.store.Close(); err != nil {
			log.Printf("node: store close error: %v", err)
		}
	}

	log.Printf("node: stopped")
	return nil
}

// ConnectUser initiates a new user session. Returns a challenge for
// the user to prove their identity.
func (n *Node) ConnectUser() (*identity.Challenge, error) {
	n.mu.RLock()
	sessionCount := len(n.sessions)
	n.mu.RUnlock()

	// Use the power profile's MaxSessions if a power manager is configured,
	// otherwise fall back to the static config value.
	maxSessions := n.cfg.Daemon.MaxSessions
	if n.powerManager != nil {
		profile := n.powerManager.Current()
		maxSessions = profile.MaxSessions
	}

	if sessionCount >= maxSessions {
		return nil, fmt.Errorf("node: maximum sessions reached (%d)", maxSessions)
	}

	challenge, err := identity.NewChallenge()
	if err != nil {
		return nil, fmt.Errorf("node: create challenge: %w", err)
	}

	return challenge, nil
}

// AuthenticateUser verifies a challenge response and establishes an
// authenticated session for the user.
func (n *Node) AuthenticateUser(challenge *identity.Challenge, resp *identity.ChallengeResponse) (*Session, error) {
	// Verify the challenge-response
	if err := challenge.Verify(resp, 30*time.Second); err != nil {
		return nil, fmt.Errorf("node: authentication failed: %w", err)
	}

	userIdentity := identity.FromPublicKey(resp.PublicKey)
	userHash := userIdentity.Hash()

	n.mu.Lock()
	defer n.mu.Unlock()

	// Check for existing session
	if existing, ok := n.sessions[userHash]; ok {
		log.Printf("node: user %s reconnecting (replacing existing session)", userHash)
		n.disconnectSessionLocked(existing.UserHash)
	}

	now := time.Now()
	session := &Session{
		UserHash:      userHash,
		Identity:      userIdentity,
		State:         SessionActive,
		ConnectedAt:   now,
		LastHeartbeat: now,
		SeqNum:        message.NewNextSeqNum(0),
	}

	n.sessions[userHash] = session

	log.Printf("node: user %s authenticated and connected (%d active sessions)",
		userHash, len(n.sessions))

	// Broadcast presence announcement
	go n.announceUser(userIdentity)

	// Deliver queued messages
	go n.deliverQueuedMessages(userHash)

	return session, nil
}

// DisconnectUser removes a user session.
func (n *Node) DisconnectUser(userHash identity.Hash) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.disconnectSessionLocked(userHash)
}

// disconnectSessionLocked removes a session (caller must hold n.mu write lock).
func (n *Node) disconnectSessionLocked(userHash identity.Hash) {
	session, ok := n.sessions[userHash]
	if !ok {
		return
	}
	session.State = SessionDisconnecting
	delete(n.sessions, userHash)
	n.presence.Remove(userHash)
	log.Printf("node: user %s disconnected", userHash)

	// Clear the retained presence message on MQTT so other nodes learn
	// this user is offline. Run in goroutine since we hold the lock.
	if n.presenceSender != nil {
		go func() {
			if err := n.presenceSender.ClearPresence(context.Background(), userHash); err != nil {
				log.Printf("node: failed to clear presence for %s: %v", userHash, err)
			}
		}()
	}
}

// Heartbeat updates the last heartbeat time for a user session.
func (n *Node) Heartbeat(userHash identity.Hash) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	session, ok := n.sessions[userHash]
	if !ok {
		return fmt.Errorf("node: no session for user %s", userHash)
	}

	session.LastHeartbeat = time.Now()
	return nil
}

// SendMessage sends a message from a connected user to a destination.
func (n *Node) SendMessage(senderHash identity.Hash, destHash identity.Hash, data []byte) error {
	n.mu.RLock()
	_, ok := n.sessions[senderHash]
	n.mu.RUnlock()

	if !ok {
		return fmt.Errorf("node: sender %s not connected", senderHash)
	}

	pkt := &transport.Packet{
		DestHash:   destHash,
		SourceHash: senderHash,
		Data:       data,
	}

	// Try to route via transport
	if err := n.router.Send(context.Background(), pkt); err != nil {
		// If routing fails, store for later delivery
		log.Printf("node: routing to %s failed, storing for later: %v", destHash, err)
		if _, storeErr := n.store.Enqueue(destHash, data); storeErr != nil {
			return fmt.Errorf("node: send and store failed: route=%v, store=%v", err, storeErr)
		}
	}

	return nil
}

// processIncoming handles packets received from all transports.
func (n *Node) processIncoming(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case pkt, ok := <-n.router.Receive():
			if !ok {
				return
			}
			n.handlePacket(pkt)
		}
	}
}

// handlePacket processes a single incoming transport packet.
func (n *Node) handlePacket(pkt *transport.Packet) {
	// Presence packets are handled separately — they update the presence
	// table rather than being delivered to users.
	if pkt.Type == transport.PacketPresence {
		n.handlePresencePacket(pkt)
		return
	}

	// Alert packets are high-priority broadcasts delivered to all local clients.
	if pkt.Type == transport.PacketAlert {
		n.handleAlertPacket(pkt)
		return
	}

	// Admin packets carry control messages such as OTA update manifests.
	if pkt.Type == transport.PacketAdmin {
		n.handleAdminPacket(pkt)
		return
	}

	// Run inbound interceptor (e.g., bridge remapping) before any processing.
	if n.interceptInbound != nil {
		pkt = n.interceptInbound(pkt)
		if pkt == nil {
			return // interceptor dropped the packet
		}
	}

	n.mu.RLock()
	_, isLocal := n.sessions[pkt.DestHash]
	n.mu.RUnlock()

	if isLocal {
		// Destination user is connected to this node -- deliver directly
		log.Printf("node: delivering message to local user %s via %s", pkt.DestHash, pkt.Via)
		if n.deliverLocal != nil {
			if err := n.deliverLocal(pkt.DestHash, pkt); err != nil {
				log.Printf("node: local delivery to %s failed: %v", pkt.DestHash, err)
			}
		}
	} else if n.deliverBridge != nil && !pkt.DestHash.IsZero() {
		// Try LoRa bridge delivery for users not on WebSocket
		if err := n.deliverBridge(pkt.DestHash, pkt.SourceHash, pkt.Data); err != nil {
			// Bridge delivery failed, store for later
			if _, storeErr := n.store.Enqueue(pkt.DestHash, pkt.Data); storeErr != nil {
				log.Printf("node: failed to store message for %s: %v", pkt.DestHash, storeErr)
			}
		}
	} else if !pkt.DestHash.IsZero() {
		// Store for later delivery
		if _, err := n.store.Enqueue(pkt.DestHash, pkt.Data); err != nil {
			log.Printf("node: failed to store message for %s: %v", pkt.DestHash, err)
		}
	}
}

// handlePresencePacket processes an incoming presence announcement from MQTT.
func (n *Node) handlePresencePacket(pkt *transport.Packet) {
	// Empty payload = offline signal (retained message cleared).
	if len(pkt.Data) == 0 {
		n.presence.Remove(pkt.DestHash)
		return
	}

	annPkt, err := announce.UnmarshalPacket(pkt.Data)
	if err != nil {
		log.Printf("node: failed to unmarshal presence packet: %v", err)
		return
	}

	// Validate signature and timestamp. Use ValidateNodeSigned which
	// accepts either user-signed or node-signed packets.
	if err := annPkt.ValidateNodeSigned(5 * time.Minute); err != nil {
		log.Printf("node: invalid presence packet for %s: %v", annPkt.UserHash, err)
		return
	}

	// Suppress self-echo: don't process presence we published ourselves.
	if annPkt.NodeID == n.identity.Hash() {
		return
	}

	// Update the presence table. The OnChange callback (if set) will fire
	// asynchronously and push the update to WebSocket clients.
	if n.presence.Update(annPkt) {
		log.Printf("node: presence update — user %s is at node %s", annPkt.UserHash, annPkt.NodeID)
	}
}

// handleAlertPacket processes an incoming emergency alert received from the
// transport layer. It deduplicates by alert ID and broadcasts to local
// WebSocket clients.
func (n *Node) handleAlertPacket(pkt *transport.Packet) {
	a, err := alert.Unmarshal(pkt.Data)
	if err != nil {
		log.Printf("node: failed to unmarshal alert: %v", err)
		return
	}

	// Skip expired alerts.
	if alert.IsExpired(a) {
		log.Printf("node: dropping expired alert id=%s", a.ID)
		return
	}

	// Deduplicate: skip if we have already seen this alert ID.
	n.mu.Lock()
	if _, seen := n.seenAlerts[a.ID]; seen {
		n.mu.Unlock()
		return
	}
	n.seenAlerts[a.ID] = time.Now()
	n.mu.Unlock()

	log.Printf("node: received alert id=%s priority=%d from=%s via=%s", a.ID, a.Priority, a.SenderHash, pkt.Via)

	// Push to all locally connected WebSocket clients.
	if n.alertBroadcast != nil {
		n.alertBroadcast(a)
	}
}

// handleAdminPacket processes an incoming admin control message. It attempts
// to parse the data as an update manifest (JSON with a "version" field) and
// passes it to the update manager.
func (n *Node) handleAdminPacket(pkt *transport.Packet) {
	if n.updateManager == nil {
		log.Printf("node: received admin packet but no update manager configured")
		return
	}

	// Quick check: try to detect if this looks like an update manifest by
	// checking for a "version" field in the JSON.
	var probe struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(pkt.Data, &probe); err != nil || probe.Version == "" {
		log.Printf("node: admin packet is not an update manifest, ignoring")
		return
	}

	if err := n.updateManager.CheckUpdate(pkt.Data); err != nil {
		log.Printf("node: update check failed: %v", err)
	}
}

// alertDedupCleanup periodically removes stale entries from the seenAlerts
// deduplication map to prevent unbounded memory growth.
func (n *Node) alertDedupCleanup(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.mu.Lock()
			cutoff := time.Now().Add(-30 * time.Minute)
			for id, ts := range n.seenAlerts {
				if ts.Before(cutoff) {
					delete(n.seenAlerts, id)
				}
			}
			n.mu.Unlock()
		}
	}
}

// announceLoop periodically broadcasts presence announcements for all
// connected users. It reads the power profile on each tick so that
// runtime tier changes take effect without restarting the goroutine.
func (n *Node) announceLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			profile := n.PowerProfile()

			// Respect the power profile's announce setting.
			if !profile.EnableAnnounce {
				continue
			}

			// Reset the ticker interval if the profile specifies a
			// different announce cadence. The ticker is reset on every
			// tick so that tier changes take effect immediately.
			if profile.AnnounceInterval > 0 {
				ticker.Reset(profile.AnnounceInterval)
			}

			n.mu.RLock()
			users := make([]*identity.Identity, 0, len(n.sessions))
			for _, s := range n.sessions {
				users = append(users, s.Identity)
			}
			n.mu.RUnlock()

			for _, user := range users {
				n.announceUser(user)
			}
		}
	}
}

// announceUser broadcasts a presence announcement for a specific user via
// MQTT retained messages. The node signs on the user's behalf since it only
// has the user's public key (not their private key).
func (n *Node) announceUser(user *identity.Identity) {
	pkt, err := announce.NewNodeSigned(n.identity, user.Hash(), user.PublicKey())
	if err != nil {
		log.Printf("node: failed to create announce for %s: %v", user.Hash(), err)
		return
	}

	data, err := pkt.Marshal()
	if err != nil {
		log.Printf("node: failed to marshal announce: %v", err)
		return
	}

	// Publish via the dedicated presence sender (MQTT retained message)
	// rather than the router (which would use msg/ topics with source-hash
	// prefix, corrupting the msgpack payload).
	if n.presenceSender != nil {
		if err := n.presenceSender.SendPresence(context.Background(), user.Hash(), data); err != nil {
			log.Printf("node: failed to publish presence for %s: %v", user.Hash(), err)
		}
	}

	// Also update the local presence table so this node knows about
	// its own users without waiting for the MQTT echo.
	n.presence.Update(pkt)
}

// sessionMonitor checks for timed-out sessions and removes them. It reads
// the power profile on each tick so that runtime tier changes take effect
// without restarting the goroutine.
func (n *Node) sessionMonitor(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			profile := n.PowerProfile()

			n.mu.Lock()
			now := time.Now()
			timeout := profile.SessionTimeout
			var timedOut []identity.Hash

			for hash, session := range n.sessions {
				if now.Sub(session.LastHeartbeat) > timeout {
					timedOut = append(timedOut, hash)
				}
			}

			for _, hash := range timedOut {
				log.Printf("node: session timeout for user %s", hash)
				n.disconnectSessionLocked(hash)
			}
			n.mu.Unlock()
		}
	}
}

// storeCleanup periodically removes expired messages from the store. It reads
// the power profile on each tick so that runtime tier changes take effect
// without restarting the goroutine.
func (n *Node) storeCleanup(ctx context.Context) {
	ticker := time.NewTicker(n.cfg.Store.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			profile := n.PowerProfile()

			// Respect the power profile's cleanup setting.
			if !profile.EnableStoreCleanup {
				continue
			}

			// Adjust the ticker interval if the profile specifies a different
			// cleanup cadence.
			if profile.CleanupInterval > 0 {
				ticker.Reset(profile.CleanupInterval)
			}

			if _, err := n.store.Cleanup(); err != nil {
				log.Printf("node: store cleanup error: %v", err)
			}
		}
	}
}

// deliverQueuedMessages sends stored messages to a newly connected user.
func (n *Node) deliverQueuedMessages(userHash identity.Hash) {
	messages, err := n.store.Dequeue(userHash)
	if err != nil {
		log.Printf("node: failed to dequeue messages for %s: %v", userHash, err)
		return
	}

	if len(messages) == 0 {
		return
	}

	log.Printf("node: delivering %d queued messages to %s", len(messages), userHash)

	for _, msg := range messages {
		if n.deliverLocal != nil {
			pkt := &transport.Packet{
				DestHash: userHash,
				Data:     msg.Data,
				Via:      "store",
			}
			if err := n.deliverLocal(userHash, pkt); err != nil {
				log.Printf("node: queued delivery to %s failed: %v", userHash, err)
				// Stop delivering; remaining messages stay queued for retry.
				return
			}
		}

		// Mark as delivered
		if err := n.store.MarkDelivered(userHash, msg.ID); err != nil {
			log.Printf("node: failed to mark message %d delivered: %v", msg.ID, err)
		}
	}
}

// ActiveSessions returns the number of active user sessions.
func (n *Node) ActiveSessions() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.sessions)
}

// Identity returns the node's identity.
func (n *Node) Identity() *identity.Identity {
	return n.identity
}

// IsUserConnected reports whether a user is connected to this node.
func (n *Node) IsUserConnected(userHash identity.Hash) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	_, ok := n.sessions[userHash]
	return ok
}

// OnlineUsers returns all users currently known to be online across the network.
func (n *Node) OnlineUsers() []announce.Entry {
	return n.presence.AllEntries()
}

// LookupUser returns the presence entry for a specific user, or nil if offline.
func (n *Node) LookupUser(userHash identity.Hash) *announce.Entry {
	return n.presence.Lookup(userHash)
}

// HealthStatus returns a summary of the node's health.
type HealthStatus struct {
	NodeID          string         `json:"node_id"`
	NodeName        string         `json:"node_name"`
	ActiveSessions  int            `json:"active_sessions"`
	TransportHealth map[string]int `json:"transport_health"`
	StoreCount      int            `json:"store_count"`
	Online          bool           `json:"online"`
}

// Health returns the current health status of the node.
func (n *Node) Health() HealthStatus {
	storeCount, _ := n.store.Count()

	return HealthStatus{
		NodeID:          n.identity.Hash().String(),
		NodeName:        n.cfg.Node.Name,
		ActiveSessions:  n.ActiveSessions(),
		TransportHealth: n.router.HealthReport(),
		StoreCount:      storeCount,
		Online:          n.router.IsOnline(),
	}
}
