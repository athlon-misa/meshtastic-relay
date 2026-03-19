// Package proto holds protobuf definitions for the relay system.
//
// This includes:
//   - Meshtastic protobuf imports for serial communication with the radio
//   - Relay-specific protobuf definitions (if needed beyond msgpack)
//
// Currently, message serialization uses msgpack (via the message package).
// Protobuf is used only for the Meshtastic serial interface in the
// meshradio transport.
//
// To regenerate protobuf Go code:
//
//	protoc --go_out=. --go_opt=paths=source_relative proto/*.proto
package proto
