// Package message defines the wire format and encryption for relay messages.
//
// The wire format is inspired by LXMF:
//
//	dest_hash(16) + source_hash(16) + nonce(24) + signature(64) + msgpack(payload)
//
// End-to-end encryption uses X25519 ECDH key exchange with XSalsa20-Poly1305
// (NaCl crypto_box). Each message carries a sequence number for ordering and
// replay prevention.
package message

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/meshtastic-relay/relay/identity"
	"github.com/vmihailenco/msgpack/v5"
)

// Type identifies the kind of message being sent.
type Type uint8

const (
	// TypeText is a user-to-user text message.
	TypeText Type = 1
	// TypePresence is a presence notification (online/offline/typing).
	TypePresence Type = 2
	// TypeRouting is a routing control message between nodes.
	TypeRouting Type = 3
	// TypeAdmin is an administrative command (e.g., node configuration).
	TypeAdmin Type = 4
)

// String returns the human-readable name of the message type.
func (t Type) String() string {
	switch t {
	case TypeText:
		return "text"
	case TypePresence:
		return "presence"
	case TypeRouting:
		return "routing"
	case TypeAdmin:
		return "admin"
	default:
		return fmt.Sprintf("unknown(%d)", t)
	}
}

// NonceSize is the size of the random nonce in the wire format.
const NonceSize = 24

// SignatureSize is the size of an Ed25519 signature.
const SignatureSize = 64

// HeaderSize is the total size of the fixed-length message header:
// dest_hash(16) + source_hash(16) + nonce(24) + signature(64) = 120 bytes
const HeaderSize = identity.HashSize + identity.HashSize + NonceSize + SignatureSize

// Payload is the cleartext content of a message, serialized with msgpack.
type Payload struct {
	// Type identifies the message kind.
	Type Type `msgpack:"type"`
	// SeqNum is a monotonically increasing sequence number from the sender.
	SeqNum uint64 `msgpack:"seq"`
	// Timestamp is the Unix timestamp (seconds) when the message was created.
	Timestamp int64 `msgpack:"ts"`
	// Content is the message body. For TypeText this is the text string.
	Content string `msgpack:"content,omitempty"`
	// Data is optional binary data attached to the message.
	Data []byte `msgpack:"data,omitempty"`
}

// Message is a complete relay message with header fields and payload.
type Message struct {
	// DestHash is the recipient's identity hash.
	DestHash identity.Hash
	// SourceHash is the sender's identity hash.
	SourceHash identity.Hash
	// Nonce is a random nonce used for uniqueness and encryption IV derivation.
	Nonce [NonceSize]byte
	// Signature is the Ed25519 signature over (dest_hash + source_hash + nonce + encrypted_payload).
	Signature [SignatureSize]byte
	// EncryptedPayload is the msgpack-encoded Payload encrypted with NaCl crypto_box.
	EncryptedPayload []byte
	// Payload is the decrypted payload (populated after Decrypt).
	Payload *Payload
}

// NewTextMessage creates a new text message from sender to recipient.
// The message is signed and encrypted.
func NewTextMessage(sender *identity.Identity, recipientPub [32]byte, recipientHash identity.Hash, seqNum uint64, text string) (*Message, error) {
	payload := &Payload{
		Type:      TypeText,
		SeqNum:    seqNum,
		Timestamp: time.Now().Unix(),
		Content:   text,
	}
	return newMessage(sender, recipientPub, recipientHash, payload)
}

// NewMessage creates a new message of arbitrary type from sender to recipient.
// The message is signed and encrypted.
func NewMessage(sender *identity.Identity, recipientPub [32]byte, recipientHash identity.Hash, payload *Payload) (*Message, error) {
	return newMessage(sender, recipientPub, recipientHash, payload)
}

func newMessage(sender *identity.Identity, recipientPub [32]byte, recipientHash identity.Hash, payload *Payload) (*Message, error) {
	if !sender.HasPrivateKey() {
		return nil, fmt.Errorf("message: sender must have private key")
	}

	// Serialize payload
	payloadBytes, err := msgpack.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("message: marshal payload: %w", err)
	}

	// Encrypt payload
	encrypted, err := sender.Encrypt(payloadBytes, recipientPub)
	if err != nil {
		return nil, fmt.Errorf("message: encrypt payload: %w", err)
	}

	// Generate message nonce
	var nonce [NonceSize]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nil, fmt.Errorf("message: generate nonce: %w", err)
	}

	msg := &Message{
		DestHash:         recipientHash,
		SourceHash:       sender.Hash(),
		Nonce:            nonce,
		EncryptedPayload: encrypted,
		Payload:          payload,
	}

	// Sign: dest_hash + source_hash + nonce + encrypted_payload
	sigData := msg.signingData()
	sig, err := sender.Sign(sigData)
	if err != nil {
		return nil, fmt.Errorf("message: sign: %w", err)
	}
	copy(msg.Signature[:], sig)

	return msg, nil
}

// signingData returns the bytes that are covered by the signature.
func (m *Message) signingData() []byte {
	var buf []byte
	buf = append(buf, m.DestHash[:]...)
	buf = append(buf, m.SourceHash[:]...)
	buf = append(buf, m.Nonce[:]...)
	buf = append(buf, m.EncryptedPayload...)
	return buf
}

// VerifySignature checks the message signature against the sender's public key.
func (m *Message) VerifySignature(senderPub []byte) bool {
	sender := identity.FromPublicKey(senderPub)
	return sender.Verify(m.signingData(), m.Signature[:])
}

// Decrypt decrypts the message payload using the recipient's identity and
// the sender's X25519 public key. On success, the Payload field is populated.
func (m *Message) Decrypt(recipient *identity.Identity, senderX25519Pub [32]byte) error {
	plaintext, err := recipient.Decrypt(m.EncryptedPayload, senderX25519Pub)
	if err != nil {
		return fmt.Errorf("message: decrypt: %w", err)
	}

	var payload Payload
	if err := msgpack.Unmarshal(plaintext, &payload); err != nil {
		return fmt.Errorf("message: unmarshal payload: %w", err)
	}

	m.Payload = &payload
	return nil
}

// Marshal serializes the message to the wire format:
// dest_hash(16) + source_hash(16) + nonce(24) + signature(64) + encrypted_payload
func (m *Message) Marshal() []byte {
	size := HeaderSize + len(m.EncryptedPayload)
	buf := make([]byte, 0, size)
	buf = append(buf, m.DestHash[:]...)
	buf = append(buf, m.SourceHash[:]...)
	buf = append(buf, m.Nonce[:]...)
	buf = append(buf, m.Signature[:]...)
	buf = append(buf, m.EncryptedPayload...)
	return buf
}

// Unmarshal parses a message from the wire format.
// The payload remains encrypted; call Decrypt to access the cleartext.
func Unmarshal(data []byte) (*Message, error) {
	if len(data) < HeaderSize {
		return nil, fmt.Errorf("message: data too short (%d bytes, need at least %d)", len(data), HeaderSize)
	}

	msg := &Message{}
	offset := 0

	copy(msg.DestHash[:], data[offset:offset+identity.HashSize])
	offset += identity.HashSize

	copy(msg.SourceHash[:], data[offset:offset+identity.HashSize])
	offset += identity.HashSize

	copy(msg.Nonce[:], data[offset:offset+NonceSize])
	offset += NonceSize

	copy(msg.Signature[:], data[offset:offset+SignatureSize])
	offset += SignatureSize

	msg.EncryptedPayload = make([]byte, len(data)-offset)
	copy(msg.EncryptedPayload, data[offset:])

	return msg, nil
}

// SeqTracker tracks sequence numbers per sender for replay prevention
// and message ordering.
type SeqTracker struct {
	// lastSeq maps sender hash to the highest sequence number seen.
	lastSeq map[identity.Hash]uint64
}

// NewSeqTracker creates a new sequence number tracker.
func NewSeqTracker() *SeqTracker {
	return &SeqTracker{
		lastSeq: make(map[identity.Hash]uint64),
	}
}

// Check verifies that a sequence number is valid (strictly greater than
// the last seen sequence number for this sender). Returns an error if
// the message appears to be a replay or out of order.
func (st *SeqTracker) Check(sender identity.Hash, seqNum uint64) error {
	last, ok := st.lastSeq[sender]
	if ok && seqNum <= last {
		return fmt.Errorf("message: sequence number %d <= last seen %d (possible replay)", seqNum, last)
	}
	return nil
}

// Update records a sequence number as seen. Call after Check succeeds
// and the message has been fully validated.
func (st *SeqTracker) Update(sender identity.Hash, seqNum uint64) {
	st.lastSeq[sender] = seqNum
}

// NextSeqNum is a helper that generates monotonically increasing sequence
// numbers for outgoing messages from this node.
type NextSeqNum struct {
	counter uint64
}

// NewNextSeqNum creates a new sequence number generator starting from the
// given value (typically loaded from persistent state, or 0 for a fresh node).
func NewNextSeqNum(start uint64) *NextSeqNum {
	return &NextSeqNum{counter: start}
}

// Next returns the next sequence number and increments the counter.
func (n *NextSeqNum) Next() uint64 {
	n.counter++
	return n.counter
}

// Current returns the current (last issued) sequence number without incrementing.
func (n *NextSeqNum) Current() uint64 {
	return n.counter
}

// EstimateWireSize returns the approximate wire size of a text message
// with the given content length. Useful for checking LoRa payload limits.
func EstimateWireSize(contentLen int) int {
	// Header (120) + NaCl overhead (24 nonce + 16 poly1305) + msgpack overhead (~20) + content
	return HeaderSize + 24 + 16 + 20 + contentLen
}

// TimestampBytes converts a Unix timestamp to big-endian bytes.
func TimestampBytes(ts int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(ts))
	return b
}
