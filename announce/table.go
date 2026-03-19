package announce

import (
	"sync"
	"time"

	"github.com/meshtastic-relay/relay/identity"
)

// PresenceChangeFunc is called when the presence table changes. The entry
// contains the user and node information; online is true for new/updated
// entries and false for removals or expirations.
//
// The callback is invoked asynchronously (in a goroutine) and must NOT
// re-enter the Table.
type PresenceChangeFunc func(entry Entry, online bool)

// Entry is a single record in the presence table, mapping a user to the
// relay node where they were last seen.
type Entry struct {
	// UserHash is the user's identity hash.
	UserHash identity.Hash
	// NodeID is the relay node where the user is connected.
	NodeID identity.Hash
	// PublicKey is the user's Ed25519 public key.
	PublicKey []byte
	// Timestamp is the Unix timestamp of the announce packet.
	Timestamp int64
	// Nonce is the random nonce from the announce packet (for replay detection).
	Nonce [NonceSize]byte
	// ExpiresAt is when this entry should be considered stale.
	ExpiresAt time.Time
}

// Table is a thread-safe presence table that maps user identity hashes
// to their current relay node. It enforces fresher-wins semantics and
// replay prevention.
type Table struct {
	mu       sync.RWMutex
	entries  map[identity.Hash]*Entry
	ttl      time.Duration
	onChange PresenceChangeFunc
}

// NewTable creates a new presence table with the given entry time-to-live.
func NewTable(ttl time.Duration) *Table {
	return &Table{
		entries: make(map[identity.Hash]*Entry),
		ttl:     ttl,
	}
}

// SetOnChange registers a callback that fires when presence entries are
// added, updated, or removed. Must be called before Start/first use.
func (t *Table) SetOnChange(fn PresenceChangeFunc) {
	t.onChange = fn
}

// notifyChange fires the onChange callback asynchronously if set.
func (t *Table) notifyChange(entry Entry, online bool) {
	if t.onChange != nil {
		go t.onChange(entry, online)
	}
}

// Update processes a validated announce packet and updates the presence table.
// Returns true if the table was updated, false if the packet was stale or
// a replay. The caller must validate the packet before calling Update.
func (t *Table) Update(pkt *Packet) bool {
	t.mu.Lock()

	existing, ok := t.entries[pkt.UserHash]
	if ok {
		// Fresher-wins: only accept if timestamp is strictly newer
		if pkt.Timestamp <= existing.Timestamp {
			t.mu.Unlock()
			return false
		}
		// Replay prevention: reject if nonce matches an existing entry
		// with the same timestamp (shouldn't happen with fresher-wins,
		// but defense in depth)
		if pkt.Timestamp == existing.Timestamp && pkt.Nonce == existing.Nonce {
			t.mu.Unlock()
			return false
		}
	}

	entry := &Entry{
		UserHash:  pkt.UserHash,
		NodeID:    pkt.NodeID,
		PublicKey: pkt.PublicKey,
		Timestamp: pkt.Timestamp,
		Nonce:     pkt.Nonce,
		ExpiresAt: time.Now().Add(t.ttl),
	}
	t.entries[pkt.UserHash] = entry
	entryCopy := *entry
	t.mu.Unlock()

	t.notifyChange(entryCopy, true)
	return true
}

// Lookup returns the presence entry for the given user hash, or nil if
// the user is not in the table or their entry has expired.
func (t *Table) Lookup(userHash identity.Hash) *Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	entry, ok := t.entries[userHash]
	if !ok {
		return nil
	}
	if time.Now().After(entry.ExpiresAt) {
		return nil
	}
	return entry
}

// NodeForUser returns the relay node ID for the given user, or a zero
// hash if the user is not known or their entry has expired.
func (t *Table) NodeForUser(userHash identity.Hash) identity.Hash {
	entry := t.Lookup(userHash)
	if entry == nil {
		return identity.Hash{}
	}
	return entry.NodeID
}

// AllEntries returns a snapshot of all non-expired entries in the table.
func (t *Table) AllEntries() []Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	now := time.Now()
	var result []Entry
	for _, e := range t.entries {
		if now.Before(e.ExpiresAt) {
			result = append(result, *e)
		}
	}
	return result
}

// Count returns the number of non-expired entries in the table.
func (t *Table) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	count := 0
	now := time.Now()
	for _, e := range t.entries {
		if now.Before(e.ExpiresAt) {
			count++
		}
	}
	return count
}

// Cleanup removes expired entries from the table.
// Returns the number of entries removed.
func (t *Table) Cleanup() int {
	t.mu.Lock()

	var removed int
	var expired []Entry
	now := time.Now()
	for hash, e := range t.entries {
		if now.After(e.ExpiresAt) {
			expired = append(expired, *e)
			delete(t.entries, hash)
			removed++
		}
	}
	t.mu.Unlock()

	for _, entry := range expired {
		t.notifyChange(entry, false)
	}
	return removed
}

// Remove deletes the entry for a specific user (e.g., on explicit disconnect).
func (t *Table) Remove(userHash identity.Hash) {
	t.mu.Lock()
	entry, ok := t.entries[userHash]
	var entryCopy Entry
	if ok {
		entryCopy = *entry
		delete(t.entries, userHash)
	}
	t.mu.Unlock()

	if ok {
		t.notifyChange(entryCopy, false)
	}
}
