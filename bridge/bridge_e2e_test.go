package bridge

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/meshtastic-relay/relay/identity"
	"github.com/meshtastic-relay/relay/transport"
)

// newE2EBridge is a helper that creates a Bridge with short timeouts suitable
// for fast E2E tests. The registry uses a 2-second active timeout and the
// dedup cache uses a 5-second window.
func newE2EBridge(t *testing.T, registry *Registry) *Bridge {
	t.Helper()
	nodeID := testHash(0x01)
	cfg := BridgeConfig{
		Enabled:       true,
		ActiveTimeout: 2 * time.Second,
		DedupWindow:   5 * time.Second,
	}
	return New(cfg, registry, nodeID)
}

// generateIdentity is a test helper that calls identity.Generate and fails the
// test immediately on error.
func generateIdentity(t *testing.T, label string) *identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatalf("generate identity for %s: %v", label, err)
	}
	return id
}

// TestE2E_InboundBridgeMessage simulates a Meshtastic device sending a message
// to a relay user. It verifies that ProcessInbound correctly remaps the source
// identity from the Meshtastic node number to the registered relay user hash,
// strips the addressing prefix, and sets the destination hash.
func TestE2E_InboundBridgeMessage(t *testing.T) {
	alice := generateIdentity(t, "alice")
	bob := generateIdentity(t, "bob")

	registry := NewRegistry(2 * time.Second)
	err := registry.Register(DeviceRegistration{
		MeshNodeNum:  0x12345678,
		UserHash:     bob.Hash(),
		Label:        "bob-radio",
		RegisteredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("register bob device: %v", err)
	}

	br := newE2EBridge(t, registry)

	// Build the addressing prefix using the first 12 hex chars of alice's hash
	// (6 bytes = short hash format expected by ParseBridgeMessage).
	aliceShortHex := alice.Hash().String()[:12]
	msgText := "Hello from the mesh!"
	pkt := &transport.Packet{
		Data:         []byte("@" + aliceShortHex + " " + msgText),
		Via:          "meshradio",
		MeshNodeNum:  0x12345678,
		MeshPacketID: 1,
	}

	result := br.ProcessInbound(pkt)
	if result == nil {
		t.Fatal("ProcessInbound returned nil for a valid inbound bridge message")
	}

	// Verify source is remapped to bob's relay identity.
	if result.SourceHash != bob.Hash() {
		t.Errorf("SourceHash = %s, want %s (bob)", result.SourceHash, bob.Hash())
	}

	// Verify destination hash prefix matches alice's short hash.
	alicePrefix, _ := hex.DecodeString(aliceShortHex)
	for i := 0; i < len(alicePrefix); i++ {
		if result.DestHash[i] != alicePrefix[i] {
			t.Errorf("DestHash[%d] = 0x%02x, want 0x%02x", i, result.DestHash[i], alicePrefix[i])
		}
	}

	// Verify message body was extracted without the addressing prefix.
	if string(result.Data) != msgText {
		t.Errorf("Data = %q, want %q", string(result.Data), msgText)
	}

	// Verify transport metadata.
	if result.Via != "lora-bridge" {
		t.Errorf("Via = %q, want %q", result.Via, "lora-bridge")
	}
	if result.MeshNodeNum != 0x12345678 {
		t.Errorf("MeshNodeNum = 0x%08x, want 0x12345678", result.MeshNodeNum)
	}
	if result.MeshPacketID != 1 {
		t.Errorf("MeshPacketID = %d, want 1", result.MeshPacketID)
	}
}

// TestE2E_OutboundBridgeMessage simulates a relay user sending a message to a
// user whose Meshtastic device is registered and recently active. It verifies
// that FormatOutbound produces the correct "[<short_hash>] <message>" payload
// and targets the correct Meshtastic node number.
func TestE2E_OutboundBridgeMessage(t *testing.T) {
	alice := generateIdentity(t, "alice")
	bob := generateIdentity(t, "bob")

	registry := NewRegistry(2 * time.Second)
	err := registry.Register(DeviceRegistration{
		MeshNodeNum:  0xAABBCCDD,
		UserHash:     bob.Hash(),
		Label:        "bob-radio",
		RegisteredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("register bob device: %v", err)
	}

	// Mark bob's device as recently heard so it qualifies as active.
	registry.UpdateLastHeard(0xAABBCCDD, testHash(0x01), 100)

	br := newE2EBridge(t, registry)

	payload, targetNodeNum, err := br.FormatOutbound(bob.Hash(), alice.Hash(), []byte("Hey Bob!"))
	if err != nil {
		t.Fatalf("FormatOutbound: %v", err)
	}

	// Verify target node number matches bob's registered device.
	if targetNodeNum != 0xAABBCCDD {
		t.Errorf("targetNodeNum = 0x%08x, want 0xAABBCCDD", targetNodeNum)
	}

	// Verify payload format: "[<alice_short_hash>] Hey Bob!"
	aliceHash := alice.Hash()
	aliceShortHex := hex.EncodeToString(aliceHash[:6])
	expectedPrefix := "[" + aliceShortHex + "] "
	payloadStr := string(payload)

	if !strings.HasPrefix(payloadStr, expectedPrefix) {
		t.Errorf("payload = %q, want prefix %q", payloadStr, expectedPrefix)
	}
	if !strings.Contains(payloadStr, "Hey Bob!") {
		t.Errorf("payload = %q, does not contain message body", payloadStr)
	}
	if payloadStr != expectedPrefix+"Hey Bob!" {
		t.Errorf("payload = %q, want %q", payloadStr, expectedPrefix+"Hey Bob!")
	}
}

// TestE2E_RoundTripBridgeMessage exercises the full bidirectional flow:
// a Meshtastic device sends a message through the bridge to a relay user,
// then the relay user replies back through the bridge to the device.
func TestE2E_RoundTripBridgeMessage(t *testing.T) {
	alice := generateIdentity(t, "alice")
	bob := generateIdentity(t, "bob")

	registry := NewRegistry(2 * time.Second)
	err := registry.Register(DeviceRegistration{
		MeshNodeNum:  0x11223344,
		UserHash:     bob.Hash(),
		Label:        "bob-radio",
		RegisteredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("register bob device: %v", err)
	}

	br := newE2EBridge(t, registry)

	// --- Inbound leg: bob's device sends "ping" addressed to alice ---

	aliceShortHex := alice.Hash().String()[:12]
	inboundPkt := &transport.Packet{
		Data:         []byte("@" + aliceShortHex + " ping"),
		Via:          "meshradio",
		MeshNodeNum:  0x11223344,
		MeshPacketID: 10,
	}

	inResult := br.ProcessInbound(inboundPkt)
	if inResult == nil {
		t.Fatal("ProcessInbound returned nil for valid inbound ping")
	}

	if inResult.SourceHash != bob.Hash() {
		t.Errorf("inbound SourceHash = %s, want %s (bob)", inResult.SourceHash, bob.Hash())
	}
	if string(inResult.Data) != "ping" {
		t.Errorf("inbound Data = %q, want %q", string(inResult.Data), "ping")
	}

	// --- Outbound leg: alice replies "pong" to bob ---

	// ProcessInbound already called UpdateLastHeard for bob's device, so it
	// should be active for outbound delivery.
	outPayload, outNodeNum, err := br.FormatOutbound(bob.Hash(), alice.Hash(), []byte("pong"))
	if err != nil {
		t.Fatalf("FormatOutbound: %v", err)
	}

	if outNodeNum != 0x11223344 {
		t.Errorf("outbound targetNodeNum = 0x%08x, want 0x11223344", outNodeNum)
	}

	// Verify the outbound payload contains the formatted sender prefix and message.
	aliceOutHash := alice.Hash()
	aliceOutShortHex := hex.EncodeToString(aliceOutHash[:6])
	expectedOutPayload := "[" + aliceOutShortHex + "] pong"
	if string(outPayload) != expectedOutPayload {
		t.Errorf("outbound payload = %q, want %q", string(outPayload), expectedOutPayload)
	}
}

// TestE2E_DuplicatePacketSuppression verifies that the bridge deduplication
// layer drops duplicate Meshtastic packets (same nodeNum + packetID pair)
// while allowing the first instance through.
func TestE2E_DuplicatePacketSuppression(t *testing.T) {
	bob := generateIdentity(t, "bob")

	registry := NewRegistry(2 * time.Second)
	err := registry.Register(DeviceRegistration{
		MeshNodeNum:  0xDEAD0001,
		UserHash:     bob.Hash(),
		Label:        "bob-radio",
		RegisteredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("register device: %v", err)
	}

	br := newE2EBridge(t, registry)

	pkt := &transport.Packet{
		Data:         []byte("@aabbccddeeff duplicate test"),
		Via:          "meshradio",
		MeshNodeNum:  0xDEAD0001,
		MeshPacketID: 42,
	}

	// First submission should be accepted.
	first := br.ProcessInbound(pkt)
	if first == nil {
		t.Fatal("first ProcessInbound should return a valid packet")
	}
	if string(first.Data) != "duplicate test" {
		t.Errorf("first Data = %q, want %q", string(first.Data), "duplicate test")
	}

	// Second submission with same nodeNum + packetID should be dropped.
	second := br.ProcessInbound(pkt)
	if second != nil {
		t.Error("second ProcessInbound should return nil for duplicate packet")
	}
}

// TestE2E_UnregisteredDeviceRejected verifies that packets from Meshtastic
// devices not present in the registry are silently dropped.
func TestE2E_UnregisteredDeviceRejected(t *testing.T) {
	registry := NewRegistry(2 * time.Second)
	br := newE2EBridge(t, registry)

	pkt := &transport.Packet{
		Data:         []byte("@aabbccddeeff hello from unknown"),
		Via:          "meshradio",
		MeshNodeNum:  0xBAD00000,
		MeshPacketID: 1,
	}

	result := br.ProcessInbound(pkt)
	if result != nil {
		t.Error("ProcessInbound should return nil for unregistered device")
	}
}

// TestE2E_MultiDeviceActiveSelection verifies that when a user has multiple
// registered Meshtastic devices, outbound messages are delivered to the most
// recently heard device. It also verifies that the selection updates when a
// different device becomes the most recent.
func TestE2E_MultiDeviceActiveSelection(t *testing.T) {
	alice := generateIdentity(t, "alice")
	bob := generateIdentity(t, "bob")

	registry := NewRegistry(2 * time.Second)

	// Register two devices for bob.
	err := registry.Register(DeviceRegistration{
		MeshNodeNum:  0x000A000A,
		UserHash:     bob.Hash(),
		Label:        "device-A",
		RegisteredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("register device A: %v", err)
	}
	err = registry.Register(DeviceRegistration{
		MeshNodeNum:  0x000B000B,
		UserHash:     bob.Hash(),
		Label:        "device-B",
		RegisteredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("register device B: %v", err)
	}

	br := newE2EBridge(t, registry)

	// Hear device A first.
	registry.UpdateLastHeard(0x000A000A, testHash(0x01), 1)
	time.Sleep(10 * time.Millisecond)
	// Hear device B slightly later -- it becomes the most recent.
	registry.UpdateLastHeard(0x000B000B, testHash(0x01), 2)

	// Outbound should target device B (most recently heard).
	_, targetNode, err := br.FormatOutbound(bob.Hash(), alice.Hash(), []byte("msg1"))
	if err != nil {
		t.Fatalf("FormatOutbound (expect B): %v", err)
	}
	if targetNode != 0x000B000B {
		t.Errorf("first outbound targetNodeNum = 0x%08x, want 0x000B000B (device B)", targetNode)
	}

	// Now hear device A again, making it the most recent.
	time.Sleep(10 * time.Millisecond)
	registry.UpdateLastHeard(0x000A000A, testHash(0x01), 3)

	// Outbound should now target device A.
	_, targetNode, err = br.FormatOutbound(bob.Hash(), alice.Hash(), []byte("msg2"))
	if err != nil {
		t.Fatalf("FormatOutbound (expect A): %v", err)
	}
	if targetNode != 0x000A000A {
		t.Errorf("second outbound targetNodeNum = 0x%08x, want 0x000A000A (device A)", targetNode)
	}
}
