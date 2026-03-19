// Package identity implements the cryptographic identity system for the relay network.
//
// Every user and node has an Ed25519 keypair. Their identity hash is
// SHA-256(public_key) truncated to 16 bytes. Key exchange uses X25519 ECDH
// with HKDF-SHA256 for session key derivation, and encryption uses
// XSalsa20-Poly1305 (NaCl crypto_box).
package identity

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/nacl/box"
)

// HashSize is the length of a truncated identity hash in bytes.
const HashSize = 16

// Hash is a truncated SHA-256 hash of a public key, used as a compact
// network-wide identifier for a user or node.
type Hash [HashSize]byte

// String returns the hex-encoded identity hash.
func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

// IsZero reports whether the hash is all zeros (uninitialized).
func (h Hash) IsZero() bool {
	for _, b := range h {
		if b != 0 {
			return false
		}
	}
	return true
}

// HashFromBytes creates a Hash from a byte slice.
// Returns an error if the slice is not exactly HashSize bytes.
func HashFromBytes(b []byte) (Hash, error) {
	var h Hash
	if len(b) != HashSize {
		return h, fmt.Errorf("identity: hash must be %d bytes, got %d", HashSize, len(b))
	}
	copy(h[:], b)
	return h, nil
}

// HashFromHex parses a hex-encoded identity hash string.
func HashFromHex(s string) (Hash, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return Hash{}, fmt.Errorf("identity: invalid hex hash: %w", err)
	}
	return HashFromBytes(b)
}

// Identity represents a cryptographic identity with an Ed25519 keypair.
// The private key is optional -- a remote peer's identity will only have
// the public key and derived hash.
type Identity struct {
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
	hash       Hash
}

// Generate creates a new random Identity with a fresh Ed25519 keypair.
func Generate() (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("identity: generate keypair: %w", err)
	}
	return newIdentity(pub, priv), nil
}

// FromPublicKey creates an Identity from an existing Ed25519 public key.
// The resulting Identity can verify signatures but cannot sign.
func FromPublicKey(pub ed25519.PublicKey) *Identity {
	return newIdentity(pub, nil)
}

// newIdentity constructs an Identity and computes the hash.
func newIdentity(pub ed25519.PublicKey, priv ed25519.PrivateKey) *Identity {
	id := &Identity{
		publicKey:  pub,
		privateKey: priv,
	}
	id.hash = computeHash(pub)
	return id
}

// computeHash computes SHA-256(publicKey) truncated to HashSize bytes.
func computeHash(pub ed25519.PublicKey) Hash {
	full := sha256.Sum256(pub)
	var h Hash
	copy(h[:], full[:HashSize])
	return h
}

// Hash returns the identity hash (truncated SHA-256 of the public key).
func (id *Identity) Hash() Hash {
	return id.hash
}

// PublicKey returns the Ed25519 public key.
func (id *Identity) PublicKey() ed25519.PublicKey {
	return id.publicKey
}

// HasPrivateKey reports whether this Identity holds a private key
// (i.e., can sign messages and perform key exchange).
func (id *Identity) HasPrivateKey() bool {
	return id.privateKey != nil
}

// Sign signs the given message with the Ed25519 private key.
// Returns an error if the Identity has no private key.
func (id *Identity) Sign(message []byte) ([]byte, error) {
	if id.privateKey == nil {
		return nil, fmt.Errorf("identity: cannot sign without private key")
	}
	sig, err := id.privateKey.Sign(rand.Reader, message, crypto.Hash(0))
	if err != nil {
		return nil, fmt.Errorf("identity: sign: %w", err)
	}
	return sig, nil
}

// Verify checks an Ed25519 signature against this Identity's public key.
func (id *Identity) Verify(message, signature []byte) bool {
	return ed25519.Verify(id.publicKey, message, signature)
}

// X25519PublicKey derives the X25519 public key from the Ed25519 public key
// using the birational map. This is used for ECDH key exchange.
func (id *Identity) X25519PublicKey() ([32]byte, error) {
	return ed25519PubToX25519(id.publicKey)
}

// X25519PrivateKey derives the X25519 private key from the Ed25519 private key
// seed. This is used for ECDH key exchange.
// Returns an error if the Identity has no private key.
func (id *Identity) X25519PrivateKey() ([32]byte, error) {
	if id.privateKey == nil {
		return [32]byte{}, fmt.Errorf("identity: cannot derive X25519 key without private key")
	}
	return ed25519PrivToX25519(id.privateKey), nil
}

// SharedSecret performs X25519 ECDH key exchange with a peer's public key,
// returning a raw shared secret. Use DeriveSessionKey for a proper key.
func (id *Identity) SharedSecret(peerPub [32]byte) ([32]byte, error) {
	priv, err := id.X25519PrivateKey()
	if err != nil {
		return [32]byte{}, err
	}
	shared, err := curve25519.X25519(priv[:], peerPub[:])
	if err != nil {
		return [32]byte{}, fmt.Errorf("identity: x25519 ecdh: %w", err)
	}
	var result [32]byte
	copy(result[:], shared)
	return result, nil
}

// DeriveSessionKey performs ECDH with the peer and then applies HKDF-SHA256
// to derive a session key of the specified length.
// The info parameter provides context separation for different key usages.
func (id *Identity) DeriveSessionKey(peerPub [32]byte, salt, info []byte, keyLen int) ([]byte, error) {
	shared, err := id.SharedSecret(peerPub)
	if err != nil {
		return nil, err
	}
	return DeriveKey(shared[:], salt, info, keyLen)
}

// DeriveKey applies HKDF-SHA256 to derive a key from input keying material.
func DeriveKey(ikm, salt, info []byte, keyLen int) ([]byte, error) {
	hkdfReader := hkdf.New(sha256.New, ikm, salt, info)
	key := make([]byte, keyLen)
	if _, err := io.ReadFull(hkdfReader, key); err != nil {
		return nil, fmt.Errorf("identity: hkdf derive: %w", err)
	}
	return key, nil
}

// Encrypt encrypts a plaintext message using NaCl crypto_box (XSalsa20-Poly1305)
// with the receiver's X25519 public key. A random nonce is generated and
// prepended to the ciphertext.
func (id *Identity) Encrypt(plaintext []byte, recipientPub [32]byte) ([]byte, error) {
	senderPriv, err := id.X25519PrivateKey()
	if err != nil {
		return nil, err
	}

	var nonce [24]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nil, fmt.Errorf("identity: generate nonce: %w", err)
	}

	// box.Seal appends the encrypted message to the nonce prefix
	sealed := box.Seal(nonce[:], plaintext, &nonce, &recipientPub, &senderPriv)
	return sealed, nil
}

// Decrypt decrypts a message encrypted with NaCl crypto_box.
// Expects the nonce prepended to the ciphertext (as produced by Encrypt).
func (id *Identity) Decrypt(encrypted []byte, senderPub [32]byte) ([]byte, error) {
	receiverPriv, err := id.X25519PrivateKey()
	if err != nil {
		return nil, err
	}

	if len(encrypted) < 24+box.Overhead {
		return nil, fmt.Errorf("identity: ciphertext too short")
	}

	var nonce [24]byte
	copy(nonce[:], encrypted[:24])
	ciphertext := encrypted[24:]

	plaintext, ok := box.Open(nil, ciphertext, &nonce, &senderPub, &receiverPriv)
	if !ok {
		return nil, fmt.Errorf("identity: decryption failed (authentication error)")
	}
	return plaintext, nil
}

// Marshal serializes the Identity to bytes.
// If the Identity has a private key, the full 64-byte Ed25519 private key
// is serialized (which contains the public key). Otherwise, only the
// 32-byte public key is serialized.
func (id *Identity) Marshal() []byte {
	if id.privateKey != nil {
		return []byte(id.privateKey)
	}
	return []byte(id.publicKey)
}

// Unmarshal deserializes an Identity from bytes.
// A 64-byte input is treated as an Ed25519 private key (contains public key).
// A 32-byte input is treated as an Ed25519 public key only.
func Unmarshal(data []byte) (*Identity, error) {
	switch len(data) {
	case ed25519.PrivateKeySize:
		priv := ed25519.PrivateKey(data)
		pub := priv.Public().(ed25519.PublicKey)
		return newIdentity(pub, priv), nil
	case ed25519.PublicKeySize:
		pub := ed25519.PublicKey(data)
		return newIdentity(pub, nil), nil
	default:
		return nil, fmt.Errorf("identity: invalid key size %d (expected %d or %d)",
			len(data), ed25519.PublicKeySize, ed25519.PrivateKeySize)
	}
}

// SaveToFile writes the Identity's private key to a file with restricted permissions.
// Returns an error if the Identity has no private key.
func (id *Identity) SaveToFile(path string) error {
	if id.privateKey == nil {
		return fmt.Errorf("identity: cannot save identity without private key")
	}
	return os.WriteFile(path, id.Marshal(), 0600)
}

// LoadFromFile reads an Identity from a key file.
func LoadFromFile(path string) (*Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("identity: read file %s: %w", path, err)
	}
	return Unmarshal(data)
}

// LoadOrGenerate loads an Identity from a file, or generates a new one
// and saves it if the file does not exist.
func LoadOrGenerate(path string) (*Identity, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		id, err := Generate()
		if err != nil {
			return nil, err
		}
		if err := id.SaveToFile(path); err != nil {
			return nil, err
		}
		return id, nil
	}
	return LoadFromFile(path)
}
