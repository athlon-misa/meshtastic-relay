package bridge

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"

	"github.com/meshtastic-relay/relay/identity"
	bolt "go.etcd.io/bbolt"
)

// bucketBridgeDevices is the BoltDB bucket name for persisted device registrations.
var bucketBridgeDevices = []byte("bridge_devices")

// registrationJSON is the JSON-serializable form of a DeviceRegistration.
// identity.Hash does not natively marshal to JSON, so we use a hex string.
type registrationJSON struct {
	MeshNodeNum  uint32 `json:"mesh_node_num"`
	UserHash     string `json:"user_hash"`
	Label        string `json:"label"`
	RegisteredAt string `json:"registered_at"`
}

// RegistryStore is a BoltDB-backed persistence layer for bridge device
// registrations. It stores each registration keyed by its 4-byte big-endian
// MeshNodeNum with a JSON-encoded value for human readability.
type RegistryStore struct {
	db *bolt.DB
}

// OpenRegistryStore opens or creates a BoltDB database at dbPath for
// persisting bridge device registrations. The caller must call Close when
// the store is no longer needed.
func OpenRegistryStore(dbPath string) (*RegistryStore, error) {
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{
		Timeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("bridge: open registry store %s: %w", dbPath, err)
	}

	// Ensure the bucket exists.
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketBridgeDevices)
		return err
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("bridge: create bucket: %w", err)
	}

	return &RegistryStore{db: db}, nil
}

// SaveRegistration persists a device registration to the store. If a
// registration with the same MeshNodeNum already exists, it is overwritten.
func (s *RegistryStore) SaveRegistration(reg DeviceRegistration) error {
	key := nodeNumKey(reg.MeshNodeNum)

	rec := registrationJSON{
		MeshNodeNum:  reg.MeshNodeNum,
		UserHash:     reg.UserHash.String(),
		Label:        reg.Label,
		RegisteredAt: reg.RegisteredAt.UTC().Format(time.RFC3339Nano),
	}

	value, err := json.Marshal(&rec)
	if err != nil {
		return fmt.Errorf("bridge: marshal registration: %w", err)
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketBridgeDevices).Put(key, value)
	})
}

// DeleteRegistration removes the registration for the given Meshtastic
// node number from the store. It is not an error if the key does not exist.
func (s *RegistryStore) DeleteRegistration(meshNodeNum uint32) error {
	key := nodeNumKey(meshNodeNum)
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketBridgeDevices).Delete(key)
	})
}

// LoadAll reads every persisted registration from the store and returns
// them as a slice. The order is by MeshNodeNum (big-endian key order).
func (s *RegistryStore) LoadAll() ([]DeviceRegistration, error) {
	var regs []DeviceRegistration

	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketBridgeDevices)
		return b.ForEach(func(k, v []byte) error {
			var rec registrationJSON
			if err := json.Unmarshal(v, &rec); err != nil {
				return fmt.Errorf("bridge: unmarshal registration key %x: %w", k, err)
			}

			userHash, err := identity.HashFromHex(rec.UserHash)
			if err != nil {
				return fmt.Errorf("bridge: parse user_hash %q: %w", rec.UserHash, err)
			}

			registeredAt, err := time.Parse(time.RFC3339Nano, rec.RegisteredAt)
			if err != nil {
				return fmt.Errorf("bridge: parse registered_at %q: %w", rec.RegisteredAt, err)
			}

			regs = append(regs, DeviceRegistration{
				MeshNodeNum:  rec.MeshNodeNum,
				UserHash:     userHash,
				Label:        rec.Label,
				RegisteredAt: registeredAt,
			})
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return regs, nil
}

// Close closes the underlying BoltDB database.
func (s *RegistryStore) Close() error {
	return s.db.Close()
}

// nodeNumKey returns a 4-byte big-endian representation of a Meshtastic
// node number, suitable as a BoltDB key.
func nodeNumKey(nodeNum uint32) []byte {
	key := make([]byte, 4)
	binary.BigEndian.PutUint32(key, nodeNum)
	return key
}
