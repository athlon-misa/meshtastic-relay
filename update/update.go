// Package update implements over-the-air software updates for the relay daemon.
// Updates are distributed via MQTT admin channel and applied atomically with
// automatic rollback on failure.
package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Status represents the current state of the update manager.
type Status int

const (
	// StatusIdle means no update activity is in progress.
	StatusIdle Status = iota
	// StatusChecking means the manager is evaluating an update manifest.
	StatusChecking
	// StatusDownloading means the manager is downloading update binary data.
	StatusDownloading
	// StatusVerifying means the manager is verifying the downloaded binary.
	StatusVerifying
	// StatusApplying means the manager is writing the new binary to disk.
	StatusApplying
	// StatusRollingBack means the manager is restoring the previous binary.
	StatusRollingBack
)

// String returns the human-readable status name.
func (s Status) String() string {
	switch s {
	case StatusIdle:
		return "idle"
	case StatusChecking:
		return "checking"
	case StatusDownloading:
		return "downloading"
	case StatusVerifying:
		return "verifying"
	case StatusApplying:
		return "applying"
	case StatusRollingBack:
		return "rolling_back"
	default:
		return "unknown"
	}
}

// Manifest describes an available update distributed via MQTT admin channel.
type Manifest struct {
	Version      string `json:"version"`
	ReleasedAt   int64  `json:"released_at"`
	BinaryURL    string `json:"binary_url"`
	SHA256       string `json:"sha256"`
	Signature    string `json:"signature"`
	SignerPubKey string `json:"signer_pub_key"`
	MinVersion   string `json:"min_version"`
	ReleaseNotes string `json:"release_notes"`
}

// UpdateInfo holds the current update state for status reporting.
type UpdateInfo struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Status         Status `json:"status"`
	LastCheck      int64  `json:"last_check"`
	Error          string `json:"error"`
}

// Manager coordinates over-the-air updates: manifest validation, binary
// verification, atomic apply, and rollback.
type Manager struct {
	currentVersion string
	binaryPath     string
	backupPath     string
	signerPubKey   []byte
	status         atomic.Int32
	lastCheck      atomic.Int64
	latestManifest *Manifest
	lastError      string
	mu             sync.RWMutex
}

// NewManager creates a new update manager. The backupPath is set to
// binaryPath + ".backup".
func NewManager(currentVersion, binaryPath string, signerPubKey []byte) *Manager {
	m := &Manager{
		currentVersion: currentVersion,
		binaryPath:     binaryPath,
		backupPath:     binaryPath + ".backup",
		signerPubKey:   signerPubKey,
	}
	m.status.Store(int32(StatusIdle))
	return m
}

// Status returns the current update status.
func (m *Manager) Status() UpdateInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	info := UpdateInfo{
		CurrentVersion: m.currentVersion,
		Status:         Status(m.status.Load()),
		LastCheck:      m.lastCheck.Load(),
		Error:          m.lastError,
	}

	if m.latestManifest != nil {
		info.LatestVersion = m.latestManifest.Version
	}

	return info
}

// CheckUpdate parses a JSON manifest, validates it (version must be newer
// than current, MinVersion satisfied), and stores it. Returns error if invalid.
func (m *Manager) CheckUpdate(manifest []byte) error {
	m.status.Store(int32(StatusChecking))
	m.lastCheck.Store(time.Now().Unix())

	parsed, err := ParseManifest(manifest)
	if err != nil {
		m.setError(fmt.Sprintf("update: parse manifest: %v", err))
		m.status.Store(int32(StatusIdle))
		return fmt.Errorf("update: parse manifest: %w", err)
	}

	// Version must be newer than current.
	if CompareVersions(parsed.Version, m.currentVersion) <= 0 {
		m.status.Store(int32(StatusIdle))
		return fmt.Errorf("update: manifest version %s is not newer than current %s", parsed.Version, m.currentVersion)
	}

	// MinVersion: current version must be >= MinVersion.
	if parsed.MinVersion != "" && CompareVersions(m.currentVersion, parsed.MinVersion) < 0 {
		m.setError(fmt.Sprintf("update: current version %s does not meet minimum %s", m.currentVersion, parsed.MinVersion))
		m.status.Store(int32(StatusIdle))
		return fmt.Errorf("update: current version %s does not meet minimum %s", m.currentVersion, parsed.MinVersion)
	}

	// Verify cryptographic signature if signer key is configured.
	if len(m.signerPubKey) > 0 {
		if err := m.VerifyManifest(parsed); err != nil {
			m.setError(fmt.Sprintf("update: signature verification failed: %v", err))
			m.status.Store(int32(StatusIdle))
			return fmt.Errorf("update: signature verification failed: %w", err)
		}
	}

	m.mu.Lock()
	m.latestManifest = parsed
	m.lastError = ""
	m.mu.Unlock()

	m.status.Store(int32(StatusIdle))
	log.Printf("update: new version available: %s (current: %s)", parsed.Version, m.currentVersion)
	return nil
}

// VerifyManifest verifies the manifest's Signature against SHA256 using
// signerPubKey. Returns error if the signature is invalid or the signer
// key does not match.
func (m *Manager) VerifyManifest(mf *Manifest) error {
	if len(m.signerPubKey) == 0 {
		return fmt.Errorf("update: no signer public key configured")
	}

	// Verify signer key matches.
	manifestPubKey, err := hex.DecodeString(mf.SignerPubKey)
	if err != nil {
		return fmt.Errorf("update: decode signer pub key: %w", err)
	}

	if !bytesEqual(manifestPubKey, m.signerPubKey) {
		return fmt.Errorf("update: signer public key mismatch")
	}

	// Decode the signature.
	sig, err := hex.DecodeString(mf.Signature)
	if err != nil {
		return fmt.Errorf("update: decode signature: %w", err)
	}

	// The signature is over the hex-encoded SHA256 hash string.
	hashBytes := []byte(mf.SHA256)

	pubKey := ed25519.PublicKey(m.signerPubKey)
	if !ed25519.Verify(pubKey, hashBytes, sig) {
		return fmt.Errorf("update: ed25519 signature verification failed")
	}

	return nil
}

// ApplyUpdate applies an update by verifying the binary data, backing up
// the current binary, and writing the new binary to disk. On any error it
// performs an automatic rollback.
func (m *Manager) ApplyUpdate(binaryData []byte) error {
	m.mu.RLock()
	manifest := m.latestManifest
	m.mu.RUnlock()

	if manifest == nil {
		return fmt.Errorf("update: no manifest available")
	}

	// Step 1: Verify SHA256 hash.
	m.status.Store(int32(StatusVerifying))

	hash := sha256.Sum256(binaryData)
	computedHash := hex.EncodeToString(hash[:])
	if computedHash != manifest.SHA256 {
		m.status.Store(int32(StatusRollingBack))
		m.setError("update: binary hash mismatch")
		_ = m.Rollback()
		return fmt.Errorf("update: binary hash mismatch: expected %s, got %s", manifest.SHA256, computedHash)
	}

	// Step 2: Apply -- backup current binary and write new one.
	m.status.Store(int32(StatusApplying))

	if err := m.backupBinary(); err != nil {
		m.status.Store(int32(StatusRollingBack))
		m.setError(fmt.Sprintf("update: backup failed: %v", err))
		_ = m.Rollback()
		return fmt.Errorf("update: backup current binary: %w", err)
	}

	if err := m.writeNewBinary(binaryData); err != nil {
		m.status.Store(int32(StatusRollingBack))
		m.setError(fmt.Sprintf("update: write failed: %v", err))
		_ = m.Rollback()
		return fmt.Errorf("update: write new binary: %w", err)
	}

	m.status.Store(int32(StatusIdle))
	log.Printf("update: successfully applied version %s", manifest.Version)
	return nil
}

// Rollback copies the backup binary back to the original path.
func (m *Manager) Rollback() error {
	m.status.Store(int32(StatusRollingBack))

	if err := copyFile(m.backupPath, m.binaryPath); err != nil {
		m.setError(fmt.Sprintf("update: rollback failed: %v", err))
		m.status.Store(int32(StatusIdle))
		return fmt.Errorf("update: rollback: %w", err)
	}

	m.status.Store(int32(StatusIdle))
	log.Printf("update: rollback completed, restored previous binary")
	return nil
}

// HasUpdate returns true if a manifest is stored and its version is newer
// than the current version.
func (m *Manager) HasUpdate() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.latestManifest == nil {
		return false
	}
	return CompareVersions(m.latestManifest.Version, m.currentVersion) > 0
}

// setError stores the last error message under the mutex.
func (m *Manager) setError(msg string) {
	m.mu.Lock()
	m.lastError = msg
	m.mu.Unlock()
}

// backupBinary copies the current binary to the backup path.
func (m *Manager) backupBinary() error {
	return copyFile(m.binaryPath, m.backupPath)
}

// writeNewBinary writes the new binary data to the binary path, preserving
// the file permissions of the original binary.
func (m *Manager) writeNewBinary(data []byte) error {
	// Get permissions from the current binary.
	info, err := os.Stat(m.binaryPath)
	if err != nil {
		return fmt.Errorf("stat current binary: %w", err)
	}

	if err := os.WriteFile(m.binaryPath, data, info.Mode()); err != nil {
		return fmt.Errorf("write binary: %w", err)
	}

	return nil
}

// copyFile copies src to dst, preserving the source file permissions.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}

	return nil
}

// bytesEqual compares two byte slices for equality.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ParseManifest unmarshals JSON data into a Manifest and validates that
// required fields are present.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}

	if m.Version == "" {
		return nil, fmt.Errorf("manifest missing required field: version")
	}
	if m.BinaryURL == "" {
		return nil, fmt.Errorf("manifest missing required field: binary_url")
	}
	if m.SHA256 == "" {
		return nil, fmt.Errorf("manifest missing required field: sha256")
	}

	return &m, nil
}

// CompareVersions performs a simple semver comparison on two version strings.
// It splits on "." and compares each numeric component. Returns -1 if a < b,
// 0 if a == b, or 1 if a > b. Non-numeric components are compared as zero.
func CompareVersions(a, b string) int {
	// Strip leading "v" prefix if present.
	a = strings.TrimPrefix(a, "v")
	b = strings.TrimPrefix(b, "v")

	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	// Pad the shorter slice so both have equal length.
	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}

	for i := 0; i < maxLen; i++ {
		var aNum, bNum int
		if i < len(aParts) {
			aNum, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bNum, _ = strconv.Atoi(bParts[i])
		}
		if aNum < bNum {
			return -1
		}
		if aNum > bNum {
			return 1
		}
	}

	return 0
}
