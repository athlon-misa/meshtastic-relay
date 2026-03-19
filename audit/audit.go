// Package audit provides structured security event logging for the relay daemon.
// Audit events are logged as structured JSON lines to a dedicated audit log file.
package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// EventType identifies the category of a security audit event.
type EventType string

const (
	EventAuthAttempt      EventType = "auth_attempt"
	EventAuthSuccess      EventType = "auth_success"
	EventAuthFailure      EventType = "auth_failure"
	EventSessionStart     EventType = "session_start"
	EventSessionEnd       EventType = "session_end"
	EventRateLimit        EventType = "rate_limit"
	EventAlertBroadcast   EventType = "alert_broadcast"
	EventBridgeRegister   EventType = "bridge_register"
	EventBridgeUnregister EventType = "bridge_unregister"
	EventUpdateCheck      EventType = "update_check"
	EventAdminAction      EventType = "admin_action"
)

// Event is a single structured audit log entry.
type Event struct {
	Timestamp string    `json:"timestamp"`
	Type      EventType `json:"type"`
	UserHash  string    `json:"user_hash"`
	SourceIP  string    `json:"source_ip"`
	NodeID    string    `json:"node_id"`
	Detail    string    `json:"detail"`
	Success   bool      `json:"success"`
}

// Logger writes structured audit events as JSON lines.
type Logger struct {
	mu      sync.Mutex
	writer  io.Writer
	nodeID  string
	encoder *json.Encoder
}

// NewLogger creates a new audit logger that writes JSON events to w.
func NewLogger(w io.Writer, nodeID string) *Logger {
	return &Logger{
		writer:  w,
		nodeID:  nodeID,
		encoder: json.NewEncoder(w),
	}
}

// Log writes a structured audit event with the current timestamp.
// It is safe for concurrent use.
func (l *Logger) Log(eventType EventType, userHash, sourceIP, detail string, success bool) {
	l.LogEvent(Event{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Type:      eventType,
		UserHash:  userHash,
		SourceIP:  sourceIP,
		NodeID:    l.nodeID,
		Detail:    detail,
		Success:   success,
	})
}

// LogEvent writes a pre-built Event as a JSON line. It is safe for
// concurrent use.
func (l *Logger) LogEvent(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Errors writing audit logs are silently dropped; the daemon must
	// not crash because the audit sink is unavailable.
	_ = l.encoder.Encode(e)
}

// NewFileLogger opens (or creates) the file at path in append mode with
// 0600 permissions and returns a Logger writing to it.
func NewFileLogger(path string, nodeID string) (*Logger, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("audit: open log file %s: %w", path, err)
	}
	return NewLogger(f, nodeID), nil
}
