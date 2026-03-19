// Package bridge implements the LoRa gateway bridge that translates between
// Meshtastic mesh traffic and the relay identity network.
//
// It handles device registration, message deduplication, address format
// parsing, and bidirectional message translation so that standard Meshtastic
// radios can exchange messages with relay-network users.
package bridge

import (
	"fmt"
	"sync"
	"time"

	"github.com/meshtastic-relay/relay/identity"
)

// DeviceRegistration maps a Meshtastic node number to a relay user identity.
type DeviceRegistration struct {
	// MeshNodeNum is the Meshtastic radio's 32-bit node number.
	MeshNodeNum uint32
	// UserHash is the relay identity hash of the device owner.
	UserHash identity.Hash
	// Label is a human-readable name for this device (e.g., "hiker-radio-1").
	Label string
	// RegisteredAt is when this device was registered.
	RegisteredAt time.Time
}

// DeviceState tracks the runtime state of a registered device, embedding
// the static registration data along with dynamic last-heard information.
type DeviceState struct {
	DeviceRegistration
	// LastHeard is when this device was last heard on the mesh.
	LastHeard time.Time
	// LastSeenByNode is the relay node identity that last received a packet
	// from this device.
	LastSeenByNode identity.Hash
	// LastPacketID is the most recent Meshtastic packet ID from this device.
	LastPacketID uint32
}

// Registry manages device registrations and runtime state. It is safe for
// concurrent use from multiple goroutines.
type Registry struct {
	mu            sync.RWMutex
	byNodeNum     map[uint32]*DeviceState
	byUser        map[identity.Hash][]*DeviceState
	activeTimeout time.Duration
	store         *RegistryStore // optional; nil means in-memory only
}

// NewRegistry creates a Registry. The activeTimeout determines how recently
// a device must have been heard to be considered "active" for outbound
// message delivery.
func NewRegistry(activeTimeout time.Duration) *Registry {
	return &Registry{
		byNodeNum:     make(map[uint32]*DeviceState),
		byUser:        make(map[identity.Hash][]*DeviceState),
		activeTimeout: activeTimeout,
	}
}

// NewPersistentRegistry creates a Registry backed by a RegistryStore. All
// existing registrations are loaded from the store on creation. Subsequent
// Register and Unregister calls are persisted automatically. Returns an
// error if the initial load fails.
func NewPersistentRegistry(activeTimeout time.Duration, store *RegistryStore) (*Registry, error) {
	r := NewRegistry(activeTimeout)
	r.store = store

	regs, err := store.LoadAll()
	if err != nil {
		return nil, fmt.Errorf("bridge: load persisted registrations: %w", err)
	}

	for _, reg := range regs {
		state := &DeviceState{
			DeviceRegistration: reg,
		}
		r.byNodeNum[reg.MeshNodeNum] = state
		r.byUser[reg.UserHash] = append(r.byUser[reg.UserHash], state)
	}

	return r, nil
}

// Register adds a device to the registry. Returns an error if a device with
// the same Meshtastic node number is already registered.
func (r *Registry) Register(reg DeviceRegistration) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.byNodeNum[reg.MeshNodeNum]; exists {
		return fmt.Errorf("bridge: node 0x%08x already registered", reg.MeshNodeNum)
	}

	state := &DeviceState{
		DeviceRegistration: reg,
	}
	r.byNodeNum[reg.MeshNodeNum] = state
	r.byUser[reg.UserHash] = append(r.byUser[reg.UserHash], state)

	if r.store != nil {
		if err := r.store.SaveRegistration(reg); err != nil {
			// Roll back in-memory state on persistence failure.
			delete(r.byNodeNum, reg.MeshNodeNum)
			userDevices := r.byUser[reg.UserHash]
			r.byUser[reg.UserHash] = userDevices[:len(userDevices)-1]
			if len(r.byUser[reg.UserHash]) == 0 {
				delete(r.byUser, reg.UserHash)
			}
			return fmt.Errorf("bridge: persist registration: %w", err)
		}
	}

	return nil
}

// Unregister removes a device from the registry by its Meshtastic node number.
func (r *Registry) Unregister(meshNodeNum uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()

	state, exists := r.byNodeNum[meshNodeNum]
	if !exists {
		return
	}

	delete(r.byNodeNum, meshNodeNum)

	// Remove from the user's device list.
	userDevices := r.byUser[state.UserHash]
	for i, ds := range userDevices {
		if ds.MeshNodeNum == meshNodeNum {
			r.byUser[state.UserHash] = append(userDevices[:i], userDevices[i+1:]...)
			break
		}
	}
	if len(r.byUser[state.UserHash]) == 0 {
		delete(r.byUser, state.UserHash)
	}

	if r.store != nil {
		// Best-effort: log but do not propagate store errors on unregister,
		// since the in-memory state has already been updated.
		_ = r.store.DeleteRegistration(meshNodeNum)
	}
}

// LookupByNodeNum returns the device state for the given Meshtastic node
// number, or nil if no device with that number is registered.
func (r *Registry) LookupByNodeNum(meshNodeNum uint32) *DeviceState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byNodeNum[meshNodeNum]
}

// DevicesForUser returns all registered devices for the given user identity
// hash. Returns nil if no devices are registered for this user.
func (r *Registry) DevicesForUser(userHash identity.Hash) []*DeviceState {
	r.mu.RLock()
	defer r.mu.RUnlock()

	devices := r.byUser[userHash]
	if len(devices) == 0 {
		return nil
	}

	// Return a copy to prevent callers from mutating the internal slice.
	result := make([]*DeviceState, len(devices))
	copy(result, devices)
	return result
}

// ActiveDeviceForUser returns the most recently heard device for the given
// user that was active within the configured activeTimeout. Returns nil if
// the user has no active devices.
func (r *Registry) ActiveDeviceForUser(userHash identity.Hash) *DeviceState {
	r.mu.RLock()
	defer r.mu.RUnlock()

	devices := r.byUser[userHash]
	if len(devices) == 0 {
		return nil
	}

	cutoff := time.Now().Add(-r.activeTimeout)
	var best *DeviceState
	for _, ds := range devices {
		if ds.LastHeard.Before(cutoff) {
			continue
		}
		if best == nil || ds.LastHeard.After(best.LastHeard) {
			best = ds
		}
	}
	return best
}

// UpdateLastHeard updates the last-heard timestamp and metadata for a
// registered device. If the device is not registered, this is a no-op.
func (r *Registry) UpdateLastHeard(meshNodeNum uint32, seenByNode identity.Hash, packetID uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()

	state, exists := r.byNodeNum[meshNodeNum]
	if !exists {
		return
	}

	state.LastHeard = time.Now()
	state.LastSeenByNode = seenByNode
	state.LastPacketID = packetID
}

// IsRegistered reports whether a device with the given Meshtastic node
// number is registered.
func (r *Registry) IsRegistered(meshNodeNum uint32) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.byNodeNum[meshNodeNum]
	return exists
}

// AllRegistrations returns a snapshot of all current device registrations.
func (r *Registry) AllRegistrations() []DeviceRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]DeviceRegistration, 0, len(r.byNodeNum))
	for _, state := range r.byNodeNum {
		result = append(result, state.DeviceRegistration)
	}
	return result
}
