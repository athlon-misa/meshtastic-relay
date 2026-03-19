package bridge

import (
	"encoding/hex"
	"strings"

	"github.com/meshtastic-relay/relay/identity"
)

// MaxOutboundTextLen is the maximum text payload length after formatting
// for LoRa transmission. This accounts for the header overhead added by
// FormatOutboundMessage within the 233-byte LoRa MTU.
const MaxOutboundTextLen = 200

// shortHashHexLen is the number of hex characters in a short hash (6 bytes).
const shortHashHexLen = 12

// fullHashHexLen is the number of hex characters in a full identity hash.
const fullHashHexLen = identity.HashSize * 2

// ParseBridgeMessage parses the "@<hash> <message>" addressing format used
// by Meshtastic devices to direct messages to relay-network users.
//
// The hash can be either a 12-character short hash (6 bytes) or a 32-character
// full hash (16 bytes). Both are returned as the raw hex string without
// resolution; the caller is responsible for resolving short hashes to full
// identity hashes.
//
// Returns the destination hash hex string, the message body, and a boolean
// indicating whether the parse was successful.
func ParseBridgeMessage(text string) (destHashHex string, body string, ok bool) {
	text = strings.TrimSpace(text)
	if len(text) == 0 || text[0] != '@' {
		return "", "", false
	}

	// Find the space separating hash from body.
	spaceIdx := strings.IndexByte(text, ' ')
	if spaceIdx < 0 {
		// "@<hash>" with no message body is not valid.
		return "", "", false
	}

	hashHex := text[1:spaceIdx]
	msgBody := strings.TrimSpace(text[spaceIdx+1:])

	if len(msgBody) == 0 {
		return "", "", false
	}

	// Validate the hex string length and content.
	switch len(hashHex) {
	case shortHashHexLen, fullHashHexLen:
		// Valid lengths.
	default:
		return "", "", false
	}

	if _, err := hex.DecodeString(hashHex); err != nil {
		return "", "", false
	}

	return hashHex, msgBody, true
}

// FormatOutboundMessage formats a relay message for display on a standard
// Meshtastic device. The format is "[<short_src_hash>] <message>" so that
// the recipient can see who sent the message using a compact identifier.
//
// If the resulting text exceeds MaxOutboundTextLen, the message body is
// truncated and a trailing ellipsis is appended.
func FormatOutboundMessage(senderHash identity.Hash, text string) string {
	// Short hash: first 6 bytes = 12 hex characters.
	shortHash := hex.EncodeToString(senderHash[:6])
	prefix := "[" + shortHash + "] "

	maxBody := MaxOutboundTextLen - len(prefix)
	if maxBody < 0 {
		maxBody = 0
	}

	if len(text) > maxBody {
		if maxBody > 3 {
			text = text[:maxBody-3] + "..."
		} else {
			text = text[:maxBody]
		}
	}

	return prefix + text
}
