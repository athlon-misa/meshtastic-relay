package identity

import (
	"crypto/rand"
	"fmt"
	"io"
	"time"
)

// ChallengeSize is the size of the random challenge nonce in bytes.
const ChallengeSize = 32

// Challenge represents a challenge-response authentication exchange.
// The verifier generates a random nonce; the prover signs it to prove
// possession of the private key corresponding to their claimed identity.
type Challenge struct {
	Nonce     [ChallengeSize]byte
	CreatedAt time.Time
}

// ChallengeResponse holds the signed response to a Challenge.
type ChallengeResponse struct {
	Nonce     [ChallengeSize]byte
	Signature []byte
	PublicKey []byte
}

// NewChallenge generates a new random challenge nonce.
func NewChallenge() (*Challenge, error) {
	var nonce [ChallengeSize]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nil, fmt.Errorf("identity: generate challenge nonce: %w", err)
	}
	return &Challenge{
		Nonce:     nonce,
		CreatedAt: time.Now(),
	}, nil
}

// Respond signs the challenge nonce with the given Identity, producing
// a ChallengeResponse that proves possession of the private key.
func (c *Challenge) Respond(id *Identity) (*ChallengeResponse, error) {
	sig, err := id.Sign(c.Nonce[:])
	if err != nil {
		return nil, fmt.Errorf("identity: sign challenge: %w", err)
	}
	return &ChallengeResponse{
		Nonce:     c.Nonce,
		Signature: sig,
		PublicKey: id.PublicKey(),
	}, nil
}

// Verify checks that a ChallengeResponse is valid: the signature is correct
// for the nonce, and the response was generated for this specific challenge.
// It also checks that the challenge has not expired (maxAge).
func (c *Challenge) Verify(resp *ChallengeResponse, maxAge time.Duration) error {
	// Check that the response matches this challenge
	if resp.Nonce != c.Nonce {
		return fmt.Errorf("identity: challenge nonce mismatch")
	}

	// Check expiry
	if time.Since(c.CreatedAt) > maxAge {
		return fmt.Errorf("identity: challenge expired (age %v > max %v)", time.Since(c.CreatedAt), maxAge)
	}

	// Verify the Ed25519 signature over the nonce
	peer := FromPublicKey(resp.PublicKey)
	if !peer.Verify(resp.Nonce[:], resp.Signature) {
		return fmt.Errorf("identity: challenge signature verification failed")
	}

	return nil
}
