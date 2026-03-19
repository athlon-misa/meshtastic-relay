// Package alert implements high-priority emergency broadcast messages that are
// sent via dual-path (internet and LoRa simultaneously) to reach all connected
// users with maximum reliability.
package alert

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Priority represents the urgency level of an emergency alert.
type Priority int

const (
	// PriorityCritical indicates a life-threatening emergency.
	PriorityCritical Priority = 0
	// PriorityHigh indicates an important situation that is not life-threatening.
	PriorityHigh Priority = 1
	// PriorityNormal indicates a standard alert.
	PriorityNormal Priority = 2
)

// Alert represents an emergency broadcast message distributed to all users.
type Alert struct {
	// ID is a random 16-byte hex identifier for deduplication.
	ID string `json:"id"`
	// Priority is the urgency level of the alert.
	Priority Priority `json:"priority"`
	// SenderHash is the hex-encoded identity hash of the user who sent the alert.
	SenderHash string `json:"sender_hash"`
	// NodeID is the hex-encoded identity hash of the originating relay node.
	NodeID string `json:"node_id"`
	// Text is the alert message body.
	Text string `json:"text"`
	// Timestamp is the Unix timestamp when the alert was created.
	Timestamp int64 `json:"timestamp"`
	// ExpiresAt is the Unix timestamp after which the alert should no longer be displayed.
	ExpiresAt int64 `json:"expires_at"`
}

// New creates a new Alert with a random ID and the given parameters.
// The ttl parameter controls how long the alert remains valid.
func New(senderHash, nodeID, text string, priority Priority, ttl time.Duration) *Alert {
	now := time.Now()
	return &Alert{
		ID:         generateAlertID(),
		Priority:   priority,
		SenderHash: senderHash,
		NodeID:     nodeID,
		Text:       text,
		Timestamp:  now.Unix(),
		ExpiresAt:  now.Add(ttl).Unix(),
	}
}

// Marshal encodes an Alert to JSON.
func Marshal(a *Alert) ([]byte, error) {
	data, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("alert: marshal: %w", err)
	}
	return data, nil
}

// Unmarshal decodes JSON bytes into an Alert. Returns an error if the data
// is not a valid alert or is missing required fields.
func Unmarshal(data []byte) (*Alert, error) {
	var a Alert
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("alert: unmarshal: %w", err)
	}
	if a.ID == "" || a.Text == "" {
		return nil, fmt.Errorf("alert: unmarshal: missing required fields (id, text)")
	}
	return &a, nil
}

// IsExpired reports whether the alert has passed its expiration time.
func IsExpired(a *Alert) bool {
	return time.Now().Unix() > a.ExpiresAt
}

// generateAlertID produces a random 16-byte hex string for alert identification.
func generateAlertID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails (should never happen).
		return fmt.Sprintf("alert-%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
