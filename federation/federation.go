// Package federation implements optional OpenFederation identity linking,
// allowing relay users to bind their DID (decentralized identifier) and
// handle to their relay identity hash via ATProto PDS token verification.
package federation

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Profile represents a linked OpenFederation identity.
type Profile struct {
	RelayHash string    `json:"relay_hash"`
	DID       string    `json:"did"`
	Handle    string    `json:"handle"`
	UpdatedAt time.Time `json:"updated_at"`
}

// sessionResponse is the JSON payload returned by the PDS getSession endpoint.
type sessionResponse struct {
	DID    string `json:"did"`
	Handle string `json:"handle"`
	Email  string `json:"email"`
	Active bool   `json:"active"`
}

// Store manages DID <-> relay hash mappings with thread-safe in-memory storage.
type Store struct {
	mu     sync.RWMutex
	byHash map[string]*Profile // relay_hash -> profile
	byDID  map[string]*Profile // did -> profile
	pdsURL string
	client *http.Client
}

// NewStore creates a new federation Store that verifies tokens against the
// given PDS URL.
func NewStore(pdsURL string) *Store {
	return &Store{
		byHash: make(map[string]*Profile),
		byDID:  make(map[string]*Profile),
		pdsURL: pdsURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// VerifyAndBind verifies an access token against the PDS, checks that the
// returned DID matches the claimed DID, and stores the relay_hash <-> DID
// mapping. Returns the resulting Profile or an error.
func (s *Store) VerifyAndBind(relayHash, accessToken, claimedDID string) (*Profile, error) {
	// Build the PDS getSession request.
	url := s.pdsURL + "/xrpc/com.atproto.server.getSession"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("federation: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("federation: PDS request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("federation: PDS returned status %d: %s", resp.StatusCode, string(body))
	}

	var session sessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("federation: decode PDS response: %w", err)
	}

	// Verify the returned DID matches the claimed DID.
	if session.DID != claimedDID {
		return nil, fmt.Errorf("federation: DID mismatch: token belongs to %s, claimed %s", session.DID, claimedDID)
	}

	// Verify the account is active.
	if !session.Active {
		return nil, fmt.Errorf("federation: account %s is not active", session.DID)
	}

	profile := &Profile{
		RelayHash: relayHash,
		DID:       session.DID,
		Handle:    session.Handle,
		UpdatedAt: time.Now(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove any previous mapping for this relay hash.
	if old, ok := s.byHash[relayHash]; ok {
		delete(s.byDID, old.DID)
	}
	// Remove any previous mapping for this DID.
	if old, ok := s.byDID[session.DID]; ok {
		delete(s.byHash, old.RelayHash)
	}

	s.byHash[relayHash] = profile
	s.byDID[session.DID] = profile

	return profile, nil
}

// LookupByHash returns the Profile for a relay hash, or nil if not linked.
func (s *Store) LookupByHash(relayHash string) *Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byHash[relayHash]
}

// LookupByDID returns the Profile for a DID, or nil if not linked.
func (s *Store) LookupByDID(did string) *Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byDID[did]
}

// Remove deletes the mapping for a relay hash (both directions).
func (s *Store) Remove(relayHash string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if profile, ok := s.byHash[relayHash]; ok {
		delete(s.byDID, profile.DID)
		delete(s.byHash, relayHash)
	}
}
