// Package poc contains end-to-end tests for the relay system.
//
// Prerequisites: MQTT broker running on localhost:1883
//
//	cd poc && docker compose up -d
//
// Run:
//
//	go test -v -count=1 ./poc/
package poc

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/meshtastic-relay/relay/identity"
)

// envelope mirrors the server/client wire format.
type envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type challengeData struct {
	Nonce  string `json:"nonce"`
	NodeID string `json:"node_id"`
}

type authResponseData struct {
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

type authOKData struct {
	UserHash string `json:"user_hash"`
}

type sendMessageData struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

type messageData struct {
	From string `json:"from"`
	Text string `json:"text"`
}

// connectAndAuth dials the WebSocket, completes challenge-response auth,
// and returns the connection and user hash.
func connectAndAuth(t *testing.T, addr string, id *identity.Identity) (*websocket.Conn, string) {
	t.Helper()

	u := url.URL{Scheme: "ws", Host: addr, Path: "/ws"}
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}

	// Read challenge
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var env envelope
	if err := conn.ReadJSON(&env); err != nil {
		t.Fatalf("read challenge: %v", err)
	}
	if env.Type != "challenge" {
		t.Fatalf("expected challenge, got %s", env.Type)
	}

	var ch challengeData
	json.Unmarshal(env.Data, &ch)
	t.Logf("Got challenge from node %s", ch.NodeID)

	// Sign the nonce
	nonceBytes, _ := hex.DecodeString(ch.Nonce)
	sig, err := id.Sign(nonceBytes)
	if err != nil {
		t.Fatalf("sign nonce: %v", err)
	}

	// Send auth response
	resp := authResponseData{
		PublicKey: hex.EncodeToString(id.PublicKey()),
		Signature: hex.EncodeToString(sig),
	}
	respData, _ := json.Marshal(resp)
	conn.WriteJSON(envelope{Type: "auth_response", Data: respData})

	// Read auth_ok
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := conn.ReadJSON(&env); err != nil {
		t.Fatalf("read auth response: %v", err)
	}
	if env.Type != "auth_ok" {
		t.Fatalf("expected auth_ok, got %s: %s", env.Type, string(env.Data))
	}

	var ok authOKData
	json.Unmarshal(env.Data, &ok)
	t.Logf("Authenticated as %s", ok.UserHash)

	conn.SetReadDeadline(time.Time{}) // Clear deadline
	return conn, ok.UserHash
}

func TestE2E_TwoNodeMessageRelay(t *testing.T) {
	// Check if the MQTT broker and nodes are running
	if os.Getenv("POC_E2E") == "" {
		t.Skip("Set POC_E2E=1 to run (requires MQTT broker + both nodes running)")
	}

	nodeAAddr := "localhost:9370"
	nodeBAddr := "localhost:9371"

	// Generate two user identities
	alice, err := identity.Generate()
	if err != nil {
		t.Fatalf("generate alice: %v", err)
	}
	bob, err := identity.Generate()
	if err != nil {
		t.Fatalf("generate bob: %v", err)
	}

	t.Logf("Alice hash: %s", alice.Hash())
	t.Logf("Bob hash:   %s", bob.Hash())

	// Connect Alice to Node A
	aliceConn, aliceHash := connectAndAuth(t, nodeAAddr, alice)
	defer aliceConn.Close()
	t.Logf("Alice connected to Node A")

	// Connect Bob to Node B
	bobConn, bobHash := connectAndAuth(t, nodeBAddr, bob)
	defer bobConn.Close()
	t.Logf("Bob connected to Node B")

	// Give MQTT a moment to propagate subscriptions
	time.Sleep(500 * time.Millisecond)

	// Alice sends a message to Bob
	testMsg := fmt.Sprintf("Hello Bob! The time is %s", time.Now().Format(time.RFC3339))
	sendData, _ := json.Marshal(sendMessageData{To: bobHash, Text: testMsg})
	aliceConn.WriteJSON(envelope{Type: "send_message", Data: sendData})
	t.Logf("Alice sent: %s", testMsg)

	// Bob should receive the message
	bobConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var env envelope
	if err := bobConn.ReadJSON(&env); err != nil {
		t.Fatalf("Bob read message: %v", err)
	}

	if env.Type != "message" {
		t.Fatalf("Bob expected message, got %s: %s", env.Type, string(env.Data))
	}

	var msg messageData
	json.Unmarshal(env.Data, &msg)
	t.Logf("Bob received from %s: %s", msg.From, msg.Text)

	if msg.Text != testMsg {
		t.Errorf("message mismatch: got %q, want %q", msg.Text, testMsg)
	}
	if msg.From != aliceHash {
		t.Errorf("sender mismatch: got %q, want %q", msg.From, aliceHash)
	}

	// Bob replies to Alice
	replyMsg := "Hey Alice! Got your message."
	replyData, _ := json.Marshal(sendMessageData{To: aliceHash, Text: replyMsg})
	bobConn.WriteJSON(envelope{Type: "send_message", Data: replyData})
	t.Logf("Bob sent reply: %s", replyMsg)

	// Alice should receive the reply
	aliceConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := aliceConn.ReadJSON(&env); err != nil {
		t.Fatalf("Alice read reply: %v", err)
	}

	if env.Type != "message" {
		t.Fatalf("Alice expected message, got %s: %s", env.Type, string(env.Data))
	}

	json.Unmarshal(env.Data, &msg)
	t.Logf("Alice received from %s: %s", msg.From, msg.Text)

	if msg.Text != replyMsg {
		t.Errorf("reply mismatch: got %q, want %q", msg.Text, replyMsg)
	}
	if msg.From != bobHash {
		t.Errorf("reply sender mismatch: got %q, want %q", msg.From, bobHash)
	}

	t.Log("SUCCESS: Two-node message relay working!")
}

func TestE2E_StoreAndForward(t *testing.T) {
	if os.Getenv("POC_E2E") == "" {
		t.Skip("Set POC_E2E=1 to run (requires MQTT broker + both nodes running)")
	}

	nodeAAddr := "localhost:9370"
	nodeBAddr := "localhost:9371"

	// Generate identities
	alice, _ := identity.Generate()
	bob, _ := identity.Generate()

	// Connect Alice to Node A
	aliceConn, _ := connectAndAuth(t, nodeAAddr, alice)
	defer aliceConn.Close()

	// Alice sends a message to Bob (who is NOT connected yet)
	offlineMsg := "Bob, you there? Sent while you were offline."
	sendData, _ := json.Marshal(sendMessageData{
		To:   bob.Hash().String(),
		Text: offlineMsg,
	})
	aliceConn.WriteJSON(envelope{Type: "send_message", Data: sendData})
	t.Logf("Alice sent message to offline Bob: %s", offlineMsg)

	// Wait for message to propagate through MQTT and get stored
	time.Sleep(1 * time.Second)

	// Now Bob connects to Node B
	bobConn, _ := connectAndAuth(t, nodeBAddr, bob)
	defer bobConn.Close()
	t.Logf("Bob connected (should receive queued messages)")

	// Bob should receive the queued message
	bobConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var env envelope
	if err := bobConn.ReadJSON(&env); err != nil {
		t.Fatalf("Bob read queued message: %v", err)
	}

	if env.Type != "message" {
		t.Fatalf("Bob expected message, got %s: %s", env.Type, string(env.Data))
	}

	var msg messageData
	json.Unmarshal(env.Data, &msg)
	t.Logf("Bob received queued message: %s", msg.Text)

	if msg.Text != offlineMsg {
		t.Errorf("queued message mismatch: got %q, want %q", msg.Text, offlineMsg)
	}

	t.Log("SUCCESS: Store-and-forward working!")
}
