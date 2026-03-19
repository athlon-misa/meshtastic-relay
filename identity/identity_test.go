package identity

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerate(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if !id.HasPrivateKey() {
		t.Error("generated identity should have private key")
	}

	if id.Hash().IsZero() {
		t.Error("generated identity should have non-zero hash")
	}

	if len(id.PublicKey()) != ed25519.PublicKeySize {
		t.Errorf("public key size = %d, want %d", len(id.PublicKey()), ed25519.PublicKeySize)
	}
}

func TestGenerateUniqueness(t *testing.T) {
	id1, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	id2, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if id1.Hash() == id2.Hash() {
		t.Error("two generated identities should have different hashes")
	}

	if bytes.Equal(id1.PublicKey(), id2.PublicKey()) {
		t.Error("two generated identities should have different public keys")
	}
}

func TestHashConsistency(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// Re-derive hash from public key and verify it matches
	recomputed := computeHash(id.PublicKey())
	if id.Hash() != recomputed {
		t.Errorf("hash mismatch: %s != %s", id.Hash(), recomputed)
	}
}

func TestHashString(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	s := id.Hash().String()
	if len(s) != HashSize*2 {
		t.Errorf("hash string length = %d, want %d", len(s), HashSize*2)
	}

	// Parse it back
	parsed, err := HashFromHex(s)
	if err != nil {
		t.Fatalf("HashFromHex(%q) error: %v", s, err)
	}
	if parsed != id.Hash() {
		t.Errorf("round-trip hash mismatch: %s != %s", parsed, id.Hash())
	}
}

func TestHashFromBytes(t *testing.T) {
	// Valid
	b := make([]byte, HashSize)
	b[0] = 0x42
	h, err := HashFromBytes(b)
	if err != nil {
		t.Fatalf("HashFromBytes() error: %v", err)
	}
	if h[0] != 0x42 {
		t.Errorf("first byte = %x, want 0x42", h[0])
	}

	// Invalid size
	_, err = HashFromBytes([]byte{1, 2, 3})
	if err == nil {
		t.Error("HashFromBytes(short) should return error")
	}
}

func TestSignAndVerify(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	message := []byte("hello, meshtastic relay")
	sig, err := id.Sign(message)
	if err != nil {
		t.Fatalf("Sign() error: %v", err)
	}

	if !id.Verify(message, sig) {
		t.Error("Verify() returned false for valid signature")
	}

	// Tampered message should fail
	tampered := []byte("hello, tampered message")
	if id.Verify(tampered, sig) {
		t.Error("Verify() returned true for tampered message")
	}

	// Tampered signature should fail
	badSig := make([]byte, len(sig))
	copy(badSig, sig)
	badSig[0] ^= 0xFF
	if id.Verify(message, badSig) {
		t.Error("Verify() returned true for tampered signature")
	}
}

func TestSignWithoutPrivateKey(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// Create a public-key-only identity
	pubOnly := FromPublicKey(id.PublicKey())
	if pubOnly.HasPrivateKey() {
		t.Error("public-key-only identity should not have private key")
	}

	_, err = pubOnly.Sign([]byte("test"))
	if err == nil {
		t.Error("Sign() on public-key-only identity should return error")
	}
}

func TestVerifyWithPeerPublicKey(t *testing.T) {
	signer, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	message := []byte("authenticated message")
	sig, err := signer.Sign(message)
	if err != nil {
		t.Fatalf("Sign() error: %v", err)
	}

	// Verify using only the public key (simulating a remote peer)
	peer := FromPublicKey(signer.PublicKey())
	if !peer.Verify(message, sig) {
		t.Error("peer Verify() returned false for valid signature")
	}
}

func TestEncryptDecrypt(t *testing.T) {
	alice, err := Generate()
	if err != nil {
		t.Fatalf("Generate() alice error: %v", err)
	}
	bob, err := Generate()
	if err != nil {
		t.Fatalf("Generate() bob error: %v", err)
	}

	aliceX25519Pub, err := alice.X25519PublicKey()
	if err != nil {
		t.Fatalf("alice X25519PublicKey() error: %v", err)
	}
	bobX25519Pub, err := bob.X25519PublicKey()
	if err != nil {
		t.Fatalf("bob X25519PublicKey() error: %v", err)
	}

	plaintext := []byte("secret message for bob")

	// Alice encrypts to Bob
	ciphertext, err := alice.Encrypt(plaintext, bobX25519Pub)
	if err != nil {
		t.Fatalf("Encrypt() error: %v", err)
	}

	// Ciphertext should be different from plaintext
	if bytes.Equal(ciphertext, plaintext) {
		t.Error("ciphertext should differ from plaintext")
	}

	// Bob decrypts from Alice
	decrypted, err := bob.Decrypt(ciphertext, aliceX25519Pub)
	if err != nil {
		t.Fatalf("Decrypt() error: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptDecryptEmpty(t *testing.T) {
	alice, _ := Generate()
	bob, _ := Generate()

	bobPub, _ := bob.X25519PublicKey()
	alicePub, _ := alice.X25519PublicKey()

	ciphertext, err := alice.Encrypt([]byte{}, bobPub)
	if err != nil {
		t.Fatalf("Encrypt empty: %v", err)
	}

	decrypted, err := bob.Decrypt(ciphertext, alicePub)
	if err != nil {
		t.Fatalf("Decrypt empty: %v", err)
	}

	if len(decrypted) != 0 {
		t.Errorf("expected empty plaintext, got %d bytes", len(decrypted))
	}
}

func TestDecryptWrongKey(t *testing.T) {
	alice, _ := Generate()
	bob, _ := Generate()
	eve, _ := Generate()

	bobPub, _ := bob.X25519PublicKey()
	evePub, _ := eve.X25519PublicKey()

	ciphertext, err := alice.Encrypt([]byte("for bob only"), bobPub)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Eve tries to decrypt with her key but using Alice's public key
	alicePub, _ := alice.X25519PublicKey()
	_, err = eve.Decrypt(ciphertext, alicePub)
	if err == nil {
		t.Error("Eve should not be able to decrypt message meant for Bob")
	}

	// Bob should also fail if given wrong sender public key
	_, err = bob.Decrypt(ciphertext, evePub)
	if err == nil {
		t.Error("Decrypt with wrong sender key should fail")
	}
}

func TestDecryptTooShort(t *testing.T) {
	bob, _ := Generate()
	alicePub := [32]byte{}
	_, err := bob.Decrypt([]byte("short"), alicePub)
	if err == nil {
		t.Error("Decrypt of too-short ciphertext should return error")
	}
}

func TestSharedSecret(t *testing.T) {
	alice, _ := Generate()
	bob, _ := Generate()

	bobPub, _ := bob.X25519PublicKey()
	alicePub, _ := alice.X25519PublicKey()

	secretAB, err := alice.SharedSecret(bobPub)
	if err != nil {
		t.Fatalf("alice SharedSecret: %v", err)
	}

	secretBA, err := bob.SharedSecret(alicePub)
	if err != nil {
		t.Fatalf("bob SharedSecret: %v", err)
	}

	if secretAB != secretBA {
		t.Error("shared secrets should be identical")
	}

	// Shared secret should not be zero
	zero := [32]byte{}
	if secretAB == zero {
		t.Error("shared secret should not be zero")
	}
}

func TestDeriveSessionKey(t *testing.T) {
	alice, _ := Generate()
	bob, _ := Generate()

	bobPub, _ := bob.X25519PublicKey()
	alicePub, _ := alice.X25519PublicKey()

	salt := []byte("session-salt")
	info := []byte("meshtastic-relay-session-v1")
	keyLen := 32

	keyA, err := alice.DeriveSessionKey(bobPub, salt, info, keyLen)
	if err != nil {
		t.Fatalf("alice DeriveSessionKey: %v", err)
	}

	keyB, err := bob.DeriveSessionKey(alicePub, salt, info, keyLen)
	if err != nil {
		t.Fatalf("bob DeriveSessionKey: %v", err)
	}

	if !bytes.Equal(keyA, keyB) {
		t.Error("derived session keys should be identical")
	}

	if len(keyA) != keyLen {
		t.Errorf("key length = %d, want %d", len(keyA), keyLen)
	}

	// Different info should produce different key
	keyDiff, _ := alice.DeriveSessionKey(bobPub, salt, []byte("different-info"), keyLen)
	if bytes.Equal(keyA, keyDiff) {
		t.Error("different info should produce different key")
	}

	// Different salt should produce different key
	keyDiffSalt, _ := alice.DeriveSessionKey(bobPub, []byte("other-salt"), info, keyLen)
	if bytes.Equal(keyA, keyDiffSalt) {
		t.Error("different salt should produce different key")
	}
}

func TestHKDFDeriveKey(t *testing.T) {
	ikm := []byte("input keying material")
	salt := []byte("salt value")
	info := []byte("context info")

	key1, err := DeriveKey(ikm, salt, info, 32)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if len(key1) != 32 {
		t.Errorf("key length = %d, want 32", len(key1))
	}

	// Same inputs should produce same output (deterministic)
	key2, _ := DeriveKey(ikm, salt, info, 32)
	if !bytes.Equal(key1, key2) {
		t.Error("DeriveKey should be deterministic")
	}

	// Different lengths
	key16, _ := DeriveKey(ikm, salt, info, 16)
	if !bytes.Equal(key1[:16], key16) {
		t.Error("16-byte key should be prefix of 32-byte key")
	}
}

func TestMarshalUnmarshal(t *testing.T) {
	// Full identity (with private key)
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	data := id.Marshal()
	if len(data) != ed25519.PrivateKeySize {
		t.Errorf("marshaled size = %d, want %d", len(data), ed25519.PrivateKeySize)
	}

	restored, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	if restored.Hash() != id.Hash() {
		t.Error("restored identity hash mismatch")
	}
	if !restored.HasPrivateKey() {
		t.Error("restored identity should have private key")
	}

	// Verify signing still works after round-trip
	msg := []byte("test after unmarshal")
	sig, _ := restored.Sign(msg)
	if !id.Verify(msg, sig) {
		t.Error("signature from restored identity should verify")
	}
}

func TestMarshalUnmarshalPublicOnly(t *testing.T) {
	id, _ := Generate()
	pubOnly := FromPublicKey(id.PublicKey())

	data := pubOnly.Marshal()
	if len(data) != ed25519.PublicKeySize {
		t.Errorf("marshaled public size = %d, want %d", len(data), ed25519.PublicKeySize)
	}

	restored, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	if restored.Hash() != id.Hash() {
		t.Error("restored public identity hash mismatch")
	}
	if restored.HasPrivateKey() {
		t.Error("restored public identity should not have private key")
	}
}

func TestUnmarshalInvalidSize(t *testing.T) {
	_, err := Unmarshal([]byte{1, 2, 3})
	if err == nil {
		t.Error("Unmarshal(short) should return error")
	}
}

func TestSaveAndLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-identity.key")

	id, _ := Generate()
	if err := id.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}

	// Check file permissions
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("file permissions = %o, want 0600", info.Mode().Perm())
	}

	loaded, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}

	if loaded.Hash() != id.Hash() {
		t.Error("loaded identity hash mismatch")
	}
}

func TestLoadOrGenerate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "identity.key")

	// First call: generates new identity
	id1, err := LoadOrGenerate(path)
	if err != nil {
		t.Fatalf("LoadOrGenerate (create): %v", err)
	}

	// Second call: loads existing identity
	id2, err := LoadOrGenerate(path)
	if err != nil {
		t.Fatalf("LoadOrGenerate (load): %v", err)
	}

	if id1.Hash() != id2.Hash() {
		t.Error("LoadOrGenerate should return same identity on second call")
	}
}

func TestChallengeResponse(t *testing.T) {
	id, _ := Generate()

	challenge, err := NewChallenge()
	if err != nil {
		t.Fatalf("NewChallenge: %v", err)
	}

	resp, err := challenge.Respond(id)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}

	err = challenge.Verify(resp, 5*time.Second)
	if err != nil {
		t.Errorf("Verify valid response: %v", err)
	}
}

func TestChallengeResponseWrongKey(t *testing.T) {
	alice, _ := Generate()
	eve, _ := Generate()

	challenge, _ := NewChallenge()
	resp, _ := challenge.Respond(alice)

	// Tamper: replace public key with Eve's
	resp.PublicKey = eve.PublicKey()
	err := challenge.Verify(resp, 5*time.Second)
	if err == nil {
		t.Error("Verify with wrong public key should fail")
	}
}

func TestChallengeExpired(t *testing.T) {
	id, _ := Generate()

	challenge, _ := NewChallenge()
	// Backdate the challenge
	challenge.CreatedAt = time.Now().Add(-10 * time.Second)

	resp, _ := challenge.Respond(id)
	err := challenge.Verify(resp, 1*time.Second)
	if err == nil {
		t.Error("Verify of expired challenge should fail")
	}
}

func TestChallengeNonceMismatch(t *testing.T) {
	id, _ := Generate()

	challenge1, _ := NewChallenge()
	challenge2, _ := NewChallenge()

	// Respond to challenge1 but verify against challenge2
	resp, _ := challenge1.Respond(id)
	err := challenge2.Verify(resp, 5*time.Second)
	if err == nil {
		t.Error("Verify with mismatched nonce should fail")
	}
}

func TestX25519KeyDerivation(t *testing.T) {
	id, _ := Generate()

	xPub, err := id.X25519PublicKey()
	if err != nil {
		t.Fatalf("X25519PublicKey: %v", err)
	}

	xPriv, err := id.X25519PrivateKey()
	if err != nil {
		t.Fatalf("X25519PrivateKey: %v", err)
	}

	// Keys should not be zero
	zero := [32]byte{}
	if xPub == zero {
		t.Error("X25519 public key should not be zero")
	}
	if xPriv == zero {
		t.Error("X25519 private key should not be zero")
	}
}

func TestX25519WithoutPrivateKey(t *testing.T) {
	id, _ := Generate()
	pubOnly := FromPublicKey(id.PublicKey())

	// Public key derivation should work
	_, err := pubOnly.X25519PublicKey()
	if err != nil {
		t.Errorf("X25519PublicKey on public-only identity: %v", err)
	}

	// Private key derivation should fail
	_, err = pubOnly.X25519PrivateKey()
	if err == nil {
		t.Error("X25519PrivateKey on public-only identity should fail")
	}
}
