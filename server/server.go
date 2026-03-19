// Package server implements a WebSocket-based connection server that allows
// users to authenticate with their Ed25519 keypair and send/receive messages
// through the relay node.
//
// Protocol flow:
//  1. Client opens WebSocket to /ws
//  2. Server sends challenge: {"type":"challenge","data":{"nonce":"<hex>","node_id":"<hex>"}}
//  3. Client signs nonce and replies: {"type":"auth_response","data":{"public_key":"<hex>","signature":"<hex>"}}
//  4. Server verifies and replies auth_ok or auth_fail
//  5. Once authenticated, client can send_message and receive messages + presence updates
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/meshtastic-relay/relay/alert"
	"github.com/meshtastic-relay/relay/audit"
	"github.com/meshtastic-relay/relay/bridge"
	"github.com/meshtastic-relay/relay/federation"
	"github.com/meshtastic-relay/relay/identity"
	"github.com/meshtastic-relay/relay/node"
	"github.com/meshtastic-relay/relay/ratelimit"
	"github.com/meshtastic-relay/relay/transport"
)

// -------------------------------------------------------------------
// Protocol message types
// -------------------------------------------------------------------

// ClientMessage is the envelope for all client-to-server WebSocket messages.
type ClientMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// ServerMessage is the envelope for all server-to-client WebSocket messages.
type ServerMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// ChallengeData is sent by the server immediately after a WebSocket connection
// is accepted, prompting the client to prove identity.
type ChallengeData struct {
	Nonce  string `json:"nonce"`
	NodeID string `json:"node_id"`
}

// AuthResponseData is the client's response to a challenge.
type AuthResponseData struct {
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

// AuthOKData is sent after successful authentication.
type AuthOKData struct {
	UserHash  string `json:"user_hash"`
	SessionID string `json:"session_id"`
}

// AuthFailData is sent when authentication fails.
type AuthFailData struct {
	Reason string `json:"reason"`
}

// SendMessageData is sent by the client to deliver a message.
type SendMessageData struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

// InboundMessageData is delivered to the client when a message arrives.
type InboundMessageData struct {
	From      string `json:"from"`
	FromName  string `json:"from_name"`
	Text      string `json:"text"`
	Timestamp int64  `json:"timestamp"`
	ID        string `json:"id"`
}

// PresenceData is pushed when a user comes online or goes offline.
type PresenceData struct {
	UserHash string `json:"user_hash"`
	NodeID   string `json:"node_id"`
	Online   bool   `json:"online"`
}

// HeartbeatData is echoed back on heartbeat.
type HeartbeatData struct {
	Timestamp int64 `json:"timestamp"`
}

// ErrorData is pushed when a server-side error occurs for a client request.
type ErrorData struct {
	Message string `json:"message"`
}

// BridgeRegisterData is sent by the client to register a bridge device.
type BridgeRegisterData struct {
	NodeNum  string `json:"node_num"`
	UserHash string `json:"user_hash"`
	Label    string `json:"label"`
}

// BridgeUnregisterData is sent by the client to unregister a bridge device.
type BridgeUnregisterData struct {
	NodeNum string `json:"node_num"`
}

// BridgeDeviceInfo describes a single bridge device in list responses.
type BridgeDeviceInfo struct {
	NodeNum   string `json:"node_num"`
	UserHash  string `json:"user_hash"`
	Label     string `json:"label"`
	LastHeard string `json:"last_heard"`
	Active    bool   `json:"active"`
}

// BridgeListResponse is the payload of a bridge_list response.
type BridgeListResponse struct {
	Devices []BridgeDeviceInfo `json:"devices"`
}

// BridgeStatusResponse is the payload of a bridge_status response.
type BridgeStatusResponse struct {
	Enabled     bool `json:"enabled"`
	DeviceCount int  `json:"device_count"`
	ActiveCount int  `json:"active_count"`
}

// WhoOnlineUser describes a single online user in the who_online response.
type WhoOnlineUser struct {
	UserHash string `json:"user_hash"`
	NodeID   string `json:"node_id"`
	Since    int64  `json:"since"`
}

// WhoOnlineResponse is the payload of a who_online response.
type WhoOnlineResponse struct {
	Users []WhoOnlineUser `json:"users"`
	Count int             `json:"count"`
}

// LookupUserRequest is the payload of a lookup_user request.
type LookupUserRequest struct {
	UserHash string `json:"user_hash"`
}

// LookupUserResponse is the payload of a lookup_user response.
type LookupUserResponse struct {
	UserHash string `json:"user_hash"`
	Online   bool   `json:"online"`
	NodeID   string `json:"node_id,omitempty"`
	Since    int64  `json:"since,omitempty"`
	DID      string `json:"did,omitempty"`
	Handle   string `json:"handle,omitempty"`
}

// BindIdentityRequest is the payload of a bind_identity request.
type BindIdentityRequest struct {
	DID         string `json:"did"`
	AccessToken string `json:"access_token"`
}

// BindOKData is the payload of a bind_ok response.
type BindOKData struct {
	DID    string `json:"did"`
	Handle string `json:"handle"`
}

// BindFailData is the payload of a bind_fail response.
type BindFailData struct {
	Reason string `json:"reason"`
}

// ResolveIdentityRequest is the payload of a resolve_identity request.
type ResolveIdentityRequest struct {
	UserHash string `json:"user_hash"`
}

// IdentityInfoData is the payload of an identity_info response.
type IdentityInfoData struct {
	UserHash string `json:"user_hash"`
	DID      string `json:"did,omitempty"`
	Handle   string `json:"handle,omitempty"`
}

// SendAlertData is sent by the client to broadcast an emergency alert.
type SendAlertData struct {
	Text     string `json:"text"`
	Priority int    `json:"priority"`
}

// AlertSentData is returned to confirm an alert was broadcast.
type AlertSentData struct {
	AlertID string `json:"alert_id"`
}

// AlertData is pushed to all connected clients when an alert arrives.
type AlertData struct {
	ID        string `json:"id"`
	Priority  int    `json:"priority"`
	From      string `json:"from"`
	NodeID    string `json:"node_id"`
	Text      string `json:"text"`
	Timestamp int64  `json:"timestamp"`
}

// PowerStatusResponse is the payload of a power_status response.
type PowerStatusResponse struct {
	Tier             string `json:"tier"`
	AnnounceInterval string `json:"announce_interval"`
	MaxSessions      int    `json:"max_sessions"`
	SessionsActive   int    `json:"sessions_active"`
}

// UpdateStatusResponse is the payload of an update_status response.
type UpdateStatusResponse struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Status         string `json:"status"`
	LastCheck      int64  `json:"last_check"`
	Error          string `json:"error,omitempty"`
}

// -------------------------------------------------------------------
// UserConn
// -------------------------------------------------------------------

// UserConn represents a single authenticated WebSocket user connection.
type UserConn struct {
	conn     *websocket.Conn
	identity *identity.Identity
	userHash identity.Hash
	send     chan []byte
	done     chan struct{}
}

// -------------------------------------------------------------------
// Server
// -------------------------------------------------------------------

// Server manages WebSocket connections, maps authenticated users to their
// connections, and acts as the bridge between the node daemon and clients.
type Server struct {
	node        *node.Node
	addr        string
	sessions    sync.Map // identity.Hash -> *UserConn
	upgrader    websocket.Upgrader
	srv         *http.Server
	registry    *bridge.Registry   // nil if bridge not enabled
	rateLimiter *ratelimit.Limiter // nil if rate limiting not configured
	auditLog    *audit.Logger      // nil if audit logging not configured
	federation  *federation.Store  // nil if federation not enabled
}

// New creates a new WebSocket server bound to addr, backed by the given node.
func New(addr string, n *node.Node) *Server {
	s := &Server{
		node: n,
		addr: addr,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
	return s
}

// SetRegistry sets the bridge registry for the server, enabling bridge
// management commands over WebSocket.
func (s *Server) SetRegistry(reg *bridge.Registry) {
	s.registry = reg
}

// SetRateLimiter configures per-user rate limiting for WebSocket messages.
func (s *Server) SetRateLimiter(rl *ratelimit.Limiter) {
	s.rateLimiter = rl
}

// SetAuditLogger configures structured security event logging.
func (s *Server) SetAuditLogger(al *audit.Logger) {
	s.auditLog = al
}

// SetFederationStore configures the OpenFederation identity store, enabling
// DID binding and resolution over WebSocket.
func (s *Server) SetFederationStore(fs *federation.Store) {
	s.federation = fs
}

// Start begins listening for WebSocket connections. It blocks until the
// context is canceled or the listener encounters a fatal error.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)

	s.srv = &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Run the HTTP server in a goroutine; shut it down on context cancellation.
	errCh := make(chan error, 1)
	go func() {
		log.Printf("server: listening on %s", s.addr)
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.srv.Shutdown(shutCtx); err != nil {
			log.Printf("server: shutdown error: %v", err)
		}
	}()

	// Give the server a moment to fail fast on bind errors.
	select {
	case err := <-errCh:
		return fmt.Errorf("server: listen %s: %w", s.addr, err)
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

// Stop gracefully shuts down the HTTP/WebSocket server.
func (s *Server) Stop() error {
	if s.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

// -------------------------------------------------------------------
// DeliverToUser -- called by the node when a message arrives for a
// local user.
// -------------------------------------------------------------------

// DeliverToUser sends a transport packet to a connected user's WebSocket.
// It satisfies the node.DeliveryFunc signature.
func (s *Server) DeliverToUser(userHash identity.Hash, pkt *transport.Packet) error {
	val, ok := s.sessions.Load(userHash)
	if !ok {
		return fmt.Errorf("server: user %s not connected", userHash)
	}
	uc := val.(*UserConn)

	msg := ServerMessage{
		Type: "message",
		Data: InboundMessageData{
			From:      pkt.SourceHash.String(),
			FromName:  "", // Name resolution could be added later
			Text:      string(pkt.Data),
			Timestamp: time.Now().Unix(),
			ID:        generateID(),
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("server: marshal message: %w", err)
	}

	select {
	case uc.send <- data:
		return nil
	default:
		return fmt.Errorf("server: send buffer full for user %s", userHash)
	}
}

// BroadcastPresence pushes a presence update to all connected users.
func (s *Server) BroadcastPresence(userHash identity.Hash, nodeID identity.Hash, online bool) {
	msg := ServerMessage{
		Type: "presence",
		Data: PresenceData{
			UserHash: userHash.String(),
			NodeID:   nodeID.String(),
			Online:   online,
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("server: marshal presence: %v", err)
		return
	}

	s.sessions.Range(func(_, val interface{}) bool {
		uc := val.(*UserConn)
		select {
		case uc.send <- data:
		default:
			log.Printf("server: presence send buffer full for %s", uc.userHash)
		}
		return true
	})
}

// BroadcastAlert pushes an emergency alert to all connected WebSocket clients.
func (s *Server) BroadcastAlert(a *alert.Alert) {
	msg := ServerMessage{
		Type: "alert",
		Data: AlertData{
			ID:        a.ID,
			Priority:  int(a.Priority),
			From:      a.SenderHash,
			NodeID:    a.NodeID,
			Text:      a.Text,
			Timestamp: a.Timestamp,
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("server: marshal alert: %v", err)
		return
	}

	s.sessions.Range(func(_, val interface{}) bool {
		uc := val.(*UserConn)
		select {
		case uc.send <- data:
		default:
			log.Printf("server: alert send buffer full for %s", uc.userHash)
		}
		return true
	})
}

// -------------------------------------------------------------------
// WebSocket handler
// -------------------------------------------------------------------

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("server: websocket upgrade: %v", err)
		return
	}
	defer conn.Close()

	// Step 1: Generate challenge via the node
	challenge, err := s.node.ConnectUser()
	if err != nil {
		s.writeJSON(conn, ServerMessage{Type: "error", Data: ErrorData{Message: err.Error()}})
		return
	}

	// Step 2: Send challenge to client
	s.writeJSON(conn, ServerMessage{
		Type: "challenge",
		Data: ChallengeData{
			Nonce:  hex.EncodeToString(challenge.Nonce[:]),
			NodeID: s.node.Identity().Hash().String(),
		},
	})

	// Step 3: Wait for auth_response with a deadline
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	var clientMsg ClientMessage
	if err := conn.ReadJSON(&clientMsg); err != nil {
		log.Printf("server: read auth response: %v", err)
		return
	}
	if clientMsg.Type != "auth_response" {
		s.writeJSON(conn, ServerMessage{
			Type: "auth_fail",
			Data: AuthFailData{Reason: "expected auth_response"},
		})
		return
	}

	var authResp AuthResponseData
	if err := json.Unmarshal(clientMsg.Data, &authResp); err != nil {
		s.writeJSON(conn, ServerMessage{
			Type: "auth_fail",
			Data: AuthFailData{Reason: "invalid auth_response data"},
		})
		return
	}

	pubKeyBytes, err := hex.DecodeString(authResp.PublicKey)
	if err != nil {
		s.writeJSON(conn, ServerMessage{
			Type: "auth_fail",
			Data: AuthFailData{Reason: "invalid public_key hex"},
		})
		return
	}

	sigBytes, err := hex.DecodeString(authResp.Signature)
	if err != nil {
		s.writeJSON(conn, ServerMessage{
			Type: "auth_fail",
			Data: AuthFailData{Reason: "invalid signature hex"},
		})
		return
	}

	// Build the ChallengeResponse expected by the node
	challengeResp := &identity.ChallengeResponse{
		Nonce:     challenge.Nonce,
		Signature: sigBytes,
		PublicKey: pubKeyBytes,
	}

	// Step 4: Authenticate through the node
	session, err := s.node.AuthenticateUser(challenge, challengeResp)
	if err != nil {
		if s.auditLog != nil {
			s.auditLog.Log(audit.EventAuthFailure, "", r.RemoteAddr, err.Error(), false)
		}
		s.writeJSON(conn, ServerMessage{
			Type: "auth_fail",
			Data: AuthFailData{Reason: err.Error()},
		})
		return
	}

	// Create session ID
	sessionID := generateID()

	// Build user connection
	uc := &UserConn{
		conn:     conn,
		identity: session.Identity,
		userHash: session.UserHash,
		send:     make(chan []byte, 64),
		done:     make(chan struct{}),
	}

	// Evict existing WebSocket connection for the same user if any.
	if prev, loaded := s.sessions.LoadAndDelete(session.UserHash); loaded {
		prevUC := prev.(*UserConn)
		close(prevUC.done)
	}
	s.sessions.Store(session.UserHash, uc)

	// Send auth_ok
	s.writeJSON(conn, ServerMessage{
		Type: "auth_ok",
		Data: AuthOKData{
			UserHash:  session.UserHash.String(),
			SessionID: sessionID,
		},
	})

	log.Printf("server: user %s authenticated (session %s)", session.UserHash, sessionID)
	if s.auditLog != nil {
		s.auditLog.Log(audit.EventAuthSuccess, session.UserHash.String(), r.RemoteAddr, fmt.Sprintf("session=%s", sessionID), true)
	}

	// Clear the auth deadline; the read/write pumps manage their own deadlines.
	conn.SetReadDeadline(time.Time{})

	// Launch the write pump; the read pump runs in this goroutine.
	go s.writePump(uc)
	s.readPump(uc)

	// Cleanup on disconnect
	s.sessions.Delete(session.UserHash)
	close(uc.done)
	s.node.DisconnectUser(session.UserHash)
	if s.auditLog != nil {
		s.auditLog.Log(audit.EventSessionEnd, session.UserHash.String(), r.RemoteAddr, "disconnect", true)
	}
	log.Printf("server: user %s disconnected", session.UserHash)
}

// -------------------------------------------------------------------
// Read/write pumps
// -------------------------------------------------------------------

const (
	writeWait  = 10 * time.Second
	pongWait   = 120 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

// readPump reads messages from the WebSocket and dispatches them.
func (s *Server) readPump(uc *UserConn) {
	uc.conn.SetReadLimit(64 * 1024)
	uc.conn.SetReadDeadline(time.Now().Add(pongWait))
	uc.conn.SetPongHandler(func(string) error {
		uc.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		var msg ClientMessage
		if err := uc.conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("server: read error for %s: %v", uc.userHash, err)
			}
			return
		}

		// Rate limit: check per-user token bucket before processing.
		if s.rateLimiter != nil && !s.rateLimiter.Allow(uc.userHash.String()) {
			s.sendToUser(uc, ServerMessage{
				Type: "error",
				Data: ErrorData{Message: "rate limit exceeded, slow down"},
			})
			if s.auditLog != nil {
				s.auditLog.Log(audit.EventRateLimit, uc.userHash.String(), "", fmt.Sprintf("type=%s", msg.Type), false)
			}
			continue
		}

		switch msg.Type {
		case "send_message":
			s.handleSendMessage(uc, msg.Data)
		case "send_alert":
			s.handleSendAlert(uc, msg.Data)
		case "heartbeat":
			s.handleHeartbeat(uc)
		case "bridge_register":
			s.handleBridgeRegister(uc, msg.Data)
		case "bridge_unregister":
			s.handleBridgeUnregister(uc, msg.Data)
		case "bridge_list":
			s.handleBridgeList(uc)
		case "bridge_status":
			s.handleBridgeStatus(uc)
		case "who_online":
			s.handleWhoOnline(uc)
		case "lookup_user":
			s.handleLookupUser(uc, msg.Data)
		case "power_status":
			s.handlePowerStatus(uc)
		case "update_status":
			s.handleUpdateStatus(uc)
		case "bind_identity":
			s.handleBindIdentity(uc, msg.Data)
		case "resolve_identity":
			s.handleResolveIdentity(uc, msg.Data)
		default:
			s.sendToUser(uc, ServerMessage{
				Type: "error",
				Data: ErrorData{Message: fmt.Sprintf("unknown message type: %s", msg.Type)},
			})
		}
	}
}

// writePump writes queued messages and periodic pings to the WebSocket.
func (s *Server) writePump(uc *UserConn) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case data, ok := <-uc.send:
			if !ok {
				// Channel closed, connection is done.
				return
			}
			uc.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := uc.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Printf("server: write error for %s: %v", uc.userHash, err)
				return
			}
		case <-ticker.C:
			uc.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := uc.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-uc.done:
			return
		}
	}
}

// -------------------------------------------------------------------
// Message handlers
// -------------------------------------------------------------------

func (s *Server) handleSendMessage(uc *UserConn, raw json.RawMessage) {
	var req SendMessageData
	if err := json.Unmarshal(raw, &req); err != nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "invalid send_message data"},
		})
		return
	}

	if strings.TrimSpace(req.Text) == "" {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "message text cannot be empty"},
		})
		return
	}
	if len(req.Text) > 4096 {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "message text exceeds 4096 byte limit"},
		})
		return
	}

	destHash, err := identity.HashFromHex(req.To)
	if err != nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "invalid destination hash"},
		})
		return
	}

	if err := s.node.SendMessage(uc.userHash, destHash, []byte(req.Text)); err != nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: fmt.Sprintf("send failed: %v", err)},
		})
		return
	}
}

func (s *Server) handleSendAlert(uc *UserConn, raw json.RawMessage) {
	var req SendAlertData
	if err := json.Unmarshal(raw, &req); err != nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "invalid send_alert data"},
		})
		return
	}

	if strings.TrimSpace(req.Text) == "" {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "alert text cannot be empty"},
		})
		return
	}
	if len(req.Text) > 1024 {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "alert text exceeds 1024 byte limit"},
		})
		return
	}

	priority := alert.Priority(req.Priority)
	if priority < alert.PriorityCritical || priority > alert.PriorityNormal {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "invalid alert priority (must be 0, 1, or 2)"},
		})
		return
	}

	a, err := s.node.BroadcastAlert(uc.userHash, req.Text, priority)
	if err != nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: fmt.Sprintf("alert broadcast failed: %v", err)},
		})
		return
	}

	if s.auditLog != nil {
		s.auditLog.Log(audit.EventAlertBroadcast, uc.userHash.String(), "", fmt.Sprintf("alert_id=%s priority=%d", a.ID, req.Priority), true)
	}
	s.sendToUser(uc, ServerMessage{
		Type: "alert_sent",
		Data: AlertSentData{AlertID: a.ID},
	})
}

func (s *Server) handleHeartbeat(uc *UserConn) {
	if err := s.node.Heartbeat(uc.userHash); err != nil {
		log.Printf("server: heartbeat error for %s: %v", uc.userHash, err)
	}
	s.sendToUser(uc, ServerMessage{
		Type: "heartbeat",
		Data: HeartbeatData{Timestamp: time.Now().Unix()},
	})
}

// -------------------------------------------------------------------
// Bridge management handlers
// -------------------------------------------------------------------

func (s *Server) handleBridgeRegister(uc *UserConn, raw json.RawMessage) {
	if s.registry == nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "bridge not enabled"},
		})
		return
	}

	var req BridgeRegisterData
	if err := json.Unmarshal(raw, &req); err != nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "invalid bridge_register data"},
		})
		return
	}

	if len(req.Label) > 64 {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "bridge label exceeds 64 byte limit"},
		})
		return
	}

	nodeNum, err := parseNodeNum(req.NodeNum)
	if err != nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: fmt.Sprintf("invalid node_num: %v", err)},
		})
		return
	}

	userHash, err := identity.HashFromHex(req.UserHash)
	if err != nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: fmt.Sprintf("invalid user_hash: %v", err)},
		})
		return
	}

	reg := bridge.DeviceRegistration{
		MeshNodeNum:  nodeNum,
		UserHash:     userHash,
		Label:        req.Label,
		RegisteredAt: time.Now(),
	}
	if err := s.registry.Register(reg); err != nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: fmt.Sprintf("register failed: %v", err)},
		})
		return
	}

	log.Printf("server: bridge device 0x%08X registered by %s", nodeNum, uc.userHash)
	if s.auditLog != nil {
		s.auditLog.Log(audit.EventBridgeRegister, uc.userHash.String(), "", fmt.Sprintf("node_num=0x%08X label=%s", nodeNum, req.Label), true)
	}
	s.sendToUser(uc, ServerMessage{
		Type: "bridge_register_ok",
		Data: map[string]string{
			"node_num": fmt.Sprintf("0x%08X", nodeNum),
			"label":    req.Label,
		},
	})
}

func (s *Server) handleBridgeUnregister(uc *UserConn, raw json.RawMessage) {
	if s.registry == nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "bridge not enabled"},
		})
		return
	}

	var req BridgeUnregisterData
	if err := json.Unmarshal(raw, &req); err != nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "invalid bridge_unregister data"},
		})
		return
	}

	nodeNum, err := parseNodeNum(req.NodeNum)
	if err != nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: fmt.Sprintf("invalid node_num: %v", err)},
		})
		return
	}

	s.registry.Unregister(nodeNum)

	log.Printf("server: bridge device 0x%08X unregistered by %s", nodeNum, uc.userHash)
	if s.auditLog != nil {
		s.auditLog.Log(audit.EventBridgeUnregister, uc.userHash.String(), "", fmt.Sprintf("node_num=0x%08X", nodeNum), true)
	}
	s.sendToUser(uc, ServerMessage{
		Type: "bridge_unregister_ok",
		Data: map[string]string{
			"node_num": fmt.Sprintf("0x%08X", nodeNum),
		},
	})
}

func (s *Server) handleBridgeList(uc *UserConn) {
	if s.registry == nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "bridge not enabled"},
		})
		return
	}

	regs := s.registry.AllRegistrations()
	devices := make([]BridgeDeviceInfo, 0, len(regs))
	for _, reg := range regs {
		info := BridgeDeviceInfo{
			NodeNum:  fmt.Sprintf("0x%08X", reg.MeshNodeNum),
			UserHash: reg.UserHash.String(),
			Label:    reg.Label,
		}

		// Check device state for last-heard and active status.
		state := s.registry.LookupByNodeNum(reg.MeshNodeNum)
		if state != nil && !state.LastHeard.IsZero() {
			info.LastHeard = state.LastHeard.UTC().Format(time.RFC3339)
		}
		active := s.registry.ActiveDeviceForUser(reg.UserHash)
		if active != nil && active.MeshNodeNum == reg.MeshNodeNum {
			info.Active = true
		}

		devices = append(devices, info)
	}

	s.sendToUser(uc, ServerMessage{
		Type: "bridge_list",
		Data: BridgeListResponse{Devices: devices},
	})
}

func (s *Server) handleBridgeStatus(uc *UserConn) {
	if s.registry == nil {
		s.sendToUser(uc, ServerMessage{
			Type: "bridge_status",
			Data: BridgeStatusResponse{
				Enabled:     false,
				DeviceCount: 0,
				ActiveCount: 0,
			},
		})
		return
	}

	regs := s.registry.AllRegistrations()

	// Count unique active users (each user can have at most one active device).
	activeUsers := make(map[identity.Hash]bool)
	for _, reg := range regs {
		if _, seen := activeUsers[reg.UserHash]; !seen {
			active := s.registry.ActiveDeviceForUser(reg.UserHash)
			activeUsers[reg.UserHash] = active != nil
		}
	}

	activeCount := 0
	for _, isActive := range activeUsers {
		if isActive {
			activeCount++
		}
	}

	s.sendToUser(uc, ServerMessage{
		Type: "bridge_status",
		Data: BridgeStatusResponse{
			Enabled:     true,
			DeviceCount: len(regs),
			ActiveCount: activeCount,
		},
	})
}

// -------------------------------------------------------------------
// Contact list / user discovery handlers
// -------------------------------------------------------------------

func (s *Server) handleWhoOnline(uc *UserConn) {
	entries := s.node.OnlineUsers()

	users := make([]WhoOnlineUser, 0, len(entries))
	for _, e := range entries {
		users = append(users, WhoOnlineUser{
			UserHash: e.UserHash.String(),
			NodeID:   e.NodeID.String(),
			Since:    e.Timestamp,
		})
	}

	s.sendToUser(uc, ServerMessage{
		Type: "who_online",
		Data: WhoOnlineResponse{
			Users: users,
			Count: len(users),
		},
	})
}

func (s *Server) handleLookupUser(uc *UserConn, raw json.RawMessage) {
	var req LookupUserRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "invalid lookup_user data"},
		})
		return
	}

	userHash, err := identity.HashFromHex(req.UserHash)
	if err != nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: fmt.Sprintf("invalid user_hash: %v", err)},
		})
		return
	}

	entry := s.node.LookupUser(userHash)

	resp := LookupUserResponse{
		UserHash: req.UserHash,
	}

	if entry != nil {
		resp.Online = true
		resp.NodeID = entry.NodeID.String()
		resp.Since = entry.Timestamp
	}

	// Enrich with federation identity if available.
	if s.federation != nil {
		if profile := s.federation.LookupByHash(req.UserHash); profile != nil {
			resp.DID = profile.DID
			resp.Handle = profile.Handle
		}
	}

	s.sendToUser(uc, ServerMessage{
		Type: "lookup_user",
		Data: resp,
	})
}

// -------------------------------------------------------------------
// Power management handler
// -------------------------------------------------------------------

func (s *Server) handlePowerStatus(uc *UserConn) {
	profile := s.node.PowerProfile()

	announceStr := profile.AnnounceInterval.String()
	if !profile.EnableAnnounce {
		announceStr = "disabled"
	}

	s.sendToUser(uc, ServerMessage{
		Type: "power_status",
		Data: PowerStatusResponse{
			Tier:             profile.Tier.String(),
			AnnounceInterval: announceStr,
			MaxSessions:      profile.MaxSessions,
			SessionsActive:   s.node.ActiveSessions(),
		},
	})
}

// -------------------------------------------------------------------
// Update status handler
// -------------------------------------------------------------------

func (s *Server) handleUpdateStatus(uc *UserConn) {
	info := s.node.UpdateStatus()

	s.sendToUser(uc, ServerMessage{
		Type: "update_status",
		Data: UpdateStatusResponse{
			CurrentVersion: info.CurrentVersion,
			LatestVersion:  info.LatestVersion,
			Status:         info.Status.String(),
			LastCheck:      info.LastCheck,
			Error:          info.Error,
		},
	})
}

// -------------------------------------------------------------------
// Federation identity handlers
// -------------------------------------------------------------------

func (s *Server) handleBindIdentity(uc *UserConn, raw json.RawMessage) {
	if s.federation == nil {
		s.sendToUser(uc, ServerMessage{
			Type: "bind_fail",
			Data: BindFailData{Reason: "federation not enabled"},
		})
		return
	}

	var req BindIdentityRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		s.sendToUser(uc, ServerMessage{
			Type: "bind_fail",
			Data: BindFailData{Reason: "invalid bind_identity data"},
		})
		return
	}

	if req.DID == "" || req.AccessToken == "" {
		s.sendToUser(uc, ServerMessage{
			Type: "bind_fail",
			Data: BindFailData{Reason: "did and access_token are required"},
		})
		return
	}

	profile, err := s.federation.VerifyAndBind(uc.userHash.String(), req.AccessToken, req.DID)
	if err != nil {
		log.Printf("server: bind_identity failed for %s: %v", uc.userHash, err)
		s.sendToUser(uc, ServerMessage{
			Type: "bind_fail",
			Data: BindFailData{Reason: err.Error()},
		})
		return
	}

	log.Printf("server: user %s bound to DID %s (handle %s)", uc.userHash, profile.DID, profile.Handle)
	s.sendToUser(uc, ServerMessage{
		Type: "bind_ok",
		Data: BindOKData{
			DID:    profile.DID,
			Handle: profile.Handle,
		},
	})
}

func (s *Server) handleResolveIdentity(uc *UserConn, raw json.RawMessage) {
	if s.federation == nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "federation not enabled"},
		})
		return
	}

	var req ResolveIdentityRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		s.sendToUser(uc, ServerMessage{
			Type: "error",
			Data: ErrorData{Message: "invalid resolve_identity data"},
		})
		return
	}

	resp := IdentityInfoData{
		UserHash: req.UserHash,
	}

	if profile := s.federation.LookupByHash(req.UserHash); profile != nil {
		resp.DID = profile.DID
		resp.Handle = profile.Handle
	}

	s.sendToUser(uc, ServerMessage{
		Type: "identity_info",
		Data: resp,
	})
}

// parseNodeNum parses a Meshtastic node number from a string that may be
// decimal ("439041101") or hexadecimal with 0x prefix ("0x1A2B3C4D").
func parseNodeNum(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		n, err := strconv.ParseUint(s[2:], 16, 32)
		if err != nil {
			return 0, fmt.Errorf("parse hex node num: %w", err)
		}
		return uint32(n), nil
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse decimal node num: %w", err)
	}
	return uint32(n), nil
}

// -------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------

func (s *Server) sendToUser(uc *UserConn, msg ServerMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("server: marshal error: %v", err)
		return
	}
	select {
	case uc.send <- data:
	default:
		log.Printf("server: send buffer full for %s, dropping", uc.userHash)
	}
}

func (s *Server) writeJSON(conn *websocket.Conn, msg ServerMessage) {
	conn.SetWriteDeadline(time.Now().Add(writeWait))
	if err := conn.WriteJSON(msg); err != nil {
		log.Printf("server: writeJSON error: %v", err)
	}
}

// generateID produces a random 16-byte hex string for session/message IDs.
func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if rand fails (should never happen).
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
