// Package power provides power management tiers for the relay daemon.
//
// Relay nodes running on battery or solar power can conserve energy by
// reducing activity based on available power. The Manager tracks the
// current power tier and exposes a Profile that goroutines check on
// each tick to adjust their behavior at runtime without restart.
package power

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Tier represents a power management tier that controls how aggressively
// the relay daemon operates.
type Tier int

const (
	// TierFull means unlimited power (mains). All features enabled,
	// aggressive announce intervals, no restrictions.
	TierFull Tier = 0

	// TierNormal means good power (solar with battery). Standard
	// intervals, all features enabled.
	TierNormal Tier = 1

	// TierConserve means low power. Reduce announce frequency, increase
	// cleanup intervals, limit max sessions.
	TierConserve Tier = 2

	// TierEmergency means critical power. Minimal operation: disable
	// periodic announces, disable store cleanup, accept only a few
	// sessions, prioritize receiving over sending.
	TierEmergency Tier = 3
)

// String returns the human-readable tier name.
func (t Tier) String() string {
	switch t {
	case TierFull:
		return "full"
	case TierNormal:
		return "normal"
	case TierConserve:
		return "conserve"
	case TierEmergency:
		return "emergency"
	default:
		return fmt.Sprintf("unknown(%d)", int(t))
	}
}

// ParseTier converts a string to a Tier. It is case-insensitive and
// defaults to TierFull for unrecognized values, returning an error.
func ParseTier(s string) (Tier, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "full", "":
		return TierFull, nil
	case "normal":
		return TierNormal, nil
	case "conserve":
		return TierConserve, nil
	case "emergency":
		return TierEmergency, nil
	default:
		return TierFull, fmt.Errorf("power: unknown tier %q (valid: full, normal, conserve, emergency)", s)
	}
}

// Profile defines operational parameters for a power tier. Goroutines
// read these values on each tick to dynamically adjust behavior when
// the tier changes at runtime.
type Profile struct {
	Tier               Tier
	AnnounceInterval   time.Duration // How often to broadcast presence
	CleanupInterval    time.Duration // Store cleanup frequency
	SessionTimeout     time.Duration // How long before idle sessions are dropped
	MaxSessions        int           // Maximum concurrent sessions (0 = no limit)
	HeartbeatInterval  time.Duration // Heartbeat frequency
	EnableStoreCleanup bool          // Whether to run periodic cleanup
	EnableAnnounce     bool          // Whether to broadcast presence
}

// DefaultProfile returns the default profile for the given tier.
func DefaultProfile(tier Tier) Profile {
	switch tier {
	case TierFull:
		return Profile{
			Tier:               TierFull,
			AnnounceInterval:   60 * time.Second,
			CleanupInterval:    1 * time.Hour,
			SessionTimeout:     120 * time.Second,
			MaxSessions:        100,
			HeartbeatInterval:  30 * time.Second,
			EnableStoreCleanup: true,
			EnableAnnounce:     true,
		}
	case TierNormal:
		return Profile{
			Tier:               TierNormal,
			AnnounceInterval:   90 * time.Second,
			CleanupInterval:    2 * time.Hour,
			SessionTimeout:     120 * time.Second,
			MaxSessions:        50,
			HeartbeatInterval:  30 * time.Second,
			EnableStoreCleanup: true,
			EnableAnnounce:     true,
		}
	case TierConserve:
		return Profile{
			Tier:               TierConserve,
			AnnounceInterval:   300 * time.Second,
			CleanupInterval:    6 * time.Hour,
			SessionTimeout:     60 * time.Second,
			MaxSessions:        10,
			HeartbeatInterval:  60 * time.Second,
			EnableStoreCleanup: true,
			EnableAnnounce:     true,
		}
	case TierEmergency:
		return Profile{
			Tier:               TierEmergency,
			AnnounceInterval:   0, // disabled
			CleanupInterval:    0, // disabled
			SessionTimeout:     30 * time.Second,
			MaxSessions:        2,
			HeartbeatInterval:  120 * time.Second,
			EnableStoreCleanup: false,
			EnableAnnounce:     false,
		}
	default:
		return DefaultProfile(TierFull)
	}
}

// Manager tracks the current power tier and notifies subscribers when
// it changes. All methods are safe for concurrent use.
type Manager struct {
	mu       sync.RWMutex
	current  Profile
	onChange func(Profile)
}

// NewManager creates a Manager initialized to the given tier's default
// profile.
func NewManager(initial Tier) *Manager {
	return &Manager{
		current: DefaultProfile(initial),
	}
}

// Current returns the active power profile. Goroutines should call this
// on each tick rather than caching the profile, so that runtime tier
// changes take effect immediately.
func (m *Manager) Current() Profile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// SetTier switches to a new power tier. If an onChange callback is
// registered it is called synchronously with the new profile.
func (m *Manager) SetTier(tier Tier) {
	m.mu.Lock()
	prev := m.current.Tier
	m.current = DefaultProfile(tier)
	fn := m.onChange
	profile := m.current
	m.mu.Unlock()

	if fn != nil && prev != tier {
		fn(profile)
	}
}

// SetOnChange registers a callback that fires when the tier changes.
// Only one callback is supported; subsequent calls replace the previous.
func (m *Manager) SetOnChange(fn func(Profile)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = fn
}
