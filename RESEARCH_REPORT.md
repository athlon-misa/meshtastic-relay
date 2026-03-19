# Meshtastic Roaming Relay System -- Research Report

**Date:** 2026-03-12
**Status:** Research & Feasibility Analysis

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [How Meshtastic Works](#2-how-meshtastic-works)
3. [Roaming Identity: State of the Art](#3-roaming-identity-state-of-the-art)
4. [Proposed Architecture](#4-proposed-architecture)
5. [Risk Analysis](#5-risk-analysis)
6. [Feasibility Assessment](#6-feasibility-assessment)
7. [Recommended Next Steps](#7-recommended-next-steps)

---

## 1. Executive Summary

### The Vision

Users carry no Meshtastic hardware. Instead, fixed nodes are deployed at homes, offices, and public spaces. A user connects to the nearest node via WiFi or Bluetooth from their phone/laptop. Their identity and contacts become visible network-wide, regardless of which node they're on. Messages route over the internet when available (fast path) and fall back to LoRa radio when it's not (disaster-resilient path).

### Key Findings

| Finding | Impact |
|---------|--------|
| **Meshtastic uses LoRa, NOT LoRaWAN** | No infrastructure needed; peer-to-peer mesh; but extremely low bandwidth (~1-5 kbps) |
| **Identity is device-bound** | Node ID = MAC address. No native roaming. This is the core problem to solve. |
| **MQTT bridge exists** | Meshtastic already supports internet bridging via MQTT. We can build on this. |
| **ESP32 supports only 1 BLE connection** | A single ESP32 node can serve only one user at a time. A companion computer (RPi) is needed for multi-user. |
| **Reticulum solves roaming identity** | Identity = cryptographic keypair, fully portable. The best existing model to draw from. |
| **LoRa throughput is the hard constraint** | ~1-2 messages/minute over LoRa. Internet MUST be the primary path; LoRa is emergency fallback only. |

### Is It Possible?

**Yes.** The system is feasible but requires a relay daemon running on a companion computer (Raspberry Pi) at each node, with a dedicated Meshtastic radio for LoRa mesh access. The user-facing connectivity (WiFi/BLE multi-user) happens on the Pi, not on the Meshtastic hardware directly.

---

## 2. How Meshtastic Works

### 2.1 LoRa vs LoRaWAN -- A Critical Distinction

Meshtastic uses **raw LoRa**, not LoRaWAN. This matters enormously:

| | LoRa | LoRaWAN | Meshtastic |
|---|---|---|---|
| **Layer** | Physical (radio modulation) | Network protocol on LoRa | Mesh protocol on LoRa |
| **Architecture** | Point-to-point | Star topology with central gateways | Decentralized peer-to-peer mesh |
| **Infrastructure** | None | Gateways + network server required | None required |

LoRa uses **Chirp Spread Spectrum (CSS)** modulation on license-free ISM bands:
- **US:** 902-928 MHz
- **EU:** 863-870 MHz
- **AU:** 915-928 MHz

Configurable parameters: Spreading Factor (SF7-SF12), Bandwidth (31-500 kHz), Coding Rate. Higher SF = longer range but slower.

**Range:** Up to 254 km recorded (line of sight, mountains). Typical urban: 0.5-3 km. Typical rural: 5-30+ km.

### 2.2 Mesh Routing

**Primary: Managed Flood Routing**
1. Node transmits packet with a `hop_limit` (max 7, default 3)
2. Receivers consider rebroadcasting
3. SNR-dependent contention: farther nodes (lower SNR) rebroadcast first; closer nodes hear this and suppress
4. ROUTER/REPEATER role nodes get priority

**Secondary: Next-Hop Routing (v2.6+, DMs only)**
- First DM uses flooding to discover a path
- Successful relay node is recorded as next-hop
- Subsequent DMs are directed, not flooded
- Falls back to flooding if next-hop fails

### 2.3 Packet Structure

Total LoRa packet: **256 bytes max** (237 bytes encrypted payload + 16-byte cleartext header)

Everything is **Protocol Buffers (protobuf)**. The `MeshPacket` contains:
- `from` / `to`: 32-bit node numbers (cleartext in header)
- `channel`: channel index (0-7)
- `id`: unique packet ID (used as encryption nonce)
- `hop_limit` / `hop_start`: routing control
- `encrypted` or `decoded` payload (with `portnum` indicating application type)

Key application ports: TEXT_MESSAGE (1), POSITION (3), NODEINFO (4), ROUTING (5), ADMIN (6), STORE_FORWARD (65), TELEMETRY (67).

### 2.4 Encryption

**Channel Messages (Group):** AES-256-CTR with Pre-Shared Key (PSK)
- No authentication -- anyone with PSK can impersonate any node
- Default PSK is publicly known (no real security unless changed)

**Direct Messages (v2.5+):** X25519 key exchange + AES-CCM
- Provides both confidentiality AND authenticity
- Each node generates a Curve25519 keypair at first boot
- Public keys broadcast in NodeInfo

### 2.5 User Identity (Current)

- **Node ID:** 32-bit integer from device MAC address (e.g., `!a1b2c3d4`)
- **User Profile:** Long Name, Short Name, hardware model, public key
- **Identity is device-bound** -- switch devices, get new identity
- **Trust on First Use (TOFU)** -- no ongoing verification

**Active proposal ([Issue #5006](https://github.com/meshtastic/firmware/issues/5006)):** Derive node ID from public key instead of MAC. This would enable key backup/restore across devices.

### 2.6 Device Communication

| Method | Protocol | Notes |
|--------|----------|-------|
| **BLE** | GATT + protobuf | Primary mobile connection. **One connection at a time.** |
| **WiFi/TCP** | TCP + protobuf | ESP32 only. **Disables BLE when active.** |
| **USB Serial** | Serial + protobuf | For Python CLI, RPi companion |

### 2.7 MQTT Bridge (Existing)

Meshtastic already supports internet bridging:
- WiFi-capable nodes connect to an MQTT broker
- Mesh packets are published as `ServiceEnvelope` protobufs
- Bidirectional: mesh-to-internet and internet-to-mesh
- Topic structure: `msh/{REGION}/2/e/{CHANNEL}/{NODE_ID}`
- Default public broker: `mqtt.meshtastic.org`

**Limitation:** Zero-hop policy on public MQTT -- traffic from MQTT doesn't propagate further into the local mesh. No identity bridging.

### 2.8 Hardware

| Platform | WiFi | BLE | Power | Notes |
|----------|------|-----|-------|-------|
| **ESP32/S3** | Yes | Yes | Higher | Most versatile, WiFi capable |
| **nRF52840** | No | Yes | Very low | Best for solar/battery |
| **RP2040** | Varies | Varies | Low | Raspberry Pi Pico based |
| **Linux (Portduino)** | Yes | Optional | N/A | RPi, x86 |

Popular devices: LILYGO T-Beam, T-Echo, T-Deck; RAKwireless WisBlock; Heltec LoRa 32.

### 2.9 Hard Constraints

| Constraint | Value | Impact |
|------------|-------|--------|
| Max packet size | 256 bytes | Text-only communication |
| Throughput (LONG_FAST) | ~1-5 kbps | ~1-2 messages/min practical |
| EU duty cycle | 10% per hour | Limits transmissions in Europe |
| NodeDB limit | 100 nodes | Oldest nodes roll off |
| Max hops | 7 (default 3) | Limits mesh diameter |
| BLE connections | 1 per device | Can't serve multiple users |

---

## 3. Roaming Identity: State of the Art

### 3.1 The Core Problem

Meshtastic identity = device MAC address. If you want user X to be recognizable regardless of which node they connect to, you need to **decouple identity from hardware**.

### 3.2 How Others Solve This

**Reticulum Network Stack -- The Gold Standard**
- Identity = Ed25519 + X25519 keypair (512-bit)
- Address = SHA-256 hash of destination characteristics
- **Fully portable**: Move your keypair to any device, any network, any transport
- **Announce mechanism**: Send an announce packet on new network -> globally reachable in seconds
- **Multi-transport**: Same identity works across LoRa, WiFi, TCP, I2P, packet radio
- Store-and-forward via LXMF protocol

**Yggdrasil / CJDNS**
- IPv6 address derived from public key hash
- Portable by moving keypair
- Network-layer identity (not application-layer user identity)

**Briar Messenger**
- Local device-bound identity
- Transport-agnostic (BLE, WiFi, Tor)
- No roaming support

**MeshCore**
- Signed identity advertisements
- Room Servers for store-and-forward
- Partial roaming via BBS-style message retrieval

**Ham Radio Model**
- Callsign = operator, not radio
- Portable across any hardware
- Externally assigned (not applicable to decentralized systems)

### 3.3 Recommended Identity Architecture

Based on the research, the strongest approach combines:

1. **Keypair-based identity**: Ed25519 for signing, X25519 for encryption. Identity = public key hash.
2. **Announce-based presence**: When connecting to a node, broadcast presence (Reticulum model)
3. **TOFU + out-of-band verification**: Accept keys on first contact; verify via QR codes or safety numbers
4. **Nonce-based replay protection**: Monotonic sequence numbers per sender
5. **Challenge-response auth**: Node challenges user to prove key possession before granting access

### 3.4 Presence Discovery Approaches

| Approach | Bandwidth | Latency | Best For |
|----------|-----------|---------|----------|
| Full flood | High | Low | Small meshes (<50 nodes) |
| Reactive path request | Low | Higher first contact | LoRa (bandwidth-constrained) |
| DHT lookup | Low | Medium | Large internet-connected networks |
| Gossip | Medium | Eventually consistent | Large meshes, non-critical |
| **MQTT retained messages** | **Negligible** | **<5 sec** | **Internet-connected relay nodes** |

**Recommended:** MQTT retained messages for internet-connected nodes (primary), LoRa announce flooding for radio-only fallback.

---

## 4. Proposed Architecture

### 4.1 System Overview

```
                          ┌─────────────────────────────────────┐
                          │         MQTT Broker Cluster         │
                          │  (presence, messages, routing)      │
                          └──────┬──────────┬──────────┬────────┘
                                 │          │          │
                            Internet    Internet   Internet
                                 │          │          │
                    ┌────────────┴─┐  ┌─────┴────────┐ │
                    │  Relay Node  │  │  Relay Node   │ ...
                    │  (Home)      │  │  (Office)     │
                    │              │  │               │
                    │ ┌──────────┐ │  │ ┌───────────┐ │
                    │ │ RPi 4/5  │ │  │ │  RPi 4/5  │ │
                    │ │ - Relay  │ │  │ │  - Relay   │ │
                    │ │   Daemon │ │  │ │    Daemon  │ │
                    │ │ - WiFi AP│ │  │ │  - WiFi AP │ │
                    │ │ - BLE    │ │  │ │  - BLE     │ │
                    │ └────┬─────┘ │  │ └─────┬──────┘ │
                    │      │USB    │  │       │USB     │
                    │ ┌────┴─────┐ │  │ ┌─────┴──────┐ │
                    │ │Meshtastic│ │  │ │ Meshtastic  │ │
                    │ │Radio     │ │  │ │ Radio       │ │
                    │ │(T-Beam / │ │  │ │ (T-Beam /   │ │
                    │ │ RAK4631) │ │  │ │  RAK4631)   │ │
                    │ └──────────┘ │  │ └─────────────┘ │
                    └──────────────┘  └─────────────────┘
                           ╲                  ╱
                        LoRa  ╲          ╱  LoRa
                          mesh  ╲      ╱  mesh
                                 ╲    ╱
                           ┌──────╲──╱──────┐
                           │  Relay Node    │
                           │  (Public/Park) │
                           │  Solar+Battery │
                           └────────────────┘

   Users connect via WiFi or BLE to the RPi at any node.
   Their identity (keypair) travels with them on their phone.
```

### 4.2 Node Hardware

Each relay node consists of:

| Component | Role | Cost |
|-----------|------|------|
| **Raspberry Pi 4/5** | Relay daemon, WiFi AP, BLE host, internet uplink | $35-80 |
| **Meshtastic Radio** (T-Beam or RAK WisBlock) | LoRa mesh transceiver, connected via USB serial in PROTO mode | $25-45 |
| **Antenna** (tuned for band) | LoRa range | $10-30 |
| **Power supply** / Solar+battery for outdoor | Resilience | $20-100 |
| **Case** | Weather protection | $10-30 |
| **Total per node** | | **$100-285** |

**Why a companion computer?** The ESP32 on the Meshtastic radio supports only 1 BLE connection and disables BLE when WiFi is active. A multi-user relay requires the RPi to handle user connections while the Meshtastic radio handles LoRa mesh duties.

### 4.3 Software Stack

```
┌─────────────────────────────────────────────┐
│                User's Phone                  │
│  ┌─────────────────────────────────────────┐ │
│  │         Relay Client App                │ │
│  │  - Ed25519/X25519 keypair storage       │ │
│  │  - Challenge-response auth              │ │
│  │  - End-to-end encryption                │ │
│  │  - Message compose/read UI              │ │
│  └─────────────────────────────────────────┘ │
└──────────────────┬──────────────────────────┘
                   │ WiFi or BLE
┌──────────────────┴──────────────────────────┐
│            Relay Daemon (on RPi)             │
│  ┌────────────────────────────────────────┐  │
│  │  Connection Manager                    │  │
│  │  - Multi-user WiFi AP / BLE server     │  │
│  │  - User authentication (challenge-     │  │
│  │    response with Ed25519)              │  │
│  ├────────────────────────────────────────┤  │
│  │  Identity & Presence Manager           │  │
│  │  - Track connected users               │  │
│  │  - Publish presence to MQTT            │  │
│  │  - Subscribe to contact presence       │  │
│  ├────────────────────────────────────────┤  │
│  │  Message Router                        │  │
│  │  - Internet path (MQTT) -- primary     │  │
│  │  - LoRa path (serial to radio) --      │  │
│  │    fallback                            │  │
│  │  - Path selection based on             │  │
│  │    connectivity health score           │  │
│  ├────────────────────────────────────────┤  │
│  │  Store & Forward                       │  │
│  │  - Queue messages for offline users    │  │
│  │  - Deliver when they reconnect         │  │
│  ├────────────────────────────────────────┤  │
│  │  Meshtastic Serial Interface           │  │
│  │  - USB serial to Meshtastic radio      │  │
│  │  - Protobuf encode/decode              │  │
│  │  - Inject/extract mesh packets         │  │
│  └────────────────────────────────────────┘  │
└──────────────────────────────────────────────┘
```

**Recommended tech stack:**
- **Relay daemon:** Python (leveraging `meshtastic` Python library for serial interface) or Rust (for performance on constrained RPi)
- **MQTT broker:** Mosquitto (self-hosted) or EMQX (clustered production)
- **Client app:** Cross-platform (Flutter/React Native) or web-based (served from the relay node)
- **Identity:** libsodium (NaCl) for Ed25519/X25519 crypto

### 4.4 User Identity System

**Identity = Ed25519 keypair generated on the user's phone**

```
User Identity:
  - Public Key:  32 bytes (Ed25519)
  - User ID:     First 4 bytes of SHA-256(public_key) = 8 hex chars
  - Display Name: "Alice" (user-chosen, not unique)
  - Short Name:   "AL" (2-4 chars for constrained displays)
```

**Authentication flow when connecting to a node:**

```
User Phone                          Relay Node
    │                                    │
    │──── Connect (WiFi/BLE) ──────────>│
    │                                    │
    │<─── Challenge (random nonce) ──────│
    │                                    │
    │──── Response (sign nonce with ─────>│
    │     Ed25519 private key +          │
    │     public key)                    │
    │                                    │
    │     [Node verifies signature]      │
    │                                    │
    │<─── Authenticated ────────────────│
    │                                    │
    │     [Node publishes presence       │
    │      to MQTT: "User Alice is       │
    │      now on Node-Home"]            │
    │                                    │
```

**Key management:**
- Keypair generated on first app launch
- Private key stored in phone's secure enclave / keystore
- Backup via seed phrase (BIP-39 style deterministic derivation)
- Contact exchange via QR codes (share public key + display name)
- TOFU for casual contacts, QR verification for trusted contacts

### 4.5 Presence System

**Internet path (primary):**

```
MQTT Topics:
  relay/{region}/presence/{user_id}     -- retained message: {node_id, timestamp, public_key}
  relay/{region}/msg/{user_id}          -- messages for this user
  relay/{region}/announce/{node_id}     -- node status & connected users
```

- 30-second heartbeat from each relay node listing connected users
- MQTT retained messages provide instant presence on subscribe
- Convergence time: <5 seconds across all internet-connected nodes

**LoRa fallback (disaster mode):**

- Compact presence announcement packet (fits in 237 bytes):
  ```
  [user_id: 4B] [node_id: 4B] [timestamp: 4B] [signature: 64B] [display_name: up to 20B]
  ```
- Flooded with hop_limit=3 (like standard Meshtastic NodeInfo)
- Updated only on connect/disconnect events (bandwidth conservation)
- Nodes maintain local routing table: user_id -> node_id

### 4.6 Dual-Path Routing

```
Message Routing Decision:

  1. User sends message to contact
  2. Relay daemon looks up contact's current node
     ├── Contact found via MQTT presence?
     │   └── YES: Route via MQTT (internet path, <1 sec latency)
     │   └── NO: Route via LoRa mesh (radio path, seconds-minutes latency)
     └── Contact offline?
         └── Queue in store-and-forward, deliver on reconnect

Internet Health Score (0-100):
  - MQTT broker reachable?     (40 points)
  - DNS resolution working?    (15 points)
  - Ping latency < 500ms?      (15 points)
  - Last MQTT publish ACKed?   (15 points)
  - No packet loss in 5 min?   (15 points)

  Score > 60: Use internet path
  Score < 60: Switch to LoRa fallback
```

**Failover behavior:**

| Internet Status | Behavior |
|----------------|----------|
| **Healthy (score > 60)** | All messages via MQTT. LoRa used only for local mesh management. |
| **Degraded (score 30-60)** | Critical messages via both paths. Non-critical internet-only with retry. |
| **Down (score < 30)** | Full LoRa mode. Messages prioritized: emergency > DM > channel > telemetry. |
| **Recovering** | Drain queued messages via internet. Sync missed messages between nodes. |

### 4.7 Message Flow Example

```
Alice (at Home Node) sends message to Bob (at Office Node):

  INTERNET PATH (normal):
  Alice Phone ──WiFi──> Home RPi ──MQTT──> Office RPi ──WiFi──> Bob Phone
  Latency: ~100-500ms

  LORA FALLBACK (internet down):
  Alice Phone ──WiFi──> Home RPi ──Serial──> Home Radio ──LoRa──>
    [mesh hops] ──LoRa──> Office Radio ──Serial──> Office RPi ──WiFi──> Bob Phone
  Latency: 5-60 seconds (depending on hops and congestion)

  STORE-AND-FORWARD (Bob offline):
  Alice Phone ──WiFi──> Home RPi ──MQTT──> Office RPi [queues message]
    ... Bob connects later ...
  Office RPi ──WiFi──> Bob Phone [delivers queued messages]
```

### 4.8 End-to-End Encryption

Messages are encrypted **on the sender's phone** and decrypted **on the recipient's phone**. Relay nodes never see plaintext.

```
Encryption: X25519 key exchange + XSalsa20-Poly1305 (NaCl crypto_box)

Alice -> Bob:
  1. Alice's app computes shared_secret = X25519(alice_private, bob_public)
  2. Encrypts: ciphertext = crypto_box(message, nonce, shared_secret)
  3. Sends: {from: alice_id, to: bob_id, nonce, ciphertext}
  4. Bob's app computes same shared_secret = X25519(bob_private, alice_public)
  5. Decrypts: message = crypto_box_open(ciphertext, nonce, shared_secret)

Relay nodes forward opaque ciphertext. Even a compromised relay cannot read messages.
```

---

## 5. Risk Analysis

### 5.1 Technical Risks

| Risk | Severity | Likelihood | Mitigation |
|------|----------|------------|------------|
| **LoRa bandwidth insufficient for multi-user relay** | HIGH | CERTAIN | Internet-first design. LoRa is emergency fallback only. Prioritize messages in radio-only mode. |
| **EU 10% duty cycle limits LoRa** | HIGH | CERTAIN (EU) | Strict airtime budgeting. Compress packets. Prioritize critical messages. |
| **ESP32 single BLE connection** | HIGH | CERTAIN | RPi companion handles multi-user. Meshtastic radio is LoRa-only via serial. |
| **Message latency over LoRa** | MEDIUM | LIKELY | Set user expectations. Internet path provides real-time messaging. LoRa is best-effort. |
| **Packet loss over LoRa** | MEDIUM | LIKELY | Application-level ACKs and retries. Store-and-forward for reliability. |
| **Clock drift across nodes** | LOW | POSSIBLE | NTP when internet available. Relative timestamps. Sequence numbers for ordering. |

### 5.2 Security Risks

| Risk | Severity | Likelihood | Mitigation |
|------|----------|------------|------------|
| **Identity spoofing** | HIGH | POSSIBLE | Ed25519 signatures on all messages. Challenge-response auth on connection. |
| **Compromised relay node** | HIGH | POSSIBLE | E2E encryption means relay sees only ciphertext. Node compromise reveals presence metadata only. |
| **Key loss (phone lost/destroyed)** | MEDIUM | LIKELY | Seed phrase backup for key recovery. Social recovery via trusted contacts. |
| **MITM at public nodes** | MEDIUM | POSSIBLE | Public keys verified via QR at first contact. Key change warnings (like Signal). |
| **Replay attacks** | LOW | POSSIBLE | Monotonic nonces. Timestamp windows. Sequence number tracking. |
| **Traffic analysis** | LOW | LIKELY | Attacker can see who talks to whom (metadata) even with E2E. Cover traffic is too expensive on LoRa. |

### 5.3 Operational Risks

| Risk | Severity | Likelihood | Mitigation |
|------|----------|------------|------------|
| **SD card failure on RPi** | MEDIUM | LIKELY | OverlayFS read-only root. Logs to tmpfs. Periodic state backup to MQTT. |
| **Power failure at node** | MEDIUM | LIKELY | UPS/battery backup. Solar for outdoor nodes. Graceful degradation tiers. |
| **Node operator burnout** | MEDIUM | POSSIBLE | Minimal maintenance via auto-updates. Remote monitoring. Plug-and-forget design. |
| **Firmware compatibility breaks** | LOW | POSSIBLE | Pin Meshtastic firmware versions. Test updates before rollout. |

### 5.4 Regulatory Risks

| Risk | Severity | Likelihood | Mitigation |
|------|----------|------------|------------|
| **LoRa duty cycle violations** | HIGH | POSSIBLE | Firmware-enforced airtime budgets. Conservative defaults. |
| **ISM band power limits** | MEDIUM | LOW | Use certified hardware with proper power settings per region. |
| **Data privacy (GDPR etc)** | MEDIUM | POSSIBLE | No central user database. Presence data is ephemeral. E2E encryption by default. |

---

## 6. Feasibility Assessment

### 6.1 What's Clearly Feasible

- **Internet relay between nodes via MQTT** -- Meshtastic already does this. We're extending it with user identity and multi-user support.
- **Cryptographic roaming identity** -- Proven by Reticulum. Well-understood cryptographic primitives.
- **RPi + Meshtastic radio as relay node** -- The `meshtastic` Python library supports serial communication with the radio in PROTO mode.
- **Dual-path routing** -- Straightforward: prefer internet, fall back to LoRa. Health score determines path.
- **Store-and-forward** -- Standard message queuing. Nothing exotic.

### 6.2 What's Challenging But Doable

- **Multi-user WiFi AP on RPi** -- Well-supported with hostapd. Need a captive portal or app for authentication.
- **Multi-user BLE on RPi** -- BlueZ supports multiple GATT connections. More complex than WiFi.
- **Presence convergence across nodes** -- MQTT retained messages solve this cleanly for internet-connected nodes.
- **Client app development** -- Cross-platform app with keypair management, E2E crypto, and mesh-aware UX.

### 6.3 What's Hard

- **LoRa-only presence propagation** -- Very limited bandwidth. Can support ~10-20 users announcing presence per hour without saturating the channel. Works for small communities, not at scale.
- **Seamless handoff between nodes** -- User disconnects from one node, connects to another. There will be a gap (seconds to minutes) where the user appears offline. Not instant like WiFi roaming.
- **Interop with standard Meshtastic** -- Users on standard Meshtastic devices (with their own radios) would see relay users as "virtual" nodes. Requires custom firmware or a translation layer.

### 6.4 What's Not Feasible on LoRa

- **Real-time messaging over LoRa** -- Too slow. Expect 5-60 second delays per message.
- **Large meshes (100+ users) on LoRa-only** -- Bandwidth saturates quickly. Internet is mandatory for scale.
- **Voice or media over LoRa** -- 237 bytes max per packet. Text only.

---

## 7. Recommended Next Steps

### Phase 1: Proof of Concept (4-6 weeks)

**Goal:** Two RPi relay nodes communicating via MQTT with one user on each.

1. Set up RPi + Meshtastic radio (T-Beam or RAK4631) at two locations
2. Write relay daemon in Python using `meshtastic` serial interface
3. Set up Mosquitto MQTT broker
4. Implement basic user identity (Ed25519 keypair on user's device)
5. Implement presence announcements via MQTT
6. Route text messages between users via MQTT
7. Simple CLI or web client for testing

**Deliverables:** Two-node relay demo with internet-only messaging.

### Phase 2: LoRa Fallback (4-6 weeks)

**Goal:** Messages fall back to LoRa when internet is down.

1. Implement connectivity health scoring
2. Implement LoRa message routing via Meshtastic serial interface
3. Implement path selection (internet vs LoRa)
4. Test failover by disconnecting internet
5. Implement store-and-forward for offline users

**Deliverables:** Dual-path messaging with automatic failover.

### Phase 3: Multi-User & Mobile App (6-8 weeks)

**Goal:** Multiple users per node, proper mobile app.

1. Configure RPi as WiFi AP with captive portal
2. Implement multi-user connection management
3. Build mobile app (Flutter or React Native) with:
   - Keypair generation and storage
   - Contact management (QR code exchange)
   - E2E encrypted messaging
   - Node discovery and connection
4. Test with 5-10 users across 3+ nodes

**Deliverables:** Usable mobile app, multi-user relay nodes.

### Phase 4: Hardening & Scale (ongoing)

- Outdoor node enclosures with solar/battery
- Auto-update mechanism for relay daemon
- Remote monitoring dashboard
- Performance testing at scale
- Interop layer with standard Meshtastic clients
- Community deployment playbook

---

## Appendix A: Comparison with Existing Projects

| Feature | This Project | Meshtastic (vanilla) | Reticulum/LXMF | MeshCore |
|---------|-------------|---------------------|-----------------|----------|
| Roaming identity | Yes (keypair) | No (device-bound) | Yes (keypair) | Partial |
| Multi-user per node | Yes (via RPi) | No (1 BLE conn) | No | No |
| Internet relay | Yes (MQTT) | Yes (MQTT, limited) | Yes (any transport) | No |
| LoRa fallback | Yes | N/A (LoRa primary) | Yes (multi-transport) | LoRa primary |
| E2E encryption | Yes (NaCl) | Partial (DMs only) | Yes | Yes |
| Store-and-forward | Yes | Limited | Yes (LXMF) | Yes (Room Servers) |
| Mobile app | Custom | Mature apps | Sideband | MeshCore app |
| Hardware | RPi + LoRa radio | LoRa radio only | Any | LoRa radio only |

## Appendix B: Key References

- [Meshtastic Documentation](https://meshtastic.org/docs/introduction/)
- [Meshtastic Protobufs](https://github.com/meshtastic/protobufs)
- [Meshtastic Python API](https://python.meshtastic.org/)
- [Meshtastic MQTT Integration](https://meshtastic.org/docs/software/integrations/mqtt/)
- [Meshtastic Encryption](https://meshtastic.org/docs/overview/encryption/)
- [Reticulum Network](https://reticulum.network/)
- [LXMF Protocol](https://github.com/markqvist/LXMF)
- [Node ID from Public Key Proposal](https://github.com/meshtastic/firmware/issues/5006)
- [W3C Decentralized Identifiers](https://www.w3.org/TR/did-1.0/)
- [NaCl Crypto Library](https://nacl.cr.yp.to/)
