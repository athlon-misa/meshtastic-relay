package identity

import (
	"crypto/ed25519"
	"crypto/sha512"
	"fmt"

	"filippo.io/edwards25519"
)

// ed25519PubToX25519 converts an Ed25519 public key to an X25519 public key
// using the birational map between the two curves (Edwards25519 -> Montgomery).
func ed25519PubToX25519(edPub ed25519.PublicKey) ([32]byte, error) {
	// Parse the Ed25519 public key as an edwards25519 point
	p, err := new(edwards25519.Point).SetBytes(edPub)
	if err != nil {
		return [32]byte{}, fmt.Errorf("identity: invalid ed25519 public key: %w", err)
	}

	// Convert Edwards point to Montgomery u-coordinate (X25519 public key)
	xPub := p.BytesMontgomery()
	var result [32]byte
	copy(result[:], xPub)
	return result, nil
}

// ed25519PrivToX25519 converts an Ed25519 private key to an X25519 private key.
// The X25519 private key is derived from the Ed25519 seed via SHA-512, with
// the standard clamping applied (matching how Ed25519 uses its scalar internally).
func ed25519PrivToX25519(edPriv ed25519.PrivateKey) [32]byte {
	// The Ed25519 private key scalar is SHA-512(seed)[:32] with clamping.
	// We replicate this to get the X25519 private key.
	seed := edPriv.Seed()
	h := sha512.Sum512(seed)

	// Clamp the scalar per RFC 7748 / X25519 requirements
	h[0] &= 248
	h[31] &= 127
	h[31] |= 64

	var xPriv [32]byte
	copy(xPriv[:], h[:32])
	return xPriv
}
