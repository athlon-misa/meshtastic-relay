# Meshtastic Relay

A hybrid internet/LoRa mesh relay daemon written in Go. Fixed Meshtastic nodes
(Raspberry Pi + LoRa radio) are deployed at homes, offices, and public places.
Users connect via WiFi or BLE from their phone to the nearest node. Their
cryptographic identity (Ed25519 keypair) is portable -- they are recognized
network-wide regardless of which node they are connected to.

Messages route over the internet (MQTT) when available, and fall back to LoRa
radio mesh when internet connectivity is lost.

## Architecture

```
Phone (WiFi/BLE) --> [Relay Node (RPi + LoRa)] --MQTT--> [Relay Node] --> Phone
                                                   |
                                                   +--LoRa--> [Relay Node] --> Phone
```

### Core Components

| Package | Description |
|---------|-------------|
| `identity/` | Ed25519/X25519 cryptographic identity system |
| `announce/` | Presence announcement and propagation |
| `message/` | Wire format, end-to-end encryption (NaCl crypto_box) |
| `transport/` | Transport abstraction with MQTT and Meshtastic serial backends |
| `transport/router/` | Path selection with internet-first, LoRa fallback |
| `node/` | Multi-user relay daemon and connection management |
| `store/` | Store-and-forward message queue (BoltDB) |
| `config/` | YAML configuration loading |
| `cmd/relayd/` | Main daemon entry point |

### Identity System

Every user has an Ed25519 keypair. Their identity hash is
`SHA-256(public_key)[:16]` (truncated to 16 bytes). Key exchange uses X25519
ECDH with HKDF-SHA256 key derivation, and messages are encrypted with
XSalsa20-Poly1305 (NaCl crypto_box).

### Transport Layer

The relay daemon maintains two transport paths:

- **MQTT** (primary): Connects to a shared MQTT broker for internet-based
  message relay between nodes.
- **Meshtastic Serial** (fallback): Communicates with a locally attached
  Meshtastic LoRa radio over serial protobuf for off-grid mesh routing.

A router component monitors health of each transport and selects the best path,
with automatic failover when internet connectivity degrades.

## Building

```bash
# Build for current platform
make build

# Cross-compile for Raspberry Pi (ARMv7)
make build-arm

# Cross-compile for Raspberry Pi 4+ (ARM64)
make build-arm64

# Run tests
make test

# Run tests with coverage
make test-cover

# Lint
make lint
```

## Configuration

Copy and edit the example configuration:

```bash
cp config.example.yaml config.yaml
```

See `config/` package documentation for all available options.

## Running

```bash
./bin/relayd -config config.yaml
```

## Project Status

This project is in early development. The core cryptographic identity system,
message format, and transport abstractions are implemented. Integration testing
and production hardening are ongoing.

## Acknowledgments

This project implements protocol concepts inspired by the
[Reticulum Network Stack](https://reticulum.network/), which is released under
the MIT License. Meshtastic Relay is a clean-room reimplementation of these
public-domain protocol concepts in Go -- it does not fork or copy any Reticulum
source code.

Meshtastic Relay also integrates with [Meshtastic](https://meshtastic.org/)
LoRa radios for off-grid mesh communication.

## License

See LICENSE file for details.
