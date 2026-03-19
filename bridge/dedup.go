package bridge

import (
	"sync"
	"time"
)

// DedupCache prevents the same LoRa message from being bridged multiple
// times. This is necessary because Meshtastic rebroadcasts packets, so the
// same (sender, packetID) pair can arrive at the relay node more than once.
//
// The cache key is (sender_node_num << 32) | packet_id, providing a unique
// 64-bit identifier for each original transmission. Entries expire after
// the configured TTL.
type DedupCache struct {
	mu      sync.Mutex
	entries map[uint64]time.Time
	ttl     time.Duration
}

// NewDedupCache creates a DedupCache with the given time-to-live for entries.
// Typical TTL is 5-10 minutes, matching the Meshtastic rebroadcast window.
func NewDedupCache(ttl time.Duration) *DedupCache {
	return &DedupCache{
		entries: make(map[uint64]time.Time),
		ttl:     ttl,
	}
}

// IsDuplicate returns true if a packet with the same (nodeNum, packetID)
// pair has been seen within the TTL window. If it is not a duplicate, the
// entry is recorded for future dedup checks.
func (d *DedupCache) IsDuplicate(nodeNum, packetID uint32) bool {
	key := dedupKey(nodeNum, packetID)
	now := time.Now()

	d.mu.Lock()
	defer d.mu.Unlock()

	if expiry, exists := d.entries[key]; exists {
		if now.Before(expiry) {
			return true
		}
		// Entry expired; treat as new.
	}

	d.entries[key] = now.Add(d.ttl)
	return false
}

// Cleanup removes expired entries from the cache. This should be called
// periodically (e.g., every TTL interval) to prevent unbounded growth.
func (d *DedupCache) Cleanup() {
	now := time.Now()

	d.mu.Lock()
	defer d.mu.Unlock()

	for key, expiry := range d.entries {
		if now.After(expiry) {
			delete(d.entries, key)
		}
	}
}

// dedupKey combines a Meshtastic node number and packet ID into a single
// 64-bit cache key.
func dedupKey(nodeNum, packetID uint32) uint64 {
	return (uint64(nodeNum) << 32) | uint64(packetID)
}
