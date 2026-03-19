// Package store implements a persistent store-and-forward message queue
// using BoltDB. Messages destined for offline users are stored and delivered
// when they reconnect.
package store

import (
	"encoding/binary"
	"fmt"
	"log"
	"time"

	"github.com/meshtastic-relay/relay/identity"
	bolt "go.etcd.io/bbolt"
)

// Bucket names used in the BoltDB database.
var (
	bucketMessages = []byte("messages")
	bucketMeta     = []byte("meta")
)

// StoredMessage is a message waiting for delivery, with metadata for
// expiry and tracking.
type StoredMessage struct {
	// ID is a unique, monotonically increasing identifier for this stored message.
	ID uint64
	// DestHash is the intended recipient's identity hash.
	DestHash identity.Hash
	// Data is the raw wire-format message bytes.
	Data []byte
	// StoredAt is when the message was stored (Unix timestamp).
	StoredAt int64
	// ExpiresAt is when the message should be deleted (Unix timestamp).
	ExpiresAt int64
	// Delivered is whether the message has been successfully delivered.
	Delivered bool
}

// Store is a persistent store-and-forward message queue backed by BoltDB.
type Store struct {
	db              *bolt.DB
	maxAge          time.Duration
	maxPerRecipient int
}

// Open opens or creates a BoltDB store at the given path.
func Open(dbPath string, maxAge time.Duration, maxPerRecipient int) (*Store, error) {
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{
		Timeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", dbPath, err)
	}

	// Create buckets
	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketMessages); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(bucketMeta); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("store: create buckets: %w", err)
	}

	return &Store{
		db:              db,
		maxAge:          maxAge,
		maxPerRecipient: maxPerRecipient,
	}, nil
}

// Close closes the BoltDB database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Enqueue stores a message for later delivery to the specified recipient.
// Returns the assigned message ID.
func (s *Store) Enqueue(destHash identity.Hash, data []byte) (uint64, error) {
	var msgID uint64
	now := time.Now()

	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketMessages)

		// Generate a unique, sequential ID
		id, err := bucket.NextSequence()
		if err != nil {
			return fmt.Errorf("next sequence: %w", err)
		}
		msgID = id

		// Create the stored message record
		msg := StoredMessage{
			ID:        id,
			DestHash:  destHash,
			Data:      data,
			StoredAt:  now.Unix(),
			ExpiresAt: now.Add(s.maxAge).Unix(),
			Delivered: false,
		}

		// Key format: dest_hash(16) + msg_id(8)
		key := makeKey(destHash, id)
		value := encodeMessage(&msg)

		return bucket.Put(key, value)
	})

	if err != nil {
		return 0, fmt.Errorf("store: enqueue: %w", err)
	}
	return msgID, nil
}

// Dequeue retrieves all undelivered messages for the given recipient,
// ordered by ID (oldest first). Does not mark them as delivered.
func (s *Store) Dequeue(destHash identity.Hash) ([]StoredMessage, error) {
	var messages []StoredMessage

	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketMessages)
		c := bucket.Cursor()

		prefix := destHash[:]
		now := time.Now().Unix()

		for k, v := c.Seek(prefix); k != nil; k, v = c.Next() {
			// Stop when we pass this recipient's prefix
			if len(k) < identity.HashSize {
				break
			}
			keyHash := k[:identity.HashSize]
			match := true
			for i := 0; i < identity.HashSize; i++ {
				if keyHash[i] != prefix[i] {
					match = false
					break
				}
			}
			if !match {
				break
			}

			msg := decodeMessage(k, v)
			if msg == nil {
				continue
			}
			if msg.Delivered {
				continue
			}
			if msg.ExpiresAt < now {
				continue
			}
			messages = append(messages, *msg)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("store: dequeue: %w", err)
	}
	return messages, nil
}

// MarkDelivered marks a specific message as delivered.
func (s *Store) MarkDelivered(destHash identity.Hash, msgID uint64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketMessages)
		key := makeKey(destHash, msgID)
		v := bucket.Get(key)
		if v == nil {
			return fmt.Errorf("message %d not found", msgID)
		}

		msg := decodeMessage(key, v)
		if msg == nil {
			return fmt.Errorf("message %d corrupt", msgID)
		}
		msg.Delivered = true

		return bucket.Put(key, encodeMessage(msg))
	})
}

// Delete removes a specific message from the store.
func (s *Store) Delete(destHash identity.Hash, msgID uint64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		key := makeKey(destHash, msgID)
		return tx.Bucket(bucketMessages).Delete(key)
	})
}

// Cleanup removes expired and delivered messages from the store.
// Returns the number of messages removed.
func (s *Store) Cleanup() (int, error) {
	removed := 0
	now := time.Now().Unix()

	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketMessages)
		c := bucket.Cursor()

		var toDelete [][]byte

		for k, v := c.First(); k != nil; k, v = c.Next() {
			msg := decodeMessage(k, v)
			if msg == nil {
				toDelete = append(toDelete, copyBytes(k))
				continue
			}
			if msg.ExpiresAt < now || msg.Delivered {
				toDelete = append(toDelete, copyBytes(k))
			}
		}

		for _, key := range toDelete {
			if err := bucket.Delete(key); err != nil {
				return err
			}
			removed++
		}

		return nil
	})

	if err != nil {
		return 0, fmt.Errorf("store: cleanup: %w", err)
	}

	if removed > 0 {
		log.Printf("store: cleanup removed %d messages", removed)
	}
	return removed, nil
}

// Count returns the total number of messages in the store (including expired).
func (s *Store) Count() (int, error) {
	var count int
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketMessages)
		count = bucket.Stats().KeyN
		return nil
	})
	return count, err
}

// CountForRecipient returns the number of stored messages for a specific recipient.
func (s *Store) CountForRecipient(destHash identity.Hash) (int, error) {
	var count int
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketMessages)
		c := bucket.Cursor()
		prefix := destHash[:]

		for k, _ := c.Seek(prefix); k != nil; k, _ = c.Next() {
			if len(k) < identity.HashSize {
				break
			}
			match := true
			for i := 0; i < identity.HashSize; i++ {
				if k[i] != prefix[i] {
					match = false
					break
				}
			}
			if !match {
				break
			}
			count++
		}
		return nil
	})
	return count, err
}

// makeKey creates a BoltDB key: dest_hash(16) + msg_id(8)
func makeKey(destHash identity.Hash, id uint64) []byte {
	key := make([]byte, identity.HashSize+8)
	copy(key[:identity.HashSize], destHash[:])
	binary.BigEndian.PutUint64(key[identity.HashSize:], id)
	return key
}

// encodeMessage serializes a StoredMessage to bytes for BoltDB storage.
// Format: stored_at(8) + expires_at(8) + delivered(1) + data(remaining)
func encodeMessage(msg *StoredMessage) []byte {
	buf := make([]byte, 17+len(msg.Data))
	binary.BigEndian.PutUint64(buf[0:8], uint64(msg.StoredAt))
	binary.BigEndian.PutUint64(buf[8:16], uint64(msg.ExpiresAt))
	if msg.Delivered {
		buf[16] = 1
	}
	copy(buf[17:], msg.Data)
	return buf
}

// decodeMessage deserializes a StoredMessage from BoltDB key/value.
func decodeMessage(key, value []byte) *StoredMessage {
	if len(value) < 17 || len(key) < identity.HashSize+8 {
		return nil
	}

	var destHash identity.Hash
	copy(destHash[:], key[:identity.HashSize])
	msgID := binary.BigEndian.Uint64(key[identity.HashSize:])

	return &StoredMessage{
		ID:        msgID,
		DestHash:  destHash,
		StoredAt:  int64(binary.BigEndian.Uint64(value[0:8])),
		ExpiresAt: int64(binary.BigEndian.Uint64(value[8:16])),
		Delivered: value[16] == 1,
		Data:      value[17:],
	}
}

// copyBytes returns a copy of a byte slice (needed because BoltDB keys
// are only valid within a transaction).
func copyBytes(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
