// Command relay-cli is an interactive CLI client for the meshtastic-relay
// network. It connects to a relay node via WebSocket, authenticates with an
// Ed25519 identity, and provides a simple command interface for sending and
// receiving text messages.
package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/meshtastic-relay/relay/identity"
)

// Protocol message types.
const (
	typeChallenge    = "challenge"
	typeAuthResponse = "auth_response"
	typeAuthOK       = "auth_ok"
	typeAuthFail     = "auth_fail"
	typeSendMessage  = "send_message"
	typeMessage      = "message"
	typePresence     = "presence"
	typeHeartbeat    = "heartbeat"

	// Bridge management message types.
	typeBridgeRegister     = "bridge_register"
	typeBridgeUnregister   = "bridge_unregister"
	typeBridgeList         = "bridge_list"
	typeBridgeStatus       = "bridge_status"
	typeBridgeRegisterOK   = "bridge_register_ok"
	typeBridgeUnregisterOK = "bridge_unregister_ok"

	// Contact list / user discovery message types.
	typeWhoOnline  = "who_online"
	typeLookupUser = "lookup_user"

	// Alert message types.
	typeSendAlert = "send_alert"
	typeAlert     = "alert"
	typeAlertSent = "alert_sent"

	// Power management message types.
	typePowerStatus = "power_status"

	// Update management message types.
	typeUpdateStatus = "update_status"
)

// envelope is the top-level JSON wire format for all WebSocket messages.
type envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// challengeData is the payload of a "challenge" message from the server.
type challengeData struct {
	Nonce  string `json:"nonce"`
	NodeID string `json:"node_id"`
}

// authResponseData is the payload we send back to the server.
type authResponseData struct {
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

// authOKData is the payload of a successful auth acknowledgement.
type authOKData struct {
	UserHash string `json:"user_hash"`
}

// authFailData is the payload of a failed auth acknowledgement.
type authFailData struct {
	Reason string `json:"reason"`
}

// sendMessageData is the payload we send to deliver a text message.
type sendMessageData struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

// messageData is the payload of an incoming text message.
type messageData struct {
	From      string `json:"from"`
	Text      string `json:"text"`
	Timestamp int64  `json:"timestamp"`
}

// presenceData is the payload of a presence update.
type presenceData struct {
	UserHash string `json:"user_hash"`
	NodeID   string `json:"node_id"`
	Online   bool   `json:"online"`
}

// bridgeDeviceInfo describes a bridge device in list responses.
type bridgeDeviceInfo struct {
	NodeNum   string `json:"node_num"`
	UserHash  string `json:"user_hash"`
	Label     string `json:"label"`
	LastHeard string `json:"last_heard"`
	Active    bool   `json:"active"`
}

// bridgeListData is the payload of a bridge_list response.
type bridgeListData struct {
	Devices []bridgeDeviceInfo `json:"devices"`
}

// bridgeStatusData is the payload of a bridge_status response.
type bridgeStatusData struct {
	Enabled     bool `json:"enabled"`
	DeviceCount int  `json:"device_count"`
	ActiveCount int  `json:"active_count"`
}

// bridgeRegisterOKData is the payload of a bridge_register_ok response.
type bridgeRegisterOKData struct {
	NodeNum string `json:"node_num"`
	Label   string `json:"label"`
}

// bridgeUnregisterOKData is the payload of a bridge_unregister_ok response.
type bridgeUnregisterOKData struct {
	NodeNum string `json:"node_num"`
}

// whoOnlineUser describes a single online user in the who_online response.
type whoOnlineUser struct {
	UserHash string `json:"user_hash"`
	NodeID   string `json:"node_id"`
	Since    int64  `json:"since"`
}

// whoOnlineData is the payload of a who_online response.
type whoOnlineData struct {
	Users []whoOnlineUser `json:"users"`
	Count int             `json:"count"`
}

// lookupUserData is the payload of a lookup_user response.
type lookupUserData struct {
	UserHash string `json:"user_hash"`
	Online   bool   `json:"online"`
	NodeID   string `json:"node_id,omitempty"`
	Since    int64  `json:"since,omitempty"`
}

// alertData is the payload of an incoming emergency alert.
type alertData struct {
	ID        string `json:"id"`
	Priority  int    `json:"priority"`
	From      string `json:"from"`
	NodeID    string `json:"node_id"`
	Text      string `json:"text"`
	Timestamp int64  `json:"timestamp"`
}

// alertSentData is the payload of an alert_sent confirmation.
type alertSentData struct {
	AlertID string `json:"alert_id"`
}

// powerStatusData is the payload of a power_status response.
type powerStatusData struct {
	Tier             string `json:"tier"`
	AnnounceInterval string `json:"announce_interval"`
	MaxSessions      int    `json:"max_sessions"`
	SessionsActive   int    `json:"sessions_active"`
}

// updateStatusData is the payload of an update_status response.
type updateStatusData struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Status         string `json:"status"`
	LastCheck      int64  `json:"last_check"`
	Error          string `json:"error"`
}

// errorData is the payload of an error response from the server.
type errorData struct {
	Message string `json:"message"`
}

// contact tracks a peer discovered through presence updates.
type contact struct {
	UserHash string
	NodeID   string
	Online   bool
}

// client holds all state for the CLI session.
type client struct {
	id       *identity.Identity
	conn     *websocket.Conn
	server   string
	name     string
	authed   bool
	userHash string

	mu       sync.Mutex
	contacts map[string]*contact // keyed by user hash hex

	done     chan struct{}
	authDone chan struct{} // closed when auth completes (success or failure)
}

func main() {
	defaultIdentity := filepath.Join(homeDir(), ".relay", "identity.key")

	server := flag.String("server", "ws://localhost:9370/ws", "Relay node WebSocket address")
	identityPath := flag.String("identity", defaultIdentity, "Path to Ed25519 identity key file")
	name := flag.String("name", hostname(), "Display name")
	flag.Parse()

	// Ensure the directory for the identity file exists.
	if err := os.MkdirAll(filepath.Dir(*identityPath), 0700); err != nil {
		log.Fatalf("Failed to create identity directory: %v", err)
	}

	id, err := identity.LoadOrGenerate(*identityPath)
	if err != nil {
		log.Fatalf("Failed to load/generate identity: %v", err)
	}

	fmt.Println("relay-cli")
	fmt.Printf("  identity : %s\n", id.Hash())
	fmt.Printf("  key file : %s\n", *identityPath)
	fmt.Printf("  server   : %s\n", *server)
	fmt.Printf("  name     : %s\n", *name)
	fmt.Println()

	c := &client{
		id:       id,
		server:   *server,
		name:     *name,
		contacts: make(map[string]*contact),
		done:     make(chan struct{}),
		authDone: make(chan struct{}),
	}

	if err := c.connect(); err != nil {
		log.Fatalf("Connection failed: %v", err)
	}

	c.run()
}

// connect dials the WebSocket server.
func (c *client) connect() error {
	fmt.Printf("Connecting to %s ...\n", c.server)

	conn, _, err := websocket.DefaultDialer.Dial(c.server, nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}
	c.conn = conn
	fmt.Println("Connected. Waiting for auth challenge...")
	return nil
}

// run starts the read loop, heartbeat, and interactive input. It blocks until
// the session ends.
func (c *client) run() {
	// Goroutine: read messages from the WebSocket.
	go c.readLoop()

	// Goroutine: send heartbeats every 30 seconds.
	go c.heartbeatLoop()

	// Handle Ctrl-C gracefully.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		fmt.Println("\nInterrupted. Disconnecting...")
		c.close()
		os.Exit(0)
	}()

	// Block on interactive input (runs on main goroutine).
	c.inputLoop()
}

// readLoop reads JSON messages from the WebSocket and dispatches them.
func (c *client) readLoop() {
	defer func() {
		close(c.done)
	}()

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				fmt.Println("\nServer closed the connection.")
			} else {
				select {
				case <-c.done:
					return
				default:
					fmt.Printf("\nConnection error: %v\n", err)
				}
			}
			return
		}

		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			fmt.Printf("\nMalformed message from server: %v\n", err)
			continue
		}

		c.handleMessage(env)
	}
}

// handleMessage dispatches a decoded envelope to the appropriate handler.
func (c *client) handleMessage(env envelope) {
	switch env.Type {
	case typeChallenge:
		c.handleChallenge(env.Data)
	case typeAuthOK:
		c.handleAuthOK(env.Data)
	case typeAuthFail:
		c.handleAuthFail(env.Data)
	case typeMessage:
		c.handleIncomingMessage(env.Data)
	case typePresence:
		c.handlePresence(env.Data)
	case typeHeartbeat:
		// Server acknowledged our heartbeat; nothing to display.
	case typeBridgeRegisterOK:
		c.handleBridgeRegisterOK(env.Data)
	case typeBridgeUnregisterOK:
		c.handleBridgeUnregisterOK(env.Data)
	case typeBridgeList:
		c.handleBridgeListResponse(env.Data)
	case typeBridgeStatus:
		c.handleBridgeStatusResponse(env.Data)
	case typeWhoOnline:
		c.handleWhoOnlineResponse(env.Data)
	case typeLookupUser:
		c.handleLookupUserResponse(env.Data)
	case typeAlert:
		c.handleAlert(env.Data)
	case typeAlertSent:
		c.handleAlertSent(env.Data)
	case typePowerStatus:
		c.handlePowerStatusResponse(env.Data)
	case typeUpdateStatus:
		c.handleUpdateStatusResponse(env.Data)
	case "error":
		c.handleError(env.Data)
	default:
		fmt.Printf("\nUnknown message type: %s\n", env.Type)
	}
}

// handleChallenge signs the server's nonce and sends the auth response.
func (c *client) handleChallenge(raw json.RawMessage) {
	var ch challengeData
	if err := json.Unmarshal(raw, &ch); err != nil {
		fmt.Printf("\nFailed to parse challenge: %v\n", err)
		return
	}

	fmt.Printf("Received challenge from node %s\n", ch.NodeID)

	nonceBytes, err := hex.DecodeString(ch.Nonce)
	if err != nil {
		fmt.Printf("\nInvalid challenge nonce hex: %v\n", err)
		return
	}

	sig, err := c.id.Sign(nonceBytes)
	if err != nil {
		fmt.Printf("\nFailed to sign challenge: %v\n", err)
		return
	}

	resp := authResponseData{
		PublicKey: hex.EncodeToString(c.id.PublicKey()),
		Signature: hex.EncodeToString(sig),
	}

	if err := c.sendJSON(typeAuthResponse, resp); err != nil {
		fmt.Printf("\nFailed to send auth response: %v\n", err)
		return
	}

	fmt.Println("Sent auth response, waiting for confirmation...")
}

// handleAuthOK processes successful authentication.
func (c *client) handleAuthOK(raw json.RawMessage) {
	var data authOKData
	if err := json.Unmarshal(raw, &data); err != nil {
		fmt.Printf("\nFailed to parse auth_ok: %v\n", err)
		return
	}

	c.authed = true
	c.userHash = data.UserHash
	close(c.authDone)

	fmt.Printf("Authenticated as %s\n", data.UserHash)
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  /send <user_hash> <message>              Send a text message")
	fmt.Println("  /alert <message>                         Broadcast critical emergency alert")
	fmt.Println("  /alert-high <message>                    Broadcast high-priority alert")
	fmt.Println("  /contacts                                List known contacts")
	fmt.Println("  /online                                  List users online across the network")
	fmt.Println("  /lookup <user_hash>                      Look up a specific user's presence")
	fmt.Println("  /whoami                                  Show own identity")
	fmt.Println("  /status                                  Show connection status")
	fmt.Println("  /bridge-register <node> <hash> [label]   Register bridge device")
	fmt.Println("  /bridge-unregister <node>                Unregister bridge device")
	fmt.Println("  /bridge-list                             List bridge devices")
	fmt.Println("  /bridge-status                           Show bridge status")
	fmt.Println("  /power                                   Show node power tier status")
	fmt.Println("  /update-status                           Show OTA update status")
	fmt.Println("  /quit                                    Disconnect and exit")
	fmt.Println()
	printPrompt()
}

// handleAuthFail processes failed authentication.
func (c *client) handleAuthFail(raw json.RawMessage) {
	var data authFailData
	if err := json.Unmarshal(raw, &data); err != nil {
		fmt.Printf("\nAuth failed (could not parse reason)\n")
	} else {
		fmt.Printf("\nAuth failed: %s\n", data.Reason)
	}
	close(c.authDone)
	c.close()
	os.Exit(1)
}

// handleIncomingMessage displays an incoming text message.
func (c *client) handleIncomingMessage(raw json.RawMessage) {
	var msg messageData
	if err := json.Unmarshal(raw, &msg); err != nil {
		fmt.Printf("\nFailed to parse message: %v\n", err)
		return
	}

	ts := time.Unix(msg.Timestamp, 0).Format("15:04:05")
	from := shortHash(msg.From)
	fmt.Printf("\r[%s from:%s] %s\n", ts, from, msg.Text)
	printPrompt()
}

// handlePresence updates the contacts map and displays the change.
func (c *client) handlePresence(raw json.RawMessage) {
	var p presenceData
	if err := json.Unmarshal(raw, &p); err != nil {
		fmt.Printf("\nFailed to parse presence: %v\n", err)
		return
	}

	c.mu.Lock()
	c.contacts[p.UserHash] = &contact{
		UserHash: p.UserHash,
		NodeID:   p.NodeID,
		Online:   p.Online,
	}
	c.mu.Unlock()

	status := "online"
	if !p.Online {
		status = "offline"
	}
	fmt.Printf("\r* user %s is now %s at %s\n", shortHash(p.UserHash), status, p.NodeID)
	printPrompt()
}

// inputLoop reads lines from stdin and processes commands.
// It blocks until authentication completes before accepting input.
func (c *client) inputLoop() {
	// Wait for auth to complete before processing commands.
	select {
	case <-c.authDone:
		if !c.authed {
			return
		}
	case <-c.done:
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		select {
		case <-c.done:
			return
		default:
		}

		if !scanner.Scan() {
			// EOF or read error.
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			printPrompt()
			continue
		}

		c.handleCommand(line)
	}
}

// handleCommand parses and executes a user command.
func (c *client) handleCommand(line string) {
	switch {
	case line == "/quit" || line == "/exit":
		fmt.Println("Disconnecting...")
		c.close()
		os.Exit(0)

	case line == "/whoami":
		fmt.Printf("  hash      : %s\n", c.id.Hash())
		fmt.Printf("  public key: %s\n", hex.EncodeToString(c.id.PublicKey()))
		if c.userHash != "" {
			fmt.Printf("  server id : %s\n", c.userHash)
		}
		printPrompt()

	case line == "/status":
		if c.authed {
			fmt.Println("  Connected and authenticated")
		} else {
			fmt.Println("  Connected, not yet authenticated")
		}
		fmt.Printf("  server: %s\n", c.server)
		printPrompt()

	case line == "/contacts":
		c.mu.Lock()
		if len(c.contacts) == 0 {
			fmt.Println("  No known contacts yet.")
		} else {
			for _, ct := range c.contacts {
				status := "online"
				if !ct.Online {
					status = "offline"
				}
				fmt.Printf("  %s  [%s]  node: %s\n", shortHash(ct.UserHash), status, ct.NodeID)
			}
		}
		c.mu.Unlock()
		printPrompt()

	case strings.HasPrefix(line, "/send "):
		c.handleSend(line)

	case strings.HasPrefix(line, "/bridge-register "):
		c.handleBridgeRegister(line)
	case strings.HasPrefix(line, "/bridge-unregister "):
		c.handleBridgeUnregister(line)
	case line == "/bridge-list":
		c.handleBridgeListCmd()
	case line == "/bridge-status":
		c.handleBridgeStatusCmd()

	case line == "/online":
		c.handleOnlineCmd()
	case strings.HasPrefix(line, "/lookup "):
		c.handleLookupCmd(line)

	case line == "/power":
		c.handlePowerCmd()

	case line == "/update-status":
		c.handleUpdateStatusCmd()

	case strings.HasPrefix(line, "/alert-high "):
		c.handleAlertCmd(line, 1) // PriorityHigh
	case strings.HasPrefix(line, "/alert "):
		c.handleAlertCmd(line, 0) // PriorityCritical

	case strings.HasPrefix(line, "/"):
		fmt.Printf("  Unknown command: %s\n", line)
		printPrompt()

	default:
		fmt.Println("  Type a command starting with /. Try /send <hash> <message>")
		printPrompt()
	}
}

// handleSend parses and sends a text message.
func (c *client) handleSend(line string) {
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 3 {
		fmt.Println("  Usage: /send <user_hash> <message>")
		printPrompt()
		return
	}

	if !c.authed {
		fmt.Println("  Not authenticated yet. Wait for auth to complete.")
		printPrompt()
		return
	}

	to := parts[1]
	text := parts[2]

	msg := sendMessageData{
		To:   to,
		Text: text,
	}

	if err := c.sendJSON(typeSendMessage, msg); err != nil {
		fmt.Printf("  Failed to send message: %v\n", err)
	} else {
		fmt.Printf("  -> sent to %s\n", shortHash(to))
	}
	printPrompt()
}

// handleBridgeRegister parses and sends a bridge_register request.
func (c *client) handleBridgeRegister(line string) {
	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 3 {
		fmt.Println("  Usage: /bridge-register <node_num> <user_hash> [label]")
		printPrompt()
		return
	}

	nodeNum := parts[1]
	userHash := parts[2]
	label := ""
	if len(parts) >= 4 {
		label = parts[3]
	}

	data := map[string]string{
		"node_num":  nodeNum,
		"user_hash": userHash,
		"label":     label,
	}

	if err := c.sendJSON(typeBridgeRegister, data); err != nil {
		fmt.Printf("  Failed to send bridge_register: %v\n", err)
	}
	printPrompt()
}

// handleBridgeUnregister parses and sends a bridge_unregister request.
func (c *client) handleBridgeUnregister(line string) {
	parts := strings.SplitN(line, " ", 2)
	if len(parts) < 2 {
		fmt.Println("  Usage: /bridge-unregister <node_num>")
		printPrompt()
		return
	}

	data := map[string]string{
		"node_num": parts[1],
	}

	if err := c.sendJSON(typeBridgeUnregister, data); err != nil {
		fmt.Printf("  Failed to send bridge_unregister: %v\n", err)
	}
	printPrompt()
}

// handleBridgeListCmd sends a bridge_list request.
func (c *client) handleBridgeListCmd() {
	if err := c.sendJSON(typeBridgeList, struct{}{}); err != nil {
		fmt.Printf("  Failed to send bridge_list: %v\n", err)
	}
	printPrompt()
}

// handleBridgeStatusCmd sends a bridge_status request.
func (c *client) handleBridgeStatusCmd() {
	if err := c.sendJSON(typeBridgeStatus, struct{}{}); err != nil {
		fmt.Printf("  Failed to send bridge_status: %v\n", err)
	}
	printPrompt()
}

// handleOnlineCmd sends a who_online request.
func (c *client) handleOnlineCmd() {
	if err := c.sendJSON(typeWhoOnline, struct{}{}); err != nil {
		fmt.Printf("  Failed to send who_online: %v\n", err)
	}
	printPrompt()
}

// handleLookupCmd parses and sends a lookup_user request.
func (c *client) handleLookupCmd(line string) {
	parts := strings.SplitN(line, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		fmt.Println("  Usage: /lookup <user_hash>")
		printPrompt()
		return
	}

	data := map[string]string{
		"user_hash": strings.TrimSpace(parts[1]),
	}

	if err := c.sendJSON(typeLookupUser, data); err != nil {
		fmt.Printf("  Failed to send lookup_user: %v\n", err)
	}
	printPrompt()
}

// handleAlertCmd sends an emergency alert with the given priority.
func (c *client) handleAlertCmd(line string, priority int) {
	parts := strings.SplitN(line, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		if priority == 0 {
			fmt.Println("  Usage: /alert <message>")
		} else {
			fmt.Println("  Usage: /alert-high <message>")
		}
		printPrompt()
		return
	}

	if !c.authed {
		fmt.Println("  Not authenticated yet. Wait for auth to complete.")
		printPrompt()
		return
	}

	text := strings.TrimSpace(parts[1])

	data := map[string]interface{}{
		"text":     text,
		"priority": priority,
	}

	if err := c.sendJSON(typeSendAlert, data); err != nil {
		fmt.Printf("  Failed to send alert: %v\n", err)
	}
	printPrompt()
}

// handleWhoOnlineResponse displays the list of online users.
func (c *client) handleWhoOnlineResponse(raw json.RawMessage) {
	var data whoOnlineData
	if err := json.Unmarshal(raw, &data); err != nil {
		fmt.Printf("\nFailed to parse who_online: %v\n", err)
		printPrompt()
		return
	}

	if data.Count == 0 {
		fmt.Println("\r  Online users (0): none")
		printPrompt()
		return
	}

	fmt.Printf("\r  Online users (%d):\n", data.Count)
	for _, u := range data.Users {
		since := time.Unix(u.Since, 0).Format("15:04:05")
		fmt.Printf("    %s  at node %s  since %s\n", shortHash(u.UserHash), shortHash(u.NodeID), since)
	}
	printPrompt()
}

// handleLookupUserResponse displays the result of a user lookup.
func (c *client) handleLookupUserResponse(raw json.RawMessage) {
	var data lookupUserData
	if err := json.Unmarshal(raw, &data); err != nil {
		fmt.Printf("\nFailed to parse lookup_user: %v\n", err)
		printPrompt()
		return
	}

	if data.Online {
		since := time.Unix(data.Since, 0).Format("15:04:05")
		fmt.Printf("\r  User %s: ONLINE at node %s since %s\n", shortHash(data.UserHash), shortHash(data.NodeID), since)
	} else {
		fmt.Printf("\r  User %s: OFFLINE\n", shortHash(data.UserHash))
	}
	printPrompt()
}

// handleAlert displays an incoming emergency alert with visual emphasis.
func (c *client) handleAlert(raw json.RawMessage) {
	var data alertData
	if err := json.Unmarshal(raw, &data); err != nil {
		fmt.Printf("\nFailed to parse alert: %v\n", err)
		printPrompt()
		return
	}

	ts := time.Unix(data.Timestamp, 0).Format("15:04:05")
	from := shortHash(data.From)

	priorityLabel := "ALERT"
	switch data.Priority {
	case 0:
		priorityLabel = "EMERGENCY ALERT"
	case 1:
		priorityLabel = "HIGH-PRIORITY ALERT"
	case 2:
		priorityLabel = "ALERT"
	}

	fmt.Printf("\r\n*** %s ***\n", priorityLabel)
	fmt.Printf("[%s from:%s] %s\n", ts, from, data.Text)
	fmt.Printf("*** END %s ***\n\n", priorityLabel)
	printPrompt()
}

// handleAlertSent confirms an alert was broadcast.
func (c *client) handleAlertSent(raw json.RawMessage) {
	var data alertSentData
	if err := json.Unmarshal(raw, &data); err != nil {
		fmt.Printf("\nFailed to parse alert_sent: %v\n", err)
		printPrompt()
		return
	}
	fmt.Printf("\r  Alert broadcast (id: %s)\n", shortHash(data.AlertID))
	printPrompt()
}

// handlePowerCmd sends a power_status request.
func (c *client) handlePowerCmd() {
	if err := c.sendJSON(typePowerStatus, struct{}{}); err != nil {
		fmt.Printf("  Failed to send power_status: %v\n", err)
	}
	printPrompt()
}

// handlePowerStatusResponse displays the node's power tier status.
func (c *client) handlePowerStatusResponse(raw json.RawMessage) {
	var data powerStatusData
	if err := json.Unmarshal(raw, &data); err != nil {
		fmt.Printf("\nFailed to parse power_status: %v\n", err)
		printPrompt()
		return
	}

	fmt.Printf("\r  Power tier:         %s\n", data.Tier)
	fmt.Printf("  Announce interval:  %s\n", data.AnnounceInterval)
	fmt.Printf("  Max sessions:       %d\n", data.MaxSessions)
	fmt.Printf("  Active sessions:    %d\n", data.SessionsActive)
	printPrompt()
}

// handleUpdateStatusCmd sends an update_status request.
func (c *client) handleUpdateStatusCmd() {
	if err := c.sendJSON(typeUpdateStatus, struct{}{}); err != nil {
		fmt.Printf("  Failed to send update_status: %v\n", err)
	}
	printPrompt()
}

// handleUpdateStatusResponse displays the OTA update status.
func (c *client) handleUpdateStatusResponse(raw json.RawMessage) {
	var data updateStatusData
	if err := json.Unmarshal(raw, &data); err != nil {
		fmt.Printf("\nFailed to parse update_status: %v\n", err)
		printPrompt()
		return
	}

	fmt.Printf("\r  Current version:  %s\n", data.CurrentVersion)
	if data.LatestVersion != "" {
		fmt.Printf("  Latest version:   %s\n", data.LatestVersion)
	} else {
		fmt.Printf("  Latest version:   (none available)\n")
	}
	fmt.Printf("  Update status:    %s\n", data.Status)
	if data.LastCheck > 0 {
		fmt.Printf("  Last check:       %s\n", time.Unix(data.LastCheck, 0).Format("2006-01-02 15:04:05"))
	} else {
		fmt.Printf("  Last check:       never\n")
	}
	if data.Error != "" {
		fmt.Printf("  Last error:       %s\n", data.Error)
	}
	printPrompt()
}

// handleBridgeRegisterOK displays a successful bridge registration confirmation.
func (c *client) handleBridgeRegisterOK(raw json.RawMessage) {
	var data bridgeRegisterOKData
	if err := json.Unmarshal(raw, &data); err != nil {
		fmt.Printf("\nFailed to parse bridge_register_ok: %v\n", err)
		printPrompt()
		return
	}
	label := data.Label
	if label == "" {
		label = "(no label)"
	}
	fmt.Printf("\r  Bridge device registered: %s  %s\n", data.NodeNum, label)
	printPrompt()
}

// handleBridgeUnregisterOK displays a successful bridge unregistration confirmation.
func (c *client) handleBridgeUnregisterOK(raw json.RawMessage) {
	var data bridgeUnregisterOKData
	if err := json.Unmarshal(raw, &data); err != nil {
		fmt.Printf("\nFailed to parse bridge_unregister_ok: %v\n", err)
		printPrompt()
		return
	}
	fmt.Printf("\r  Bridge device unregistered: %s\n", data.NodeNum)
	printPrompt()
}

// handleBridgeListResponse displays the list of bridge devices.
func (c *client) handleBridgeListResponse(raw json.RawMessage) {
	var data bridgeListData
	if err := json.Unmarshal(raw, &data); err != nil {
		fmt.Printf("\nFailed to parse bridge_list: %v\n", err)
		printPrompt()
		return
	}

	if len(data.Devices) == 0 {
		fmt.Println("\r  No bridge devices registered.")
		printPrompt()
		return
	}

	fmt.Printf("\r  Bridge devices (%d):\n", len(data.Devices))
	fmt.Printf("  %-14s %-14s %-16s %-20s %s\n", "NODE", "USER", "LABEL", "LAST HEARD", "ACTIVE")
	fmt.Printf("  %-14s %-14s %-16s %-20s %s\n", "----", "----", "-----", "----------", "------")
	for _, dev := range data.Devices {
		userShort := shortHash(dev.UserHash)
		label := dev.Label
		if label == "" {
			label = "-"
		}
		lastHeard := dev.LastHeard
		if lastHeard == "" {
			lastHeard = "never"
		}
		active := "no"
		if dev.Active {
			active = "yes"
		}
		fmt.Printf("  %-14s %-14s %-16s %-20s %s\n", dev.NodeNum, userShort, label, lastHeard, active)
	}
	printPrompt()
}

// handleBridgeStatusResponse displays the bridge status overview.
func (c *client) handleBridgeStatusResponse(raw json.RawMessage) {
	var data bridgeStatusData
	if err := json.Unmarshal(raw, &data); err != nil {
		fmt.Printf("\nFailed to parse bridge_status: %v\n", err)
		printPrompt()
		return
	}

	enabledStr := "disabled"
	if data.Enabled {
		enabledStr = "enabled"
	}
	fmt.Printf("\r  Bridge status: %s\n", enabledStr)
	fmt.Printf("  Registered devices: %d\n", data.DeviceCount)
	fmt.Printf("  Active devices:     %d\n", data.ActiveCount)
	printPrompt()
}

// handleError displays a server error message.
func (c *client) handleError(raw json.RawMessage) {
	var data errorData
	if err := json.Unmarshal(raw, &data); err != nil {
		fmt.Printf("\nServer error (could not parse details)\n")
		printPrompt()
		return
	}
	fmt.Printf("\r  Error: %s\n", data.Message)
	printPrompt()
}

// heartbeatLoop sends a heartbeat message every 30 seconds.
func (c *client) heartbeatLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			if c.authed {
				if err := c.sendJSON(typeHeartbeat, struct{}{}); err != nil {
					fmt.Printf("\nHeartbeat failed: %v\n", err)
				}
			}
		}
	}
}

// sendJSON marshals a typed message and sends it over the WebSocket.
func (c *client) sendJSON(msgType string, data interface{}) error {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal data: %w", err)
	}

	env := envelope{
		Type: msgType,
		Data: dataBytes,
	}

	return c.conn.WriteJSON(env)
}

// close gracefully shuts down the WebSocket connection.
func (c *client) close() {
	if c.conn != nil {
		_ = c.conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		_ = c.conn.Close()
	}
}

// shortHash returns the first 6 characters of a hex hash for display.
func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// printPrompt writes the input prompt to stdout.
func printPrompt() {
	fmt.Print("relay> ")
}

// homeDir returns the user's home directory.
func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

// hostname returns the system hostname, falling back to "unknown".
func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
