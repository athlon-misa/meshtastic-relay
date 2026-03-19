package bridge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegistryStore_SaveAndLoad(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-registry.db")

	// Open store, save two registrations, then close.
	s, err := OpenRegistryStore(dbPath)
	if err != nil {
		t.Fatalf("OpenRegistryStore: %v", err)
	}

	reg1 := DeviceRegistration{
		MeshNodeNum:  0x11111111,
		UserHash:     testHash(0xAA),
		Label:        "device-one",
		RegisteredAt: time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
	}
	reg2 := DeviceRegistration{
		MeshNodeNum:  0x22222222,
		UserHash:     testHash(0xBB),
		Label:        "device-two",
		RegisteredAt: time.Date(2026, 2, 20, 14, 30, 0, 0, time.UTC),
	}

	if err := s.SaveRegistration(reg1); err != nil {
		t.Fatalf("SaveRegistration reg1: %v", err)
	}
	if err := s.SaveRegistration(reg2); err != nil {
		t.Fatalf("SaveRegistration reg2: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and verify both registrations are present.
	s2, err := OpenRegistryStore(dbPath)
	if err != nil {
		t.Fatalf("OpenRegistryStore (reopen): %v", err)
	}
	defer s2.Close()

	loaded, err := s2.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("LoadAll returned %d registrations, want 2", len(loaded))
	}

	// BoltDB iterates keys in sorted order (big-endian), so 0x11111111 < 0x22222222.
	if loaded[0].MeshNodeNum != 0x11111111 {
		t.Errorf("loaded[0].MeshNodeNum = 0x%08x, want 0x11111111", loaded[0].MeshNodeNum)
	}
	if loaded[0].UserHash != testHash(0xAA) {
		t.Errorf("loaded[0].UserHash = %s, want %s", loaded[0].UserHash, testHash(0xAA))
	}
	if loaded[0].Label != "device-one" {
		t.Errorf("loaded[0].Label = %q, want %q", loaded[0].Label, "device-one")
	}
	if !loaded[0].RegisteredAt.Equal(reg1.RegisteredAt) {
		t.Errorf("loaded[0].RegisteredAt = %v, want %v", loaded[0].RegisteredAt, reg1.RegisteredAt)
	}

	if loaded[1].MeshNodeNum != 0x22222222 {
		t.Errorf("loaded[1].MeshNodeNum = 0x%08x, want 0x22222222", loaded[1].MeshNodeNum)
	}
	if loaded[1].UserHash != testHash(0xBB) {
		t.Errorf("loaded[1].UserHash = %s, want %s", loaded[1].UserHash, testHash(0xBB))
	}
	if loaded[1].Label != "device-two" {
		t.Errorf("loaded[1].Label = %q, want %q", loaded[1].Label, "device-two")
	}
}

func TestRegistryStore_Delete(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-registry.db")

	s, err := OpenRegistryStore(dbPath)
	if err != nil {
		t.Fatalf("OpenRegistryStore: %v", err)
	}
	defer s.Close()

	reg := DeviceRegistration{
		MeshNodeNum:  0xDEADBEEF,
		UserHash:     testHash(0xCC),
		Label:        "to-delete",
		RegisteredAt: time.Now().UTC(),
	}

	if err := s.SaveRegistration(reg); err != nil {
		t.Fatalf("SaveRegistration: %v", err)
	}

	// Verify it was saved.
	loaded, err := s.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll before delete: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("LoadAll returned %d, want 1", len(loaded))
	}

	// Delete it.
	if err := s.DeleteRegistration(0xDEADBEEF); err != nil {
		t.Fatalf("DeleteRegistration: %v", err)
	}

	// Verify it is gone.
	loaded, err = s.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll after delete: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("LoadAll returned %d after delete, want 0", len(loaded))
	}
}

func TestRegistryStore_DeleteNonexistent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-registry.db")

	s, err := OpenRegistryStore(dbPath)
	if err != nil {
		t.Fatalf("OpenRegistryStore: %v", err)
	}
	defer s.Close()

	// Deleting a key that does not exist should not return an error.
	if err := s.DeleteRegistration(0x99999999); err != nil {
		t.Fatalf("DeleteRegistration of nonexistent key: %v", err)
	}
}

func TestRegistryStore_LoadAllEmpty(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-registry.db")

	s, err := OpenRegistryStore(dbPath)
	if err != nil {
		t.Fatalf("OpenRegistryStore: %v", err)
	}
	defer s.Close()

	loaded, err := s.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("LoadAll on empty store returned %d, want 0", len(loaded))
	}
}

func TestRegistryStore_SaveOverwrite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-registry.db")

	s, err := OpenRegistryStore(dbPath)
	if err != nil {
		t.Fatalf("OpenRegistryStore: %v", err)
	}
	defer s.Close()

	reg := DeviceRegistration{
		MeshNodeNum:  0x12345678,
		UserHash:     testHash(0xDD),
		Label:        "original-label",
		RegisteredAt: time.Now().UTC(),
	}

	if err := s.SaveRegistration(reg); err != nil {
		t.Fatalf("SaveRegistration (original): %v", err)
	}

	// Overwrite with updated label.
	reg.Label = "updated-label"
	if err := s.SaveRegistration(reg); err != nil {
		t.Fatalf("SaveRegistration (overwrite): %v", err)
	}

	loaded, err := s.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("LoadAll returned %d, want 1 (overwrite should not duplicate)", len(loaded))
	}
	if loaded[0].Label != "updated-label" {
		t.Errorf("Label = %q, want %q", loaded[0].Label, "updated-label")
	}
}

func TestPersistentRegistry_SurvivesRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-registry.db")

	// Phase 1: create a persistent registry and register devices.
	store1, err := OpenRegistryStore(dbPath)
	if err != nil {
		t.Fatalf("OpenRegistryStore: %v", err)
	}

	reg1, err := NewPersistentRegistry(5*time.Minute, store1)
	if err != nil {
		t.Fatalf("NewPersistentRegistry: %v", err)
	}

	if err := reg1.Register(DeviceRegistration{
		MeshNodeNum:  0xAAAAAAAA,
		UserHash:     testHash(0x11),
		Label:        "alpha",
		RegisteredAt: time.Now(),
	}); err != nil {
		t.Fatalf("Register alpha: %v", err)
	}

	if err := reg1.Register(DeviceRegistration{
		MeshNodeNum:  0xBBBBBBBB,
		UserHash:     testHash(0x22),
		Label:        "bravo",
		RegisteredAt: time.Now(),
	}); err != nil {
		t.Fatalf("Register bravo: %v", err)
	}

	// Close the store (simulating daemon shutdown).
	if err := store1.Close(); err != nil {
		t.Fatalf("Close store1: %v", err)
	}

	// Phase 2: reopen the store and create a new registry from the same DB.
	store2, err := OpenRegistryStore(dbPath)
	if err != nil {
		t.Fatalf("OpenRegistryStore (reopen): %v", err)
	}
	defer store2.Close()

	reg2, err := NewPersistentRegistry(5*time.Minute, store2)
	if err != nil {
		t.Fatalf("NewPersistentRegistry (reopen): %v", err)
	}

	// Verify both devices survived the restart.
	if !reg2.IsRegistered(0xAAAAAAAA) {
		t.Error("device 0xAAAAAAAA not found after restart")
	}
	if !reg2.IsRegistered(0xBBBBBBBB) {
		t.Error("device 0xBBBBBBBB not found after restart")
	}

	stateA := reg2.LookupByNodeNum(0xAAAAAAAA)
	if stateA == nil {
		t.Fatal("LookupByNodeNum 0xAAAAAAAA returned nil")
	}
	if stateA.Label != "alpha" {
		t.Errorf("label = %q, want %q", stateA.Label, "alpha")
	}
	if stateA.UserHash != testHash(0x11) {
		t.Errorf("UserHash = %s, want %s", stateA.UserHash, testHash(0x11))
	}

	stateB := reg2.LookupByNodeNum(0xBBBBBBBB)
	if stateB == nil {
		t.Fatal("LookupByNodeNum 0xBBBBBBBB returned nil")
	}
	if stateB.Label != "bravo" {
		t.Errorf("label = %q, want %q", stateB.Label, "bravo")
	}

	// Verify user-based lookup works.
	devicesForUser11 := reg2.DevicesForUser(testHash(0x11))
	if len(devicesForUser11) != 1 {
		t.Errorf("DevicesForUser(0x11) = %d, want 1", len(devicesForUser11))
	}

	all := reg2.AllRegistrations()
	if len(all) != 2 {
		t.Errorf("AllRegistrations = %d, want 2", len(all))
	}
}

func TestPersistentRegistry_UnregisterPersists(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-registry.db")

	store1, err := OpenRegistryStore(dbPath)
	if err != nil {
		t.Fatalf("OpenRegistryStore: %v", err)
	}

	reg1, err := NewPersistentRegistry(5*time.Minute, store1)
	if err != nil {
		t.Fatalf("NewPersistentRegistry: %v", err)
	}

	if err := reg1.Register(DeviceRegistration{
		MeshNodeNum:  0x11111111,
		UserHash:     testHash(0xAA),
		Label:        "keeper",
		RegisteredAt: time.Now(),
	}); err != nil {
		t.Fatalf("Register keeper: %v", err)
	}
	if err := reg1.Register(DeviceRegistration{
		MeshNodeNum:  0x22222222,
		UserHash:     testHash(0xBB),
		Label:        "goner",
		RegisteredAt: time.Now(),
	}); err != nil {
		t.Fatalf("Register goner: %v", err)
	}

	// Unregister the second device.
	reg1.Unregister(0x22222222)

	if err := store1.Close(); err != nil {
		t.Fatalf("Close store1: %v", err)
	}

	// Reopen and verify only the first device remains.
	store2, err := OpenRegistryStore(dbPath)
	if err != nil {
		t.Fatalf("OpenRegistryStore (reopen): %v", err)
	}
	defer store2.Close()

	reg2, err := NewPersistentRegistry(5*time.Minute, store2)
	if err != nil {
		t.Fatalf("NewPersistentRegistry (reopen): %v", err)
	}

	if !reg2.IsRegistered(0x11111111) {
		t.Error("device 0x11111111 should still be registered")
	}
	if reg2.IsRegistered(0x22222222) {
		t.Error("device 0x22222222 should have been unregistered")
	}

	all := reg2.AllRegistrations()
	if len(all) != 1 {
		t.Errorf("AllRegistrations = %d, want 1", len(all))
	}
}

func TestPersistentRegistry_IdempotentRegister(t *testing.T) {
	// Verify that registering a device that already exists (loaded from store)
	// returns an error, keeping the behavior consistent.
	dbPath := filepath.Join(t.TempDir(), "test-registry.db")

	store1, err := OpenRegistryStore(dbPath)
	if err != nil {
		t.Fatalf("OpenRegistryStore: %v", err)
	}

	reg1, err := NewPersistentRegistry(5*time.Minute, store1)
	if err != nil {
		t.Fatalf("NewPersistentRegistry: %v", err)
	}

	dev := DeviceRegistration{
		MeshNodeNum:  0xCAFEBABE,
		UserHash:     testHash(0xDD),
		Label:        "idempotent",
		RegisteredAt: time.Now(),
	}
	if err := reg1.Register(dev); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := store1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen — the device should be loaded from store.
	store2, err := OpenRegistryStore(dbPath)
	if err != nil {
		t.Fatalf("OpenRegistryStore (reopen): %v", err)
	}
	defer store2.Close()

	reg2, err := NewPersistentRegistry(5*time.Minute, store2)
	if err != nil {
		t.Fatalf("NewPersistentRegistry (reopen): %v", err)
	}

	// Trying to register the same node num again should fail.
	err = reg2.Register(dev)
	if err == nil {
		t.Error("expected error when re-registering already-loaded device")
	}
}

func TestOpenRegistryStore_InvalidPath(t *testing.T) {
	// Attempting to open a DB in a nonexistent directory should fail.
	dbPath := filepath.Join(t.TempDir(), "nonexistent", "subdir", "test.db")

	_, err := OpenRegistryStore(dbPath)
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestRegistryStore_FileCreated(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "new-registry.db")

	s, err := OpenRegistryStore(dbPath)
	if err != nil {
		t.Fatalf("OpenRegistryStore: %v", err)
	}
	s.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file was not created")
	}
}
