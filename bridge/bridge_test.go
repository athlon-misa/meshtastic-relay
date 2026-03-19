package bridge

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/meshtastic-relay/relay/identity"
	"github.com/meshtastic-relay/relay/transport"
)

// testHash creates a deterministic identity.Hash from a byte value for testing.
func testHash(b byte) identity.Hash {
	var h identity.Hash
	for i := range h {
		h[i] = b
	}
	return h
}

// --- Registry Tests ---

func TestRegistry_RegisterAndLookup(t *testing.T) {
	reg := NewRegistry(5 * time.Minute)

	registration := DeviceRegistration{
		MeshNodeNum:  0x12345678,
		UserHash:     testHash(0xAA),
		Label:        "test-device",
		RegisteredAt: time.Now(),
	}

	if err := reg.Register(registration); err != nil {
		t.Fatalf("Register: %v", err)
	}

	state := reg.LookupByNodeNum(0x12345678)
	if state == nil {
		t.Fatal("LookupByNodeNum returned nil for registered device")
	}
	if state.MeshNodeNum != 0x12345678 {
		t.Errorf("MeshNodeNum = 0x%08x, want 0x12345678", state.MeshNodeNum)
	}
	if state.UserHash != testHash(0xAA) {
		t.Errorf("UserHash = %s, want %s", state.UserHash, testHash(0xAA))
	}
	if state.Label != "test-device" {
		t.Errorf("Label = %q, want %q", state.Label, "test-device")
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	reg := NewRegistry(5 * time.Minute)

	registration := DeviceRegistration{
		MeshNodeNum:  0x12345678,
		UserHash:     testHash(0xAA),
		Label:        "first",
		RegisteredAt: time.Now(),
	}

	if err := reg.Register(registration); err != nil {
		t.Fatalf("first Register: %v", err)
	}

	registration.Label = "second"
	err := reg.Register(registration)
	if err == nil {
		t.Fatal("expected error for duplicate registration, got nil")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	reg := NewRegistry(5 * time.Minute)

	_ = reg.Register(DeviceRegistration{
		MeshNodeNum:  0x11111111,
		UserHash:     testHash(0xBB),
		Label:        "to-remove",
		RegisteredAt: time.Now(),
	})

	reg.Unregister(0x11111111)

	if reg.IsRegistered(0x11111111) {
		t.Error("device still registered after Unregister")
	}
	if state := reg.LookupByNodeNum(0x11111111); state != nil {
		t.Error("LookupByNodeNum returned non-nil after Unregister")
	}
	if devices := reg.DevicesForUser(testHash(0xBB)); len(devices) != 0 {
		t.Errorf("DevicesForUser returned %d devices, want 0", len(devices))
	}
}

func TestRegistry_UnregisterNonexistent(t *testing.T) {
	reg := NewRegistry(5 * time.Minute)
	// Should not panic.
	reg.Unregister(0xDEADBEEF)
}

func TestRegistry_LookupUnknown(t *testing.T) {
	reg := NewRegistry(5 * time.Minute)
	if state := reg.LookupByNodeNum(0x99999999); state != nil {
		t.Error("LookupByNodeNum returned non-nil for unknown device")
	}
}

func TestRegistry_MultiDevicePerUser(t *testing.T) {
	reg := NewRegistry(5 * time.Minute)
	userHash := testHash(0xCC)

	_ = reg.Register(DeviceRegistration{
		MeshNodeNum:  0x00000001,
		UserHash:     userHash,
		Label:        "device-1",
		RegisteredAt: time.Now(),
	})
	_ = reg.Register(DeviceRegistration{
		MeshNodeNum:  0x00000002,
		UserHash:     userHash,
		Label:        "device-2",
		RegisteredAt: time.Now(),
	})

	devices := reg.DevicesForUser(userHash)
	if len(devices) != 2 {
		t.Fatalf("DevicesForUser returned %d devices, want 2", len(devices))
	}

	// Unregister one and verify the other remains.
	reg.Unregister(0x00000001)
	devices = reg.DevicesForUser(userHash)
	if len(devices) != 1 {
		t.Fatalf("after unregister, DevicesForUser returned %d devices, want 1", len(devices))
	}
	if devices[0].MeshNodeNum != 0x00000002 {
		t.Errorf("remaining device = 0x%08x, want 0x00000002", devices[0].MeshNodeNum)
	}
}

func TestRegistry_ActiveDeviceForUser(t *testing.T) {
	timeout := 5 * time.Minute
	reg := NewRegistry(timeout)
	userHash := testHash(0xDD)

	_ = reg.Register(DeviceRegistration{
		MeshNodeNum:  0x00000001,
		UserHash:     userHash,
		Label:        "old-device",
		RegisteredAt: time.Now(),
	})
	_ = reg.Register(DeviceRegistration{
		MeshNodeNum:  0x00000002,
		UserHash:     userHash,
		Label:        "new-device",
		RegisteredAt: time.Now(),
	})

	// Neither device has been heard yet.
	if dev := reg.ActiveDeviceForUser(userHash); dev != nil {
		t.Error("expected nil active device before any UpdateLastHeard")
	}

	// Hear device 1.
	reg.UpdateLastHeard(0x00000001, testHash(0x01), 100)
	time.Sleep(10 * time.Millisecond)
	// Hear device 2 more recently.
	reg.UpdateLastHeard(0x00000002, testHash(0x01), 200)

	active := reg.ActiveDeviceForUser(userHash)
	if active == nil {
		t.Fatal("expected non-nil active device")
	}
	if active.MeshNodeNum != 0x00000002 {
		t.Errorf("active device = 0x%08x, want 0x00000002", active.MeshNodeNum)
	}
}

func TestRegistry_ActiveDeviceForUser_Expired(t *testing.T) {
	// Use a very short timeout so devices expire quickly.
	timeout := 10 * time.Millisecond
	reg := NewRegistry(timeout)
	userHash := testHash(0xEE)

	_ = reg.Register(DeviceRegistration{
		MeshNodeNum:  0x00000001,
		UserHash:     userHash,
		Label:        "device",
		RegisteredAt: time.Now(),
	})

	reg.UpdateLastHeard(0x00000001, testHash(0x01), 100)

	// Wait for the timeout to expire.
	time.Sleep(20 * time.Millisecond)

	if dev := reg.ActiveDeviceForUser(userHash); dev != nil {
		t.Error("expected nil active device after timeout expiry")
	}
}

func TestRegistry_IsRegistered(t *testing.T) {
	reg := NewRegistry(5 * time.Minute)

	if reg.IsRegistered(0x00000001) {
		t.Error("IsRegistered returned true for empty registry")
	}

	_ = reg.Register(DeviceRegistration{
		MeshNodeNum:  0x00000001,
		UserHash:     testHash(0xFF),
		Label:        "device",
		RegisteredAt: time.Now(),
	})

	if !reg.IsRegistered(0x00000001) {
		t.Error("IsRegistered returned false for registered device")
	}
}

func TestRegistry_AllRegistrations(t *testing.T) {
	reg := NewRegistry(5 * time.Minute)

	_ = reg.Register(DeviceRegistration{
		MeshNodeNum:  0x00000001,
		UserHash:     testHash(0xAA),
		Label:        "device-1",
		RegisteredAt: time.Now(),
	})
	_ = reg.Register(DeviceRegistration{
		MeshNodeNum:  0x00000002,
		UserHash:     testHash(0xBB),
		Label:        "device-2",
		RegisteredAt: time.Now(),
	})

	all := reg.AllRegistrations()
	if len(all) != 2 {
		t.Fatalf("AllRegistrations returned %d, want 2", len(all))
	}
}

// --- DedupCache Tests ---

func TestDedupCache_DuplicateDetection(t *testing.T) {
	cache := NewDedupCache(5 * time.Minute)

	// First time: not a duplicate.
	if cache.IsDuplicate(0x1234, 42) {
		t.Error("first call should not be duplicate")
	}

	// Second time: duplicate.
	if !cache.IsDuplicate(0x1234, 42) {
		t.Error("second call should be duplicate")
	}

	// Different packet ID: not a duplicate.
	if cache.IsDuplicate(0x1234, 43) {
		t.Error("different packetID should not be duplicate")
	}

	// Different node, same packet ID: not a duplicate.
	if cache.IsDuplicate(0x5678, 42) {
		t.Error("different nodeNum should not be duplicate")
	}
}

func TestDedupCache_Expiry(t *testing.T) {
	cache := NewDedupCache(10 * time.Millisecond)

	cache.IsDuplicate(0x1234, 100)

	// Wait for the entry to expire.
	time.Sleep(20 * time.Millisecond)

	// Should no longer be considered a duplicate.
	if cache.IsDuplicate(0x1234, 100) {
		t.Error("entry should have expired")
	}
}

func TestDedupCache_Cleanup(t *testing.T) {
	cache := NewDedupCache(10 * time.Millisecond)

	cache.IsDuplicate(0x1111, 1)
	cache.IsDuplicate(0x2222, 2)

	time.Sleep(20 * time.Millisecond)

	cache.Cleanup()

	// Verify entries were cleaned up by checking they are no longer duplicates.
	if cache.IsDuplicate(0x1111, 1) {
		t.Error("entry 1 should not be duplicate after cleanup")
	}
	if cache.IsDuplicate(0x2222, 2) {
		t.Error("entry 2 should not be duplicate after cleanup")
	}
}

// --- ParseBridgeMessage Tests ---

func TestParseBridgeMessage_FullHash(t *testing.T) {
	fullHash := "aabbccddee112233aabbccddee112233"
	input := "@" + fullHash + " hello world"

	destHex, body, ok := ParseBridgeMessage(input)
	if !ok {
		t.Fatal("expected ok=true for valid full hash message")
	}
	if destHex != fullHash {
		t.Errorf("destHashHex = %q, want %q", destHex, fullHash)
	}
	if body != "hello world" {
		t.Errorf("body = %q, want %q", body, "hello world")
	}
}

func TestParseBridgeMessage_ShortHash(t *testing.T) {
	shortHash := "aabbccddeeff"
	input := "@" + shortHash + " hi there"

	destHex, body, ok := ParseBridgeMessage(input)
	if !ok {
		t.Fatal("expected ok=true for valid short hash message")
	}
	if destHex != shortHash {
		t.Errorf("destHashHex = %q, want %q", destHex, shortHash)
	}
	if body != "hi there" {
		t.Errorf("body = %q, want %q", body, "hi there")
	}
}

func TestParseBridgeMessage_LeadingWhitespace(t *testing.T) {
	input := "   @aabbccddeeff hello"

	destHex, body, ok := ParseBridgeMessage(input)
	if !ok {
		t.Fatal("expected ok=true for message with leading whitespace")
	}
	if destHex != "aabbccddeeff" {
		t.Errorf("destHashHex = %q, want %q", destHex, "aabbccddeeff")
	}
	if body != "hello" {
		t.Errorf("body = %q, want %q", body, "hello")
	}
}

func TestParseBridgeMessage_NoPrefix(t *testing.T) {
	_, _, ok := ParseBridgeMessage("hello world")
	if ok {
		t.Error("expected ok=false for message without @ prefix")
	}
}

func TestParseBridgeMessage_EmptyBody(t *testing.T) {
	_, _, ok := ParseBridgeMessage("@aabbccddeeff ")
	if ok {
		t.Error("expected ok=false for message with empty body")
	}
}

func TestParseBridgeMessage_NoSpace(t *testing.T) {
	_, _, ok := ParseBridgeMessage("@aabbccddeeff")
	if ok {
		t.Error("expected ok=false for message without space after hash")
	}
}

func TestParseBridgeMessage_InvalidHex(t *testing.T) {
	_, _, ok := ParseBridgeMessage("@gghhiijjkkll hello")
	if ok {
		t.Error("expected ok=false for invalid hex characters")
	}
}

func TestParseBridgeMessage_WrongHashLength(t *testing.T) {
	// 10 hex chars: neither 12 nor 32.
	_, _, ok := ParseBridgeMessage("@aabbccddee hello")
	if ok {
		t.Error("expected ok=false for wrong hash length")
	}
}

func TestParseBridgeMessage_EmptyString(t *testing.T) {
	_, _, ok := ParseBridgeMessage("")
	if ok {
		t.Error("expected ok=false for empty string")
	}
}

// --- FormatOutboundMessage Tests ---

func TestFormatOutboundMessage_Basic(t *testing.T) {
	hash := testHash(0xAB)
	result := FormatOutboundMessage(hash, "hello")

	shortHash := hex.EncodeToString(hash[:6])
	expected := "[" + shortHash + "] hello"
	if result != expected {
		t.Errorf("result = %q, want %q", result, expected)
	}
}

func TestFormatOutboundMessage_Truncation(t *testing.T) {
	hash := testHash(0xAB)
	longMsg := strings.Repeat("x", 300)
	result := FormatOutboundMessage(hash, longMsg)

	if len(result) > MaxOutboundTextLen {
		t.Errorf("result length = %d, exceeds MaxOutboundTextLen = %d", len(result), MaxOutboundTextLen)
	}
	if !strings.HasSuffix(result, "...") {
		t.Error("truncated message should end with ellipsis")
	}
}

func TestFormatOutboundMessage_ExactFit(t *testing.T) {
	hash := testHash(0xAB)
	shortHash := hex.EncodeToString(hash[:6])
	prefix := "[" + shortHash + "] "
	maxBody := MaxOutboundTextLen - len(prefix)
	exactMsg := strings.Repeat("a", maxBody)

	result := FormatOutboundMessage(hash, exactMsg)
	if result != prefix+exactMsg {
		t.Error("message that fits exactly should not be truncated")
	}
	if strings.HasSuffix(result, "...") {
		t.Error("non-truncated message should not end with ellipsis")
	}
}

// --- ProcessInbound Tests ---

func TestProcessInbound_FullFlow(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	nodeID := testHash(0x01)
	cfg := BridgeConfig{
		Enabled:       true,
		ActiveTimeout: 5 * time.Minute,
		DedupWindow:   5 * time.Minute,
	}
	br := New(cfg, registry, nodeID)

	userHash := testHash(0xAA)
	_ = registry.Register(DeviceRegistration{
		MeshNodeNum:  0x12345678,
		UserHash:     userHash,
		Label:        "test-radio",
		RegisteredAt: time.Now(),
	})

	destHashHex := "aabbccddee112233aabbccddee112233"
	pkt := &transport.Packet{
		Data:         []byte("@" + destHashHex + " hello relay"),
		Via:          "meshradio",
		MeshNodeNum:  0x12345678,
		MeshPacketID: 42,
	}

	result := br.ProcessInbound(pkt)
	if result == nil {
		t.Fatal("ProcessInbound returned nil for valid bridgeable packet")
	}

	if result.SourceHash != userHash {
		t.Errorf("SourceHash = %s, want %s", result.SourceHash, userHash)
	}
	if string(result.Data) != "hello relay" {
		t.Errorf("Data = %q, want %q", string(result.Data), "hello relay")
	}
	if result.Via != "lora-bridge" {
		t.Errorf("Via = %q, want %q", result.Via, "lora-bridge")
	}

	expectedDest, _ := identity.HashFromHex(destHashHex)
	if result.DestHash != expectedDest {
		t.Errorf("DestHash = %s, want %s", result.DestHash, expectedDest)
	}
}

func TestProcessInbound_UnregisteredDevice(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{DedupWindow: 5 * time.Minute}, registry, testHash(0x01))

	pkt := &transport.Packet{
		Data:         []byte("@aabbccddeeff hello"),
		Via:          "meshradio",
		MeshNodeNum:  0xDEADBEEF,
		MeshPacketID: 1,
	}

	if result := br.ProcessInbound(pkt); result != nil {
		t.Error("expected nil for unregistered device")
	}
}

func TestProcessInbound_Dedup(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{DedupWindow: 5 * time.Minute}, registry, testHash(0x01))

	_ = registry.Register(DeviceRegistration{
		MeshNodeNum:  0x11111111,
		UserHash:     testHash(0xBB),
		Label:        "device",
		RegisteredAt: time.Now(),
	})

	pkt := &transport.Packet{
		Data:         []byte("@aabbccddeeff hello"),
		Via:          "meshradio",
		MeshNodeNum:  0x11111111,
		MeshPacketID: 99,
	}

	// First: should succeed.
	if result := br.ProcessInbound(pkt); result == nil {
		t.Fatal("first ProcessInbound should succeed")
	}

	// Second: duplicate, should return nil.
	if result := br.ProcessInbound(pkt); result != nil {
		t.Error("duplicate packet should return nil")
	}
}

func TestProcessInbound_InvalidFormat(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{DedupWindow: 5 * time.Minute}, registry, testHash(0x01))

	_ = registry.Register(DeviceRegistration{
		MeshNodeNum:  0x22222222,
		UserHash:     testHash(0xCC),
		Label:        "device",
		RegisteredAt: time.Now(),
	})

	pkt := &transport.Packet{
		Data:         []byte("just a regular message without addressing"),
		Via:          "meshradio",
		MeshNodeNum:  0x22222222,
		MeshPacketID: 50,
	}

	if result := br.ProcessInbound(pkt); result != nil {
		t.Error("expected nil for non-bridge-format message")
	}
}

func TestProcessInbound_NilPacket(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{DedupWindow: 5 * time.Minute}, registry, testHash(0x01))

	if result := br.ProcessInbound(nil); result != nil {
		t.Error("expected nil for nil packet")
	}
}

func TestProcessInbound_ShortHash(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{DedupWindow: 5 * time.Minute}, registry, testHash(0x01))

	_ = registry.Register(DeviceRegistration{
		MeshNodeNum:  0x33333333,
		UserHash:     testHash(0xDD),
		Label:        "device",
		RegisteredAt: time.Now(),
	})

	shortHash := "aabbccddeeff"
	pkt := &transport.Packet{
		Data:         []byte("@" + shortHash + " short hash test"),
		Via:          "meshradio",
		MeshNodeNum:  0x33333333,
		MeshPacketID: 77,
	}

	result := br.ProcessInbound(pkt)
	if result == nil {
		t.Fatal("ProcessInbound returned nil for valid short hash message")
	}

	// Verify the destination hash has the short hash prefix in the first 6 bytes.
	expectedPrefix, _ := hex.DecodeString(shortHash)
	for i := 0; i < len(expectedPrefix); i++ {
		if result.DestHash[i] != expectedPrefix[i] {
			t.Errorf("DestHash[%d] = 0x%02x, want 0x%02x", i, result.DestHash[i], expectedPrefix[i])
		}
	}
	if string(result.Data) != "short hash test" {
		t.Errorf("Data = %q, want %q", string(result.Data), "short hash test")
	}
}

// --- FormatOutbound Tests ---

func TestFormatOutbound_Success(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{
		ActiveTimeout: 5 * time.Minute,
		DedupWindow:   5 * time.Minute,
	}, registry, testHash(0x01))

	destUser := testHash(0xEE)
	_ = registry.Register(DeviceRegistration{
		MeshNodeNum:  0x44444444,
		UserHash:     destUser,
		Label:        "device",
		RegisteredAt: time.Now(),
	})
	registry.UpdateLastHeard(0x44444444, testHash(0x01), 1)

	senderHash := testHash(0xFF)
	payload, nodeNum, err := br.FormatOutbound(destUser, senderHash, []byte("test message"))
	if err != nil {
		t.Fatalf("FormatOutbound: %v", err)
	}
	if nodeNum != 0x44444444 {
		t.Errorf("targetNodeNum = 0x%08x, want 0x44444444", nodeNum)
	}
	if !strings.Contains(string(payload), "test message") {
		t.Errorf("payload %q does not contain message", string(payload))
	}
}

func TestFormatOutbound_NoActiveDevice(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{
		ActiveTimeout: 5 * time.Minute,
		DedupWindow:   5 * time.Minute,
	}, registry, testHash(0x01))

	_, _, err := br.FormatOutbound(testHash(0xEE), testHash(0xFF), []byte("hello"))
	if err == nil {
		t.Error("expected error for user with no active device")
	}
}

// --- UserPresenceMode Tests ---

func TestUserPresenceMode_WebSocket(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{
		ActiveTimeout: 5 * time.Minute,
		DedupWindow:   5 * time.Minute,
	}, registry, testHash(0x01))

	mode := br.UserPresenceMode(testHash(0xAA), true)
	if mode != PresenceWebSocket {
		t.Errorf("mode = %q, want %q", mode, PresenceWebSocket)
	}
}

func TestUserPresenceMode_LoRa(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{
		ActiveTimeout: 5 * time.Minute,
		DedupWindow:   5 * time.Minute,
	}, registry, testHash(0x01))

	userHash := testHash(0xBB)
	_ = registry.Register(DeviceRegistration{
		MeshNodeNum:  0x55555555,
		UserHash:     userHash,
		Label:        "device",
		RegisteredAt: time.Now(),
	})
	registry.UpdateLastHeard(0x55555555, testHash(0x01), 1)

	mode := br.UserPresenceMode(userHash, false)
	if mode != PresenceLoRa {
		t.Errorf("mode = %q, want %q", mode, PresenceLoRa)
	}
}

func TestUserPresenceMode_Offline(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{
		ActiveTimeout: 5 * time.Minute,
		DedupWindow:   5 * time.Minute,
	}, registry, testHash(0x01))

	mode := br.UserPresenceMode(testHash(0xCC), false)
	if mode != PresenceOffline {
		t.Errorf("mode = %q, want %q", mode, PresenceOffline)
	}
}

func TestUserPresenceMode_WebSocketTakesPrecedence(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{
		ActiveTimeout: 5 * time.Minute,
		DedupWindow:   5 * time.Minute,
	}, registry, testHash(0x01))

	userHash := testHash(0xDD)
	_ = registry.Register(DeviceRegistration{
		MeshNodeNum:  0x66666666,
		UserHash:     userHash,
		Label:        "device",
		RegisteredAt: time.Now(),
	})
	registry.UpdateLastHeard(0x66666666, testHash(0x01), 1)

	// Even though the user has an active LoRa device, WebSocket takes precedence.
	mode := br.UserPresenceMode(userHash, true)
	if mode != PresenceWebSocket {
		t.Errorf("mode = %q, want %q (WebSocket should take precedence over LoRa)", mode, PresenceWebSocket)
	}
}

// --- ResolveShortHash Tests ---

func TestResolveShortHash_SingleMatch(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{DedupWindow: 5 * time.Minute}, registry, testHash(0x01))

	expectedHash := testHash(0xAA)
	resolver := func(prefix []byte) []identity.Hash {
		return []identity.Hash{expectedHash}
	}

	result, err := br.ResolveShortHash("aaaaaaaaaaaa", resolver)
	if err != nil {
		t.Fatalf("ResolveShortHash: %v", err)
	}
	if result != expectedHash {
		t.Errorf("result = %s, want %s", result, expectedHash)
	}
}

func TestResolveShortHash_NoMatch(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{DedupWindow: 5 * time.Minute}, registry, testHash(0x01))

	resolver := func(prefix []byte) []identity.Hash {
		return nil
	}

	_, err := br.ResolveShortHash("aabbccddeeff", resolver)
	if err == nil {
		t.Error("expected error for no match")
	}
}

func TestResolveShortHash_Ambiguous(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{DedupWindow: 5 * time.Minute}, registry, testHash(0x01))

	resolver := func(prefix []byte) []identity.Hash {
		return []identity.Hash{testHash(0xAA), testHash(0xBB)}
	}

	_, err := br.ResolveShortHash("aabbccddeeff", resolver)
	if err == nil {
		t.Error("expected error for ambiguous match")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error %q should mention ambiguous", err.Error())
	}
}

func TestResolveShortHash_InvalidLength(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{DedupWindow: 5 * time.Minute}, registry, testHash(0x01))

	resolver := func(prefix []byte) []identity.Hash { return nil }

	_, err := br.ResolveShortHash("aabb", resolver)
	if err == nil {
		t.Error("expected error for invalid length")
	}
}

// --- Start/Stop Tests ---

func TestBridge_StartStop(t *testing.T) {
	registry := NewRegistry(5 * time.Minute)
	br := New(BridgeConfig{
		Enabled:       true,
		ActiveTimeout: 5 * time.Minute,
		DedupWindow:   1 * time.Second,
	}, registry, testHash(0x01))

	ctx := context.Background()
	br.Start(ctx)

	// Give the goroutine time to start.
	time.Sleep(10 * time.Millisecond)

	br.Stop()

	// Calling Stop again should not panic.
	br.Stop()
}
