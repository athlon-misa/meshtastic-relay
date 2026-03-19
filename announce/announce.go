// Package announce implements the presence announcement system for the relay network.
//
// Each node periodically broadcasts announce packets so the network can
// maintain a mapping of user identity hashes to the node they are currently
// connected to. Announce packets are signed with the user's Ed25519 key
// and include replay prevention via timestamps and random nonces.
package announce

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/meshtastic-relay/relay/identity"
	"github.com/vmihailenco/msgpack/v5"
)

// NonceSize is the size of the random nonce in an announce packet.
const NonceSize = 16

// Packet is a presence announcement broadcast by a node on behalf of a
// connected user. It proves the user is reachable via the announcing node.
type Packet struct {
	// UserHash is the truncated SHA-256 hash of the user's public key.
	UserHash identity.Hash `msgpack:"user_hash"`
	// PublicKey is the user's full Ed25519 public key.
	PublicKey []byte `msgpack:"public_key"`
	// NodeID is the identity hash of the relay node where the user is connected.
	NodeID identity.Hash `msgpack:"node_id"`
	// Timestamp is the Unix timestamp (seconds) when the announcement was created.
	Timestamp int64 `msgpack:"timestamp"`
	// Nonce is a random value for replay prevention.
	Nonce [NonceSize]byte `msgpack:"nonce"`
	// Signature is the Ed25519 signature over the announcement fields.
	Signature []byte `msgpack:"signature"`
	// SignerPubKey is the public key of the signer. When a node signs on
	// behalf of a user, this is the node's public key (different from
	// PublicKey which is the user's). Empty means the user signed directly.
	SignerPubKey []byte `msgpack:"signer_pub_key,omitempty"`
}

// New creates and signs a new announce packet for the given user identity
// and relay node.
func New(user *identity.Identity, nodeID identity.Hash) (*Packet, error) {
	if !user.HasPrivateKey() {
		return nil, fmt.Errorf("announce: user identity must have private key to sign")
	}

	var nonce [NonceSize]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nil, fmt.Errorf("announce: generate nonce: %w", err)
	}

	pkt := &Packet{
		UserHash:  user.Hash(),
		PublicKey: user.PublicKey(),
		NodeID:    nodeID,
		Timestamp: time.Now().Unix(),
		Nonce:     nonce,
	}

	// Sign the packet fields (excluding the signature itself)
	payload := pkt.signingPayload()
	sig, err := user.Sign(payload)
	if err != nil {
		return nil, fmt.Errorf("announce: sign packet: %w", err)
	}
	pkt.Signature = sig

	return pkt, nil
}

// NewNodeSigned creates an announce packet where the relay node signs on
// behalf of a connected user. The node attests "this user is reachable
// through me." The node signs with its own key; the user's public key is
// included so receivers can verify the UserHash matches.
func NewNodeSigned(nodeSigner *identity.Identity, userHash identity.Hash, userPubKey []byte) (*Packet, error) {
	if !nodeSigner.HasPrivateKey() {
		return nil, fmt.Errorf("announce: node identity must have private key to sign")
	}

	var nonce [NonceSize]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nil, fmt.Errorf("announce: generate nonce: %w", err)
	}

	pkt := &Packet{
		UserHash:     userHash,
		PublicKey:    userPubKey,
		NodeID:       nodeSigner.Hash(),
		Timestamp:    time.Now().Unix(),
		Nonce:        nonce,
		SignerPubKey: nodeSigner.PublicKey(),
	}

	payload := pkt.signingPayload()
	sig, err := nodeSigner.Sign(payload)
	if err != nil {
		return nil, fmt.Errorf("announce: sign packet: %w", err)
	}
	pkt.Signature = sig

	return pkt, nil
}

// ValidateNodeSigned checks a node-signed announce packet:
//   - The UserHash matches SHA-256(PublicKey)[:16]
//   - The signature is valid against SignerPubKey (the node's key)
//   - The timestamp is within the allowed window
func (p *Packet) ValidateNodeSigned(maxAge time.Duration) error {
	// Verify user hash matches the user's public key
	peer := identity.FromPublicKey(p.PublicKey)
	if peer.Hash() != p.UserHash {
		return fmt.Errorf("announce: user hash does not match public key")
	}

	// Verify timestamp
	now := time.Now().Unix()
	age := now - p.Timestamp
	if age < 0 {
		age = -age
	}
	if time.Duration(age)*time.Second > maxAge {
		return fmt.Errorf("announce: timestamp out of range (age=%ds, max=%v)", age, maxAge)
	}

	// Determine which key signed the packet
	signerKey := p.PublicKey
	if len(p.SignerPubKey) > 0 {
		signerKey = p.SignerPubKey
	}

	signer := identity.FromPublicKey(signerKey)
	payload := p.signingPayload()
	if !signer.Verify(payload, p.Signature) {
		return fmt.Errorf("announce: invalid signature")
	}

	return nil
}

// signingPayload returns the byte representation of the fields that are signed.
func (p *Packet) signingPayload() []byte {
	// Deterministic serialization: hash + pubkey + nodeID + timestamp + nonce
	var buf []byte
	buf = append(buf, p.UserHash[:]...)
	buf = append(buf, p.PublicKey...)
	buf = append(buf, p.NodeID[:]...)
	ts := make([]byte, 8)
	binary.BigEndian.PutUint64(ts, uint64(p.Timestamp))
	buf = append(buf, ts...)
	buf = append(buf, p.Nonce[:]...)
	return buf
}

// Validate checks the announce packet's integrity:
//   - The UserHash matches SHA-256(PublicKey)[:16]
//   - The Ed25519 signature is valid
//   - The timestamp is within the allowed window
//
// maxAge specifies the maximum acceptable age (and future skew) for the timestamp.
func (p *Packet) Validate(maxAge time.Duration) error {
	// Verify user hash matches public key
	peer := identity.FromPublicKey(p.PublicKey)
	if peer.Hash() != p.UserHash {
		return fmt.Errorf("announce: user hash does not match public key")
	}

	// Verify timestamp is within acceptable window
	now := time.Now().Unix()
	age := now - p.Timestamp
	if age < 0 {
		age = -age
	}
	if time.Duration(age)*time.Second > maxAge {
		return fmt.Errorf("announce: timestamp out of range (age=%ds, max=%v)", age, maxAge)
	}

	// Verify signature
	payload := p.signingPayload()
	if !peer.Verify(payload, p.Signature) {
		return fmt.Errorf("announce: invalid signature")
	}

	return nil
}

// Marshal serializes the announce packet to msgpack bytes.
func (p *Packet) Marshal() ([]byte, error) {
	data, err := msgpack.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("announce: marshal: %w", err)
	}
	return data, nil
}

// UnmarshalPacket deserializes an announce packet from msgpack bytes.
func UnmarshalPacket(data []byte) (*Packet, error) {
	var pkt Packet
	if err := msgpack.Unmarshal(data, &pkt); err != nil {
		return nil, fmt.Errorf("announce: unmarshal: %w", err)
	}
	return &pkt, nil
}
