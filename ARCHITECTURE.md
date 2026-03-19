# Meshtastic Internet Relay System -- Architecture Design

## Document Version

- Date: 2026-03-14
- Status: Active Development (POC Complete)
- Author: Backend Architect
- Last Updated: 2026-03-14 — added LoRa Gateway Bridge, Multi-Device Registration, Presence State Machine

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [System Concept and Constraints](#2-system-concept-and-constraints)
3. [Node Hardware Architecture](#3-node-hardware-architecture)
4. [Internet Relay Layer](#4-internet-relay-layer)
5. [Dual-Path Routing](#5-dual-path-routing)
6. [User Identity and Presence](#6-user-identity-and-presence)
7. [Disaster Resilience](#7-disaster-resilience)
8. [Scalability Analysis](#8-scalability-analysis)
9. [Security Architecture](#9-security-architecture)
10. [Risk Analysis](#10-risk-analysis)
11. [Deployment Topology](#11-deployment-topology)
12. [Technology Stack](#12-technology-stack)
13. [Implementation Phases](#13-implementation-phases)
14. [LoRa Gateway Bridge](#14-lora-gateway-bridge)
15. [Multi-Device Registration](#15-multi-device-registration)
16. [Presence State Machine](#16-presence-state-machine)

---

## 1. Executive Summary

This document proposes an architecture for a hybrid mesh-internet relay system built on Meshtastic hardware. Fixed Meshtastic nodes placed at homes, offices, and public spaces provide LoRa mesh coverage while simultaneously connecting to the internet. Users connect to the nearest node via WiFi or Bluetooth from personal devices (phones, laptops) without needing dedicated Meshtastic hardware. Messages are routed over the internet when available (low latency path) and fall back to LoRa radio when the internet is unavailable (resilient path). User presence is propagated across all nodes so that contacts are visible regardless of which node a user is attached to.

### Key Design Decisions (Summary)

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Node hardware | Raspberry Pi + Meshtastic radio (USB serial) | Pi handles multi-user connections, relay logic, local storage; ESP32 alone cannot serve multiple concurrent users |
| Language | Go | Goroutines for concurrency; single-binary; trivial ARM cross-compilation; strong stdlib |
| Internet relay protocol | MQTT with private broker | Low latency pub/sub; proven at scale; topic-based routing per user/region |
| Presence system | MQTT retained messages + heartbeats + bridge activity tracking | No single point of failure; WebSocket > LoRa > Offline priority |
| Fallback path | LoRa mesh via Meshtastic serial protobuf API | Health-score-based failover; automatic degradation when internet is down |
| User authentication | Ed25519 keypairs with challenge-response | Portable identity not tied to hardware; enables E2E encryption |
| LoRa bridge | Device registry with @hash message format | Stock Meshtastic devices can address relay users without firmware changes |
| Multi-device | Last-heard timeout with configurable active window | Naturally tracks nearest/most-active device without BLE |

---

## 2. System Concept and Constraints

### 2.1 Operational Model

```
USER DEVICE                  RELAY NODE                    INTERNET BACKBONE
(Phone/Laptop)               (Fixed Location)

+-------------+    WiFi/BLE    +------------------+    MQTT/TLS    +------------------+
|  Web App /  | <-----------> | Relay Software   | <-----------> | MQTT Broker      |
|  Mobile App |               | (Companion Pi)   |               | Cluster          |
+-------------+               |                  |               +------------------+
                              | Meshtastic Radio |                       |
                              | (ESP32/nRF52)    | <-- LoRa -->  Other Relay Nodes
                              +------------------+
```

A user walks into a coffee shop. Their phone connects to the local relay node's WiFi access point or joins the venue's WiFi and discovers the relay node via mDNS. The user authenticates with their cryptographic identity. The relay node announces the user's presence to all other relay nodes via MQTT. The user's contacts, connected to other relay nodes anywhere in the network, see that user come online.

### 2.2 Critical Constraints from Meshtastic Platform

These constraints, derived from research into the current Meshtastic platform, fundamentally shape the architecture:

| Constraint | Value | Impact |
|------------|-------|--------|
| Concurrent BLE/WiFi connections per Meshtastic device | **1 user** | Cannot use a single Meshtastic device as a multi-user access point; companion computer required |
| LoRa data rate (LONG_FAST default) | **1.07 kbps** theoretical max | Internet path must be primary for all non-emergency traffic |
| LoRa data rate (SHORT_TURBO max) | **21.88 kbps** theoretical max | Even at maximum speed, LoRa is insufficient for multi-user relay traffic |
| Maximum LoRa hops | **7** (default 3) | Radio-only routing limited to geographic area of a few km to tens of km |
| Meshtastic packet storage | **~30 packets** in device memory | Cannot queue large amounts of offline traffic on the radio device |
| Channel encryption | AES256-CTR with pre-shared key | All nodes sharing a channel share the same key; no per-user encryption at channel level |
| Direct message encryption | Ed25519/Curve25519 PKC (v2.5+) | Per-user encryption available for DMs but not group channels |
| Node identity | Based on hardware MAC address | No built-in portable user identity; must be added at relay layer |
| EU duty cycle | 10% hourly rolling limit | Significantly constrains LoRa throughput in EU regions |
| Message format | Protobuf (binary) | Efficient encoding; relay must speak protobuf to interact with mesh |

### 2.3 Implications

The single-user-connection limit on Meshtastic devices is the most consequential constraint. It means a relay node cannot simply be a Meshtastic device with WiFi enabled -- it must include a companion computer (Raspberry Pi or equivalent) that:

1. Connects to the Meshtastic radio via serial/USB (taking the one available connection slot)
2. Serves multiple users simultaneously over its own WiFi/network interface
3. Runs the relay software that manages user sessions, presence, routing, and internet connectivity

---

## 3. Node Hardware Architecture

### 3.1 Primary Configuration: Companion Pi + Meshtastic Radio

This is the recommended configuration for all permanent relay sites.

```
+------------------------------------------------------------------+
|  RELAY NODE (Physical Enclosure)                                  |
|                                                                   |
|  +---------------------------+   +----------------------------+   |
|  |  Raspberry Pi 4/5         |   |  Meshtastic Radio          |   |
|  |  or CM4 Module            |   |  (RAK WisBlock or T-Beam)  |   |
|  |                           |   |                            |   |
|  |  - Relay daemon (Rust)    |   |  - LoRa SX1262 radio       |   |
|  |  - MQTT client            |   |  - Meshtastic firmware     |   |
|  |  - User session manager   |   |  - Serial PROTO mode       |   |
|  |  - Local SQLite DB        |   |  - GPS (optional)          |   |
|  |  - WiFi AP (hostapd)      |   |                            |   |
|  |  - mDNS responder         |   +----------------------------+   |
|  |  - Web UI server          |        |  USB/Serial (PROTO API)   |
|  |  - Ethernet uplink        |--------+                          |
|  +---------------------------+                                    |
|                                                                   |
|  +---------------------------+                                    |
|  |  Power System             |                                    |
|  |  - UPS / Battery backup   |                                    |
|  |  - Solar charge controller|                                    |
|  |  - PoE (if Ethernet)      |                                    |
|  +---------------------------+                                    |
+------------------------------------------------------------------+
```

**Why Raspberry Pi:**
- Serves multiple simultaneous user connections (WiFi AP supports 10-20+ clients)
- Runs full relay daemon with persistent storage, caching, and queuing
- Has Ethernet port for reliable internet uplink
- Can host a web UI for user interaction (no native app required)
- Sufficient CPU/RAM for encryption, protobuf serialization, MQTT client
- Linux ecosystem provides mature networking stack (hostapd, dnsmasq, WireGuard)

**Why a dedicated Meshtastic radio (not the Pi's own LoRa hat):**
- Meshtastic firmware handles all LoRa mesh routing, managed flooding, SNR-based rebroadcast timing, and duty cycle compliance
- Reimplementing Meshtastic's mesh protocol on the Pi would be fragile and diverge from upstream
- The radio device runs battle-tested firmware; the Pi runs relay logic
- Serial PROTO mode exposes the full Meshtastic client API to the Pi

**Recommended radio hardware:**
- **RAK WisBlock (RAK4631 + RAK19007):** nRF52840-based, ultra-low power (critical for battery/solar sites), compact modular design, supports Ethernet add-on module (RAK13800)
- **LILYGO T-Beam (ESP32-S3):** Built-in GPS, larger community, WiFi available as backup path
- **Heltec LoRa 32 V3:** Budget option, ESP32-S3, adequate for basic relay

### 3.2 Lightweight Configuration: ESP32-S3 Standalone

For locations where a full Pi is impractical (size, power, cost), a single ESP32-S3 device running modified Meshtastic firmware can serve as a minimal relay node.

```
+------------------------------------------+
|  LIGHTWEIGHT RELAY NODE                   |
|                                          |
|  ESP32-S3 + LoRa Radio (e.g., T-Beam)   |
|                                          |
|  - Standard Meshtastic firmware          |
|  - MQTT bridge enabled (built-in)        |
|  - WiFi connected to venue network       |
|  - Single user BLE/WiFi connection       |
|  - Limited relay capabilities            |
+------------------------------------------+
```

**Limitations of standalone ESP32:**
- Only one user can connect at a time (Meshtastic firmware constraint)
- No local persistent storage for message queuing
- Limited processing power for relay logic
- No web UI (uses standard Meshtastic app)
- Relies on Meshtastic's built-in MQTT bridge (less flexible)

**Best suited for:** Low-traffic locations, extending LoRa coverage, personal home nodes, backup/redundancy.

### 3.3 Hardware Bill of Materials (Primary Node)

| Component | Example Part | Est. Cost (USD) | Notes |
|-----------|-------------|-----------------|-------|
| Single-board computer | Raspberry Pi 4 (4GB) or Pi 5 | $55-80 | CM4 for compact deployments |
| Meshtastic radio | RAK WisBlock Starter Kit | $30-45 | nRF52840 + SX1262 |
| LoRa antenna | 915MHz fiberglass (US) | $15-30 | Tuned to regional frequency |
| USB cable | USB-A to USB-C | $5 | Pi to radio connection |
| MicroSD card | 32GB industrial grade | $12 | For Pi OS and relay software |
| Enclosure | IP65 weatherproof box | $20-40 | For outdoor deployments |
| UPS / Battery | PiJuice HAT + 12Ah battery | $60-80 | 8-24hr backup depending on load |
| Solar panel (optional) | 20W monocrystalline | $25-40 | For off-grid sites |
| PoE splitter (optional) | IEEE 802.3af splitter | $10-15 | Single cable for power + data |
| **Total (basic indoor)** | | **$120-175** | |
| **Total (outdoor solar)** | | **$220-330** | |

---

## 4. Internet Relay Layer

### 4.1 Protocol Selection: MQTT

**Decision: MQTT 5.0 over TLS with a private broker cluster.**

Evaluation of alternatives:

| Protocol | Pros | Cons | Verdict |
|----------|------|------|---------|
| **MQTT** | Already supported by Meshtastic; pub/sub matches mesh broadcast model; lightweight; excellent library support; supports QoS levels; retained messages for presence; wildcard subscriptions | Not ideal for large binary transfers; broker is a central component | **Selected** |
| WebSocket | Full-duplex; works through firewalls; good browser support | Requires custom routing logic; no built-in pub/sub; higher overhead per connection | Rejected for node-to-node; used for user-facing web UI |
| gRPC | Strong typing via protobuf; bidirectional streaming; high performance | Point-to-point not broadcast; requires service discovery; heavier runtime | Rejected |
| Custom UDP | Lowest latency; full control | Massive development effort; NAT traversal complexity; reliability must be built | Rejected |
| Matrix protocol | Decentralized; E2EE built in; room-based model fits channels | Heavy server requirements; complex protocol; overkill for relay layer | Considered for future user-facing layer |

### 4.2 MQTT Topic Architecture

```
mesh-relay/                                    # Root namespace
  v1/                                          # Protocol version
    presence/                                  # User presence system
      {region}/                                # Geographic region
        {node_id}/                             # Publishing node
          announce                             # User connected/disconnected
          heartbeat                            # Periodic liveness (every 30s)

    messages/                                  # Message routing
      {region}/
        direct/                                # Point-to-point messages
          {dest_user_id}                       # Destination user's public key hash
        channel/                               # Channel/group messages
          {channel_hash}                       # Channel identifier
        broadcast/                             # Network-wide broadcasts
          emergency                            # Emergency alerts (highest priority)
          system                               # System announcements

    routing/                                   # Routing table updates
      {region}/
        {node_id}/
          users                                # Which users are at this node
          neighbors                            # LoRa-reachable neighbor nodes
          status                               # Node health/metrics

    admin/                                     # Administrative
      {region}/
        firmware                               # OTA update notifications
        config                                 # Remote configuration
```

**Key design choices:**
- Region-based partitioning allows nodes to subscribe only to relevant traffic
- Direct messages use destination user ID as topic, so only the node hosting that user receives it
- Channel messages use a hash of the channel name + key, preventing topic enumeration attacks
- QoS 1 (at-least-once delivery) for messages; QoS 0 for heartbeats and telemetry
- Retained messages on presence topics so new nodes immediately learn current state

### 4.3 MQTT Broker Architecture

```
                    +-------------------+
                    |  Load Balancer    |
                    |  (HAProxy/NLB)    |
                    +-------------------+
                      /        |        \
            +--------+    +--------+    +--------+
            | Broker |    | Broker |    | Broker |
            | Node 1 |    | Node 2 |    | Node 3 |
            | (EMQX) |    | (EMQX) |    | (EMQX) |
            +--------+    +--------+    +--------+
                 \             |             /
                  +------------+------------+
                  |     Shared State        |
                  |  (Redis / Built-in      |
                  |   cluster sync)         |
                  +-------------------------+
```

**Broker selection: EMQX** (open-source, Erlang/OTP-based)
- Native clustering with automatic session migration
- Supports millions of concurrent connections
- Built-in authentication plugins (JWT, certificate-based)
- Rule engine for message transformation and routing
- Bridge mode can connect to other MQTT brokers (federation)
- Dashboard for monitoring

**Alternative: Mosquitto** for small deployments (single broker, simpler operations).

**Deployment: Three-node EMQX cluster** behind a TCP load balancer. Each relay node maintains a persistent TLS connection to the broker. If one broker node fails, relay nodes reconnect to another.

### 4.4 Message Format

Messages on the MQTT relay layer use Protocol Buffers, extending the Meshtastic protobuf definitions.

```protobuf
syntax = "proto3";

package meshrelay;

// Envelope for all relay messages
message RelayEnvelope {
  uint32 version = 1;               // Protocol version
  string source_node_id = 2;        // Originating relay node
  string source_user_id = 3;        // Sender's public key hash
  uint64 timestamp_ms = 4;          // Unix timestamp milliseconds
  uint64 message_id = 5;            // Globally unique message ID

  oneof payload {
    DirectMessage direct = 10;
    ChannelMessage channel = 11;
    PresenceUpdate presence = 12;
    RoutingUpdate routing = 13;
    EmergencyAlert emergency = 14;
  }
}

message DirectMessage {
  string dest_user_id = 1;          // Recipient's public key hash
  bytes encrypted_payload = 2;     // NaCl box (X25519 + XSalsa20-Poly1305)
  bytes sender_public_key = 3;     // For key exchange
  bytes nonce = 4;                 // Unique nonce
}

message ChannelMessage {
  string channel_id = 1;           // Channel identifier
  bytes encrypted_payload = 2;     // AES256-GCM encrypted
  bytes nonce = 3;
  uint32 sequence = 4;             // For ordering within channel
}

message PresenceUpdate {
  string user_id = 1;              // Public key hash
  string display_name = 2;         // Human-readable name
  PresenceState state = 3;
  string node_id = 4;              // Node user is connected to
  uint64 last_seen = 5;
  bytes signature = 6;             // Ed25519 signature of this update

  enum PresenceState {
    ONLINE = 0;
    AWAY = 1;
    OFFLINE = 2;
  }
}

message RoutingUpdate {
  string node_id = 1;
  repeated string connected_users = 2;  // Users at this node
  repeated NeighborInfo neighbors = 3;  // LoRa-reachable nodes
  NodeHealth health = 4;

  message NeighborInfo {
    string node_id = 1;
    float snr = 2;                 // Signal quality
    uint32 hop_count = 3;
  }

  message NodeHealth {
    float cpu_percent = 1;
    float memory_percent = 2;
    float battery_percent = 3;
    bool internet_connected = 4;
    uint32 connected_user_count = 5;
    uint64 uptime_seconds = 6;
  }
}

message EmergencyAlert {
  string alert_id = 1;
  string message = 2;
  double latitude = 3;
  double longitude = 4;
  uint64 expiry = 5;
  AlertSeverity severity = 6;

  enum AlertSeverity {
    INFO = 0;
    WARNING = 1;
    CRITICAL = 2;
    LIFE_SAFETY = 3;
  }
}
```

### 4.5 Message Flow: Internet Path

```
User A (Phone)           Node Alpha (Pi)         MQTT Broker         Node Beta (Pi)          User B (Phone)
     |                        |                       |                     |                      |
     |-- WebSocket msg ------>|                       |                     |                      |
     |                        |-- Encrypt (NaCl) ---->|                     |                      |
     |                        |-- MQTT PUBLISH ------>|                     |                      |
     |                        |   topic: mesh-relay/  |                     |                      |
     |                        |   v1/messages/region/ |                     |                      |
     |                        |   direct/{user_b_id}  |                     |                      |
     |                        |                       |-- MQTT DELIVER ---->|                      |
     |                        |                       |                     |-- Decrypt ----------->|
     |                        |                       |                     |-- WebSocket msg ----->|
     |                        |                       |                     |                      |
     |                        |   Typical latency: 50-200ms end-to-end     |                      |
```

### 4.6 Message Routing: Finding a User's Node

The relay layer maintains a distributed presence directory:

1. **On user connect:** The relay node publishes a `PresenceUpdate` to `mesh-relay/v1/presence/{region}/{node_id}/announce` with `state=ONLINE` and a retained flag.

2. **On user disconnect:** Same topic, `state=OFFLINE`, with retained flag.

3. **Heartbeat:** Every 30 seconds, each node publishes to `mesh-relay/v1/presence/{region}/{node_id}/heartbeat` with the list of connected users. This serves as the source of truth; if a heartbeat is missed for 90 seconds, other nodes consider that node's users potentially unreachable.

4. **Routing table construction:** Each relay node subscribes to `mesh-relay/v1/presence/{region}/+/announce` and builds a local routing table mapping `user_id -> node_id`. When a message is sent to a user, the sending node publishes to the topic `mesh-relay/v1/messages/{region}/direct/{user_id}`. The node hosting that user subscribes to `mesh-relay/v1/messages/{region}/direct/{user_id}` for each of its connected users.

5. **User migration:** When a user disconnects from Node A and connects to Node B, Node A publishes OFFLINE, Node B publishes ONLINE, and Node B subscribes to the user's direct message topic. Messages in flight during migration are handled by MQTT QoS 1 (redelivery) and the relay's deduplication layer.

**Consistency guarantee:** Eventual consistency with a convergence window of under 5 seconds in normal operation. The heartbeat mechanism prevents stale presence from persisting indefinitely.

### 4.7 Network Partition Handling (Internet Side)

When a relay node loses its MQTT broker connection:

1. **Detection:** TCP keepalive failure or MQTT PINGRESP timeout (configurable, default 15 seconds).
2. **Local operation:** The node continues serving its locally connected users. Messages between users on the same node are delivered locally.
3. **Queuing:** Outbound messages for remote users are queued in a local SQLite database with a configurable TTL (default: 24 hours, max queue size: 10,000 messages).
4. **Reconnection:** Upon broker reconnection, queued messages are drained in FIFO order with rate limiting to avoid flooding.
5. **LoRa fallback:** If internet is down and the destination user is reachable via LoRa (because their node is a LoRa neighbor), the message is routed over the radio path. See Section 5.

---

## 5. Dual-Path Routing

### 5.1 Path Selection Logic

```
                            +-------------------+
                            |  Message to Send  |
                            +-------------------+
                                     |
                                     v
                          +---------------------+
                          | Is internet up and  |
                          | MQTT broker         |
                          | reachable?          |
                          +---------------------+
                           /  YES           \  NO
                          v                  v
               +------------------+   +------------------+
               | Is destination   |   | Is destination   |
               | user online      |   | node reachable   |
               | (presence known) |   | via LoRa?        |
               | via internet?    |   |                  |
               +------------------+   +------------------+
               /  YES       \  NO     /  YES       \  NO
              v              v       v              v
    +-----------+  +----------+  +-----------+  +-----------+
    | SEND VIA  |  | QUEUE +  |  | SEND VIA  |  | QUEUE     |
    | MQTT      |  | ATTEMPT  |  | LORA      |  | LOCALLY   |
    | (fast     |  | LORA     |  | MESH      |  | (store &  |
    |  path)    |  | FALLBACK |  |           |  |  forward) |
    +-----------+  +----------+  +-----------+  +-----------+
         |               |            |               |
         v               v            v               v
    +-----------+  +-----------+  +-----------+  +-----------+
    | Delivery  |  | Delivery  |  | Delivery  |  | Retry     |
    | confirm   |  | confirm   |  | confirm   |  | when path |
    | via MQTT  |  | via either|  | via LoRa  |  | available |
    | ACK       |  | path      |  | ACK       |  |           |
    +-----------+  +-----------+  +-----------+  +-----------+
```

### 5.2 Internet Connectivity Detection

The relay daemon on each node monitors internet health through multiple signals:

```
Connectivity Health Score (0-100):

  MQTT broker connection alive         +40 points
  DNS resolution succeeds              +15 points
  HTTPS probe to known endpoint        +15 points
  Default gateway reachable (ping)     +15 points
  No packet loss in last 60 seconds    +15 points

  Score >= 70:  INTERNET_HEALTHY       -> Use MQTT path
  Score 30-69:  INTERNET_DEGRADED      -> Use MQTT with LoRa shadow copy for critical messages
  Score < 30:   INTERNET_DOWN          -> Switch to LoRa-only mode
```

**Probe frequency:** Every 10 seconds when healthy; every 3 seconds when degraded; every 10 seconds when down (to detect recovery without draining battery).

**Hysteresis:** Transition from HEALTHY to DEGRADED requires 3 consecutive degraded readings (30 seconds). Transition from DEGRADED to DOWN requires 3 consecutive down readings (9 seconds). Recovery from DOWN to HEALTHY requires 5 consecutive healthy readings (50 seconds). This prevents flapping.

### 5.3 LoRa Path: Message Adaptation

LoRa's severe bandwidth constraints (1.07 kbps at LONG_FAST) require significant message adaptation when falling back to radio:

**Maximum Meshtastic payload: ~228 bytes** (after headers, at maximum packet size).

When routing over LoRa:
1. **Text compression:** Messages are compressed using a custom dictionary-based scheme optimized for short text (Smaz or similar). Typical 2-3x compression for English text.
2. **Message truncation:** Messages exceeding the payload limit after compression are truncated with an indicator. The full message is queued for delivery when internet is restored.
3. **Priority filtering:** Only high-priority messages are sent over LoRa during congestion:
   - Priority 0 (CRITICAL): Emergency alerts -- always sent
   - Priority 1 (HIGH): Direct messages -- sent if airtime budget allows
   - Priority 2 (NORMAL): Channel messages -- sent with rate limiting
   - Priority 3 (LOW): Presence updates, telemetry -- suppressed over LoRa
4. **Airtime budgeting:** The relay daemon tracks LoRa airtime usage and enforces a budget of 5% duty cycle (half the EU regulatory limit of 10%) to leave headroom for Meshtastic's own mesh management traffic.

### 5.4 State Synchronization After Internet Recovery

When internet connectivity is restored after an outage:

```
Phase 1: RECONNECT (0-5 seconds)
  - Re-establish MQTT broker connection
  - Re-subscribe to all relevant topics
  - Publish current presence state for all connected users

Phase 2: SYNC (5-30 seconds)
  - Request presence state from all nodes (via MQTT retained messages)
  - Compare local routing table with network state
  - Identify messages received via LoRa that need internet confirmation

Phase 3: DRAIN (30 seconds - N minutes)
  - Flush queued outbound messages in priority order
  - Rate-limited to 50 messages/second to avoid broker overload
  - Deduplicate: messages already delivered via LoRa are marked;
    recipients' nodes ACK to prevent duplicate delivery

Phase 4: STEADY STATE
  - Resume normal internet-first routing
  - Continue LoRa monitoring for neighbor discovery
```

### 5.5 Split-Brain Resolution

Split-brain occurs when the internet partitions the relay network into two or more groups that cannot communicate via MQTT but may still have LoRa connectivity across partition boundaries.

**Detection:** A node notices that presence heartbeats from certain nodes have stopped (internet partition) but those nodes are still reachable via LoRa neighbor reports.

**Handling:**
1. Nodes on the LoRa-connected side of the partition elect a "bridge node" (the node with the best LoRa link to the other partition).
2. The bridge node forwards critical messages (Priority 0-1) between the internet-partitioned groups via LoRa.
3. Message IDs include a vector clock component so that when the internet partition heals, duplicate messages are suppressed.
4. Presence information exchanged via LoRa during split-brain is marked as "low confidence" and is overridden by internet-sourced presence upon reconnection.

---

## 6. User Identity and Presence

### 6.1 Identity Model

Users are not tied to a specific Meshtastic device. Instead, identity is based on cryptographic keypairs:

```
User Identity:
  - Private key: Ed25519 (stored on user's phone, never leaves device)
  - Public key: Ed25519 (shared with network)
  - User ID: SHA-256(public_key)[0:16] encoded as base32 (26 chars)
  - Display name: Human-readable, set by user, not unique
  - Avatar hash: Optional, for UI display
```

**Authentication flow when a user connects to a relay node:**

```
User Phone                          Relay Node
     |                                   |
     |-- Connect (WebSocket/BLE) ------->|
     |                                   |
     |<-- Challenge (32 random bytes) ---|
     |                                   |
     |-- Response:                       |
     |   { public_key,                   |
     |     signature(challenge),         |
     |     display_name }         ------>|
     |                                   |
     |   [Node verifies signature        |
     |    against public_key]            |
     |                                   |
     |<-- Session token (JWT, 24hr) -----|
     |                                   |
     |   [Node announces user presence   |
     |    to MQTT network]               |
```

**Key properties:**
- No registration server required -- users generate their own keypair
- Identity is portable across nodes -- connect to any node, same identity
- Contacts are identified by public key hash -- exchange keys via QR code or out-of-band
- Relay nodes cannot forge messages -- all DMs are end-to-end encrypted with recipient's public key

### 6.2 Contact Discovery

Users maintain a local contact list (public keys of their contacts). When connecting to a node:

1. The user's phone sends its contact list (as a list of user ID hashes) to the relay node.
2. The relay node checks the network-wide presence table for each contact.
3. The relay node returns the status of each contact (online/offline, last seen).
4. As contacts come online or go offline, the relay node pushes updates to the user in real time via the WebSocket connection.

**Privacy consideration:** The relay node learns which user IDs you are interested in. This is a tradeoff for usability. Mitigation: nodes could implement Private Information Retrieval (PIR) in a future version, or contacts could be resolved client-side by subscribing to presence topics directly.

### 6.3 Presence Propagation

```
Time -->

Node A              MQTT Broker           Node B              Node C
  |                      |                   |                   |
  |  User "alice"        |                   |                   |
  |  connects            |                   |                   |
  |                      |                   |                   |
  |--PUBLISH------------>|                   |                   |
  |  presence/region/    |                   |                   |
  |  nodeA/announce      |                   |                   |
  |  {alice, ONLINE,     |                   |                   |
  |   nodeA, sig}        |                   |                   |
  |  [RETAINED]          |                   |                   |
  |                      |--DELIVER--------->|                   |
  |                      |--DELIVER-------------------------------->|
  |                      |                   |                   |
  |                      |  Both nodes now   |                   |
  |                      |  know alice is    |                   |
  |                      |  at Node A        |                   |
  |                      |                   |                   |
  |--PUBLISH------------>|  (every 30s)      |                   |
  |  presence/region/    |                   |                   |
  |  nodeA/heartbeat     |                   |                   |
  |  {[alice], timestamp}|                   |                   |
```

---

## 7. Disaster Resilience

### 7.1 Graceful Degradation Tiers

```
TIER 0: FULL OPERATION
  Internet: UP    LoRa: UP    Power: MAINS
  - All features available
  - Internet-routed messages (low latency)
  - Full presence across all nodes
  - Web UI, firmware updates, monitoring

TIER 1: INTERNET DEGRADED
  Internet: FLAKY  LoRa: UP    Power: MAINS
  - Messages queued and retried
  - Critical messages shadow-copied to LoRa
  - Presence updates delayed but functional
  - Local node operation unaffected

TIER 2: INTERNET DOWN
  Internet: DOWN   LoRa: UP    Power: MAINS/BATTERY
  - All messages route over LoRa mesh
  - Presence limited to LoRa-reachable nodes
  - Message size constrained (228 bytes max)
  - Priority filtering active
  - Pre-cached routing tables used
  - Users see "Radio Only Mode" indicator

TIER 3: PARTIAL MESH
  Internet: DOWN   LoRa: PARTIAL   Power: BATTERY
  - Some nodes unreachable
  - Store-and-forward for unreachable nodes
  - Emergency alerts flood to all reachable nodes
  - Power conservation mode active

TIER 4: ISLAND MODE
  Internet: DOWN   LoRa: DOWN   Power: BATTERY
  - Node operates as standalone local hub
  - Users on same node can communicate
  - Messages queued for eventual delivery
  - Emergency beacon mode available
```

### 7.2 Pre-Positioned Data for Offline Operation

Each relay node maintains the following data locally for offline resilience:

| Data | Storage | Update Frequency | Purpose |
|------|---------|-----------------|---------|
| LoRa neighbor table | SQLite | Every 5 min | Direct radio routing |
| Node registry (all known nodes) | SQLite | Every 15 min (via MQTT) | Addressing and routing |
| User presence cache | SQLite | Every 30s (via MQTT) | Last-known user locations |
| Message queue (outbound) | SQLite | Real-time | Store-and-forward |
| Message queue (inbound, undelivered) | SQLite | Real-time | Delivery when user reconnects |
| Channel encryption keys | Encrypted file | On change | Channel message decryption |
| Emergency contact info | Encrypted file | On admin change | Critical contact routing |
| Geographic node map | SQLite | Daily | Location-based routing decisions |
| Firmware image (next version) | File | On release | Offline firmware update capability |

**Storage budget:** Approximately 50-200 MB per node, well within a 32GB SD card.

### 7.3 Power Architecture

```
POWER PRIORITY LEVELS:

  Level 1 - CRITICAL (never shut down):
    - Meshtastic radio (receives/relays LoRa packets)
    - Minimal relay daemon (message routing only)
    - Power: ~1W continuous

  Level 2 - IMPORTANT (shed after 4 hours on battery):
    - MQTT client (internet relay)
    - User session management
    - Power: ~2W additional

  Level 3 - NORMAL (shed after 1 hour on battery):
    - WiFi access point
    - Web UI server
    - Power: ~1.5W additional

  Level 4 - OPTIONAL (shed immediately on battery):
    - Monitoring/telemetry export
    - Firmware update checks
    - Log rotation and cleanup
    - Power: ~0.5W additional
```

**Power budget examples:**

| Configuration | Power Draw | 12Ah Battery Runtime | Solar Panel Needed |
|---------------|-----------|---------------------|-------------------|
| Full operation (Pi 4 + radio) | ~5W | ~29 hours | 20W panel |
| Internet relay only | ~3.5W | ~41 hours | 15W panel |
| Radio relay only (Pi idle, radio active) | ~1.5W | ~96 hours | 10W panel |
| Radio only (Pi off, radio on battery) | ~0.1W | ~1,440 hours (60 days) | 5W panel |

### 7.4 Emergency Alert System

During disasters, the system supports a special emergency mode:

1. **Emergency alert origination:** Any user can send an emergency alert. The relay node immediately:
   - Publishes to MQTT `mesh-relay/v1/messages/{region}/broadcast/emergency` (if internet available)
   - Broadcasts on LoRa mesh on all configured channels
   - Stores locally for delivery to any user who connects within the TTL

2. **Emergency alert propagation:** All relay nodes that receive an emergency alert:
   - Display it to all connected users immediately
   - Rebroadcast on LoRa if received via internet (and vice versa) to maximize reach
   - Cache the alert for 72 hours

3. **Emergency beacon mode:** If power is critically low (< 10% battery), a relay node can enter beacon mode:
   - Shuts down the Pi
   - The Meshtastic radio periodically broadcasts a pre-configured emergency message with GPS coordinates
   - Power consumption drops to ~0.1W, extending runtime to weeks on battery

---

## 8. Scalability Analysis

### 8.1 Users Per Node

The relay node (Raspberry Pi) serves users over WiFi or through the venue's existing network.

| Resource | Limit | Constraint |
|----------|-------|------------|
| WiFi AP clients (hostapd) | 10-20 simultaneous | WiFi chipset and channel capacity |
| WebSocket connections (relay daemon) | 200+ | RAM (~1KB per connection state) |
| Active message sessions | 50-100 | CPU for encryption/decryption |
| LoRa message throughput | 1-2 messages/minute | Airtime budget (5% duty cycle at LONG_FAST) |

**Practical limit: 20-50 active users per node** in normal operation. The bottleneck is not the Pi (which can handle hundreds of WebSocket connections) but the LoRa airtime budget if many users are sending messages simultaneously and the internet is down.

**With internet available:** The Pi can serve 100+ users because messages route over MQTT, not LoRa. LoRa is only used for mesh management and fallback.

### 8.2 Network Scale

| Scale Tier | Nodes | Users | Internet Bandwidth | MQTT Broker |
|------------|-------|-------|-------------------|-------------|
| Neighborhood | 5-20 | 50-200 | Single broker (Mosquitto) on any VPS | 1 vCPU, 512MB RAM |
| City district | 20-100 | 200-2,000 | 3-node EMQX cluster | 3x 2 vCPU, 4GB RAM |
| Metropolitan | 100-500 | 2,000-10,000 | 5-node EMQX cluster with regional sharding | 5x 4 vCPU, 8GB RAM |
| Regional | 500-2,000 | 10,000-50,000 | Federated MQTT brokers per metro area | Multiple clusters |

### 8.3 LoRa Bandwidth Budget

At LONG_FAST (1.07 kbps, SF11, 250kHz BW):

```
Packet overhead (Meshtastic headers): ~40 bytes
Maximum payload: ~228 bytes
Total packet size: ~268 bytes
Time on air per packet: ~2 seconds (at SF11)

With 5% duty cycle budget:
  Available airtime per hour: 180 seconds
  Packets per hour: ~90
  Packets per minute: ~1.5

With 10% duty cycle (EU max):
  Available airtime per hour: 360 seconds
  Packets per hour: ~180
  Packets per minute: ~3
```

**This means a single relay node can send/receive approximately 1-3 LoRa messages per minute.** This is the fundamental constraint that makes the internet path essential for any multi-user relay system. The LoRa path is reserved for:
- Emergency alerts
- Critical direct messages when internet is unavailable
- Mesh management (neighbor discovery, routing updates)
- Last-resort communication

### 8.4 Internet Bandwidth Requirements

Per relay node:

| Traffic Type | Rate | Bandwidth |
|--------------|------|-----------|
| MQTT keepalive | 1 packet/30s | ~100 bytes/min |
| Presence heartbeat | 1 message/30s | ~200 bytes/min |
| Presence subscriptions (50 contacts) | Variable | ~500 bytes/min |
| Text messages (active user) | ~5 messages/min peak | ~2 KB/min |
| Routing updates | 1/5min | ~500 bytes/5min |
| **Total per node (typical)** | | **~5-20 KB/min (~3-12 kbps)** |

This is negligible even on poor internet connections. A 3G connection (1 Mbps) could support hundreds of relay nodes. WiFi and Ethernet are more than adequate.

---

## 9. Security Architecture

### 9.1 Threat Model

| Threat | Attack Vector | Mitigation |
|--------|--------------|------------|
| Message interception at relay node | Node operator reads messages in transit | End-to-end encryption (NaCl box for DMs, AES256-GCM for channels with per-channel keys) -- relay node never has plaintext |
| Node impersonation | Attacker deploys rogue relay node | Nodes authenticate to MQTT broker with TLS client certificates; node registry maintained by network operators |
| User impersonation | Attacker claims another user's identity | Ed25519 challenge-response authentication; identity is the public key |
| MQTT broker compromise | Attacker gains access to broker | Messages are E2E encrypted; broker only sees ciphertext and metadata (topics, timing) |
| Replay attacks | Attacker replays captured messages | Message IDs include timestamp and nonce; recipients reject messages older than 5 minutes or with seen nonces |
| Denial of service | Flood of messages to a node or broker | Rate limiting at relay daemon (per-user); rate limiting at MQTT broker (per-node); LoRa airtime budget enforced |
| Physical node compromise | Attacker gains physical access to relay node hardware | Disk encryption on Pi SD card; secure boot where supported; tamper detection alerts; node certificates can be revoked remotely |
| Traffic analysis | Observer determines who is communicating | Partial mitigation: cover traffic (dummy messages at constant rate); topic structure uses hashes not plaintext names |

### 9.2 Encryption Layers

```
Layer 1: Transport Encryption
  - MQTT over TLS 1.3 (relay node <-> broker)
  - WSS (WebSocket Secure) for user <-> relay node
  - WireGuard VPN as optional additional layer for node <-> broker

Layer 2: Message Encryption (End-to-End)
  - Direct messages: X25519 key agreement + XSalsa20-Poly1305 (NaCl crypto_box)
  - Channel messages: AES-256-GCM with channel-specific key derived from channel PSK
  - Emergency broadcasts: Signed but unencrypted (must be readable by all)

Layer 3: Authentication
  - User identity: Ed25519 signatures on all messages
  - Node identity: TLS client certificates (X.509)
  - Message integrity: Poly1305 MAC (included in NaCl box) or GCM tag

Layer 4: LoRa Encryption
  - Meshtastic's built-in AES256-CTR channel encryption
  - PKC for direct messages (Meshtastic v2.5+)
  - Relay-layer encryption is applied BEFORE Meshtastic encryption when
    tunneling relay messages over LoRa (defense in depth)
```

### 9.3 Key Management

```
Key Type               Generation        Storage              Rotation
---------------------------------------------------------------------------
User Ed25519 keypair   On first app use  User's device only   Manual (new identity)
User X25519 keypair    Derived from Ed25519  Device keychain  With Ed25519
Channel PSK            Admin-generated   All member devices   Quarterly recommended
Node TLS certificate   CA-signed         Node's encrypted     Annual, auto-renewal
                                         storage              via ACME
MQTT broker TLS cert   CA-signed         Broker config        Annual, auto-renewal
Session JWT            On user connect   Node memory only     24-hour expiry
```

---

## 10. Risk Analysis

### 10.1 Technical Risks

| Risk | Severity | Likelihood | Mitigation |
|------|----------|------------|------------|
| LoRa bandwidth insufficient for multi-user relay | HIGH | CERTAIN | Design internet as primary path; LoRa is emergency fallback only; strict airtime budgeting; message prioritization |
| ESP32 single-connection limit prevents direct user access | HIGH | CERTAIN | Companion Pi architecture; ESP32 used only as LoRa radio |
| MQTT broker becomes single point of failure | MEDIUM | LOW | Clustered broker deployment; local operation continues during broker outage; LoRa fallback |
| Message ordering inconsistency during path switching | MEDIUM | MEDIUM | Vector clocks on messages; sequence numbers per channel; deduplication at receiver |
| Clock drift between nodes causes message ordering issues | LOW | MEDIUM | NTP synchronization when internet available; LoRa-based time sync protocol as fallback; timestamps are advisory not authoritative |
| Meshtastic firmware updates break serial API compatibility | MEDIUM | LOW | Pin firmware version per deployment; test updates on canary nodes first; abstraction layer in relay daemon |
| WiFi interference at dense deployments | LOW | MEDIUM | 5GHz preferred; channel selection; Ethernet uplink instead of WiFi for internet |

### 10.2 Security Risks

| Risk | Severity | Likelihood | Mitigation |
|------|----------|------------|------------|
| Compromised relay node reads message metadata | MEDIUM | MEDIUM | Cover traffic; topic obfuscation; accept as inherent tradeoff of relay architecture |
| Rogue relay node performs downgrade attack | HIGH | LOW | Certificate pinning; node allowlist; anomaly detection in relay daemon |
| Channel PSK leak exposes all channel history | HIGH | LOW | Key rotation; forward secrecy planned for future (ratcheting protocol); per-message keys |
| Harvest-now-decrypt-later attack on LoRa traffic | MEDIUM | MEDIUM | Additional relay-layer encryption on top of Meshtastic encryption; plan migration to post-quantum algorithms |
| Physical theft of relay node exposes keys | MEDIUM | MEDIUM | Full-disk encryption; TPM-based key storage where available; remote revocation |

### 10.3 Operational Risks

| Risk | Severity | Likelihood | Mitigation |
|------|----------|------------|------------|
| Node firmware/software update requires physical access | MEDIUM | HIGH | OTA update system via MQTT admin channel; automatic rollback on boot failure |
| Power failure at multiple nodes simultaneously | HIGH | LOW (localized) | Battery backup; solar; geographically distributed nodes; graceful shutdown preserves state |
| SD card corruption on Raspberry Pi | MEDIUM | MEDIUM | Read-only root filesystem (OverlayFS); wear-leveling; daily SQLite backups to second partition; industrial-grade SD cards |
| Volunteer node operators lose interest | MEDIUM | HIGH | Minimize maintenance burden; auto-updates; remote monitoring; gamification of node operation (uptime badges) |
| Network grows beyond MQTT broker capacity | LOW | LOW | Horizontal scaling via EMQX cluster; regional federation; capacity planning alerts |

### 10.4 Regulatory Risks

| Risk | Severity | Likelihood | Mitigation |
|------|----------|------------|------------|
| EU 10% duty cycle limits LoRa throughput severely | HIGH | CERTAIN (EU) | Strict airtime budget enforcement in relay daemon; internet as primary path; SHORT_FAST preset in EU where range permits |
| ISM band interference from other devices | MEDIUM | MEDIUM | Listen-before-talk where supported; frequency hopping; SNR monitoring; adaptive spreading factor |
| Local regulations on public WiFi access points | LOW | LOW | Comply with local telecoms regulations; terms of service on captive portal; no open relay |
| LoRa licensing varies by country | LOW | LOW | Region-locked firmware (already handled by Meshtastic); document per-country requirements |
| Data protection regulations (GDPR, etc.) | MEDIUM | MEDIUM | Minimal data collection; no persistent user tracking; E2E encryption means relay nodes don't process personal data content; privacy policy |

---

## 11. Deployment Topology

### 11.1 Urban Deployment Example

```
                           INTERNET
                              |
                    +---------+---------+
                    |   MQTT Broker     |
                    |   Cluster (Cloud) |
                    +---------+---------+
                              |
            +---------+-------+-------+---------+
            |         |               |         |
         [Node A]  [Node B]       [Node C]  [Node D]
         Home       Office        Library    Park
         (Ethernet) (Ethernet)    (WiFi)     (Solar+4G)
            |         |               |         |
           LoRa      LoRa            LoRa      LoRa
            |         |               |         |
            +---------+               +---------+
            |    LoRa mesh link       |
            +-------------------------+
              (2-5 km typical range)

  Users connect to nearest node via:
  - Venue WiFi + mDNS discovery
  - Node's own WiFi AP (captive portal)
  - BLE (within ~30m, one user only via Meshtastic)
```

### 11.2 Disaster Scenario: Internet Down

```
                           INTERNET
                              X  (DOWN)

            +---------+-------X-------+---------+
            |         |               |         |
         [Node A]  [Node B]       [Node C]  [Node D]
         Home       Office        Library    Park
         (BATTERY)  (BATTERY)     (BATTERY)  (SOLAR)
            |         |               |         |
           LoRa      LoRa            LoRa      LoRa
            |         |               |         |
            +---------+               +---------+
            |    LoRa mesh link       |
            +-------------------------+

  - Nodes A & B form a LoRa cluster (can relay for each other)
  - Nodes C & D form another LoRa cluster
  - A-B cluster and C-D cluster can communicate if within LoRa range
  - Each cluster operates independently if not in range
  - Messages queued for cross-cluster delivery when internet returns
  - Emergency alerts flood all reachable nodes via LoRa
```

### 11.3 Node Discovery and Onboarding

For users discovering and connecting to relay nodes:

1. **mDNS/DNS-SD:** Relay nodes advertise `_meshrelay._tcp.local` service. Phones on the same WiFi network discover nodes automatically.
2. **Captive portal:** Nodes running their own WiFi AP present a captive portal with the relay web UI.
3. **QR code:** Physical QR codes posted at node locations encode the node's WiFi credentials and relay endpoint URL.
4. **Bluetooth beacon:** Nodes broadcast a BLE advertisement with a custom service UUID for the relay app to discover.
5. **Static directory:** A web-based node directory (hosted independently) lists known relay nodes by location for pre-trip planning.

---

## 12. Technology Stack

### 12.1 Relay Daemon (runs on Raspberry Pi)

| Layer | Technology | Rationale |
|-------|-----------|-----------|
| Language | **Go** | Excellent concurrency (goroutines/channels); fast compilation; single-binary deployment; trivial ARM cross-compilation (`GOARCH=arm`); strong stdlib |
| MQTT client | **eclipse/paho.mqtt.golang** | Mature, callback-based MQTT 3.1.1/5.0 client |
| WebSocket server | **gorilla/websocket** | De facto standard Go WebSocket library; read/write pump pattern |
| Serial communication | **go.bug.st/serial** | Meshtastic radio PROTO API over USB serial |
| Protobuf | **buf.build generated** (meshtastic/protobufs) | Pre-generated Go types from upstream Meshtastic protobuf definitions |
| Database | **BoltDB** (**bbolt**) | Embedded key-value store; no CGo dependency; safe for SD cards; single-file database |
| Encryption | **crypto/ed25519** + **golang.org/x/crypto/nacl** | Ed25519 identity, X25519 ECDH, NaCl crypto_box (XSalsa20-Poly1305), HKDF-SHA256 |
| WiFi AP | **hostapd** + **dnsmasq** | Standard Linux WiFi AP stack |
| Service discovery | **libmdns** or **avahi** | mDNS/DNS-SD advertisement |
| Process management | **systemd** | Auto-restart, logging, dependency management |
| OTA updates | **mender.io** or custom A/B partition scheme | Atomic updates with rollback |

### 12.2 User-Facing Client

| Component | Technology | Rationale |
|-----------|-----------|-----------|
| Primary client | **Progressive Web App (PWA)** | No app store required; works on any device with a browser; served directly by the relay node |
| Framework | **SolidJS** or **Preact** | Lightweight; fast initial load; minimal JS bundle for constrained connections |
| Transport | **WebSocket** to relay node | Real-time bidirectional messaging |
| Encryption | **TweetNaCl.js** | Client-side E2E encryption in browser |
| Key storage | **Web Crypto API** + **IndexedDB** | Non-extractable keys in browser's secure storage |
| Offline support | **Service Worker** | Cache app shell for offline access; queue messages |
| Optional native app | **React Native** or **Flutter** | For users who want push notifications, background operation |

### 12.3 Infrastructure

| Component | Technology | Rationale |
|-----------|-----------|-----------|
| MQTT broker | **EMQX** (production) / **Mosquitto** (small scale) | Clustered, high-performance, open-source |
| Monitoring | **Prometheus** + **Grafana** | Node metrics, broker metrics, message latency |
| Alerting | **Alertmanager** | Node offline, battery low, connectivity loss |
| Log aggregation | **Loki** | Lightweight, integrates with Grafana |
| Infrastructure as Code | **Ansible** | Node provisioning and configuration management |
| CI/CD | **GitHub Actions** | Firmware builds, relay daemon cross-compilation |
| Node OS | **Raspberry Pi OS Lite** (Debian-based) with **OverlayFS** | Read-only root for reliability; writable data partition |

---

## 13. Implementation Phases

### Phase 1: Foundation -- COMPLETE

**Goal:** Single relay node working end-to-end with internet path.

- [x] Ed25519 identity system with keypair generation, challenge-response auth
- [x] NaCl crypto_box E2E encryption (XSalsa20-Poly1305), X25519 ECDH, HKDF-SHA256
- [x] MQTT transport with source hash preservation (16-byte header prepended to payload)
- [x] WebSocket server with JSON protocol (challenge, auth_response, auth_ok, message, presence, heartbeat)
- [x] BoltDB store-and-forward with enqueue, dequeue, delivery tracking, expiry cleanup
- [x] Two-node POC verified with E2E tests (TestE2E_TwoNodeMessageRelay, TestE2E_StoreAndForward)
- [x] Interactive CLI client (relay-cli) with identity management and WebSocket auth flow
- [x] Makefile with build, test, lint, cross-compile (ARM/ARM64), POC targets

**Deliverables:** Two-node relay system with internet-routed messaging. **DONE.**

### Phase 2: LoRa Fallback & Gateway Bridge -- COMPLETE

**Goal:** Dual-path routing with Meshtastic radio integration.

- [x] Meshtastic serial transport (meshradio) with protobuf support via buf.build generated types
- [x] Wake signal (32 bytes 0xC3), config handshake (want_config_id / config_complete_id)
- [x] FromRadio decoding: Packet, MyInfo, ConfigCompleteId, NodeInfo, Rebooted, LogRecord
- [x] Device reboot detection and auto-reconfiguration
- [x] Priority-ordered router with health-score-based failover (threshold 20)
- [x] LoRa gateway bridge: Meshtastic node numbers <-> relay identity hashes
- [x] Device registry with multi-device per user support
- [x] Active device detection via last-heard timestamps (configurable timeout, default 5 min)
- [x] @hash message format for addressing relay users from Meshtastic devices
- [x] Packet deduplication (TTL-based, key: sender_node_num << 32 | packet_id)
- [x] Bridge integration in node daemon with inbound interceptor and bridge delivery func
- [x] 40 bridge tests + 26 identity tests passing

**Deliverables:** Bidirectional LoRa/internet messaging with portable Meshtastic devices. **DONE.**

### Phase 3: Presence, Routing, and Multi-Device -- IN PROGRESS

**Goal:** Full presence system with multi-device support and user discovery.

- [x] Presence table with TTL expiry, fresher-wins semantics, replay prevention
- [x] Presence state machine (WebSocket > LoRa > Offline)
- [x] Multi-device registration with active device selection
- [ ] Presence propagation via MQTT retained messages
- [ ] Contact list management and user discovery
- [ ] Bridge CLI commands (/bridge-register, /bridge-list, /bridge-status)
- [ ] Persistent device registrations in BoltDB
- [ ] E2E test for bridge path (simulated, no physical LoRa in CI)

### Phase 4: Resilience and Security (Planned)

**Goal:** Production-ready security and disaster resilience.

- [ ] TLS for MQTT broker connections
- [ ] Power management tiers
- [ ] Emergency alert system
- [ ] OTA update mechanism
- [ ] Deploy EMQX cluster (3 nodes)
- [ ] Security audit of relay daemon

### Phase 5: Scale and Polish (Planned)

**Goal:** Support for large deployments with operational tooling.

- [ ] Regional MQTT topic sharding
- [ ] Node management dashboard (admin web UI)
- [ ] Monitoring and alerting (Prometheus/Grafana)
- [ ] Node provisioning automation (Ansible)
- [ ] Deployment guides (indoor, outdoor, solar)
- [ ] Community beta testing

---

## 14. LoRa Gateway Bridge

The LoRa Gateway Bridge enables users carrying portable Meshtastic radios (with no internet) to send and receive messages through the relay network. It translates between the Meshtastic mesh (32-bit node numbers, plain text) and the relay identity network (Ed25519 hashes, structured messages).

### 14.1 Problem

Meshtastic devices identify nodes by 32-bit hardware-derived numbers, while the relay network uses 16-byte Ed25519 public key hashes. A user carrying a Meshtastic radio cannot natively address relay-network users. The bridge solves this by maintaining a mapping between the two identity systems.

### 14.2 Architecture

```
                        RELAY NODE (RPi)
                  +---------------------------+
                  |                            |
  Meshtastic      |   +------------------+    |    MQTT / Internet
  Radio (USB) ----+-->| meshradio        |    |    Transport
                  |   | (serial proto)   |    |
                  |   +--------+---------+    |
                  |            |              |
                  |            v              |
                  |   +------------------+    |
                  |   | Inbound          |    |
                  |   | Interceptor      |    |
                  |   +--------+---------+    |
                  |            |              |
                  |            v              |
                  |   +------------------+    |
                  |   | Bridge           |    |
                  |   | .ProcessInbound()|    |
                  |   +--------+---------+    |
                  |            |              |
                  |    Remapped Packet        |
                  |    (relay identity)       |
                  |            |              |
                  |            v              |
                  |   +------------------+    |
                  |   | Node             |    |    +---> WebSocket user
                  |   | .handlePacket()  +----+--->|
                  |   |                  |    |    +---> MQTT (other nodes)
                  |   +------------------+    |    +---> Store (offline)
                  +---------------------------+
```

### 14.3 Inbound Flow (LoRa -> Relay Network)

When a Meshtastic device sends a text message in the format `@<hash> <message>`:

1. **meshradio** receives the FromRadio protobuf, decodes TEXT_MESSAGE_APP, populates `Packet.MeshNodeNum` and `Packet.MeshPacketID`.
2. **Inbound Interceptor** checks if `pkt.Via == "meshradio" && pkt.MeshNodeNum != 0` and delegates to `bridge.ProcessInbound()`.
3. **Bridge.ProcessInbound()** performs:
   - **Registration check**: Verifies the source node number is a registered device.
   - **Deduplication**: Checks `(nodeNum, packetID)` against TTL cache. Meshtastic flooding causes duplicate packets; the bridge drops them.
   - **Format parsing**: Extracts destination hash and message body from `@<hash> <text>`.
   - **Identity remapping**: Replaces MeshNodeNum source with the registered user's relay identity hash.
   - **Last-heard update**: Marks the device as recently active.
4. The node routes the remapped packet normally (WebSocket delivery, MQTT relay, or store-and-forward).

### 14.4 Outbound Flow (Relay Network -> LoRa)

When a message arrives for a user who is reachable via LoRa (not WebSocket):

1. **Node.handlePacket()** finds the user is not connected via WebSocket.
2. Calls **BridgeDeliveryFunc** which invokes `bridge.FormatOutbound()`.
3. **Bridge.FormatOutbound()** looks up the user's **active device** (most recently heard within timeout).
4. Formats the message as `[<short_sender_hash>] <text>` (truncated to 200 chars for LoRa MTU).
5. Calls **meshradio.SendText()** to send a targeted unicast to the specific Meshtastic node number.

### 14.5 Message Format

**Inbound (Meshtastic -> Relay):**
```
@a1b2c3d4e5f6 Hello from the trail!
 ^             ^
 |             message body
 destination hash (12-char short or 32-char full hex)
```

**Outbound (Relay -> Meshtastic):**
```
[a1b2c3d4e5f6] Hey! Got your message.
 ^              ^
 |              message body (truncated to 200 chars)
 sender's short hash (12 hex chars = 6 bytes)
```

Short hashes (12 hex chars / 6 bytes) are used for display compactness on small Meshtastic screens. The bridge resolves short hashes against the presence table, with ambiguity detection (error if multiple identities share a prefix).

### 14.6 Deduplication

Meshtastic's "managed flooding" routing means a single packet may arrive multiple times at the relay node (via different mesh paths). The bridge maintains a TTL-based dedup cache keyed by `(sender_node_num << 32) | packet_id`. The default dedup window matches the Meshtastic mesh packet lifetime.

### 14.7 Activity Tracking

The meshradio transport reports not just TEXT_MESSAGE_APP packets but also POSITION, TELEMETRY, and NODEINFO app packets as "activity". This means a Meshtastic device that is powered on and periodically broadcasting its position is considered "active" even if the user hasn't sent a text message recently. This is important for the active device selection logic described in Section 15.

---

## 15. Multi-Device Registration

Users can register multiple Meshtastic devices to their relay identity. The system tracks which device is currently active and routes outbound messages to the right one.

### 15.1 Device Registry

The registry maintains a bidirectional mapping:

```
byNodeNum:  map[uint32]*DeviceState      // Meshtastic node number -> state
byUser:     map[identity.Hash][]*DeviceState  // User hash -> all devices
```

Each `DeviceState` tracks:
- Static registration: `MeshNodeNum`, `UserHash`, `Label`, `RegisteredAt`
- Dynamic state: `LastHeard`, `LastSeenByNode`, `LastPacketID`

### 15.2 Registration Sources

Currently, devices are registered via YAML configuration:

```yaml
bridge:
  enabled: true
  active_timeout: 5m
  dedup_window: 2m
  devices:
    - node_num: "0x1A2B3C4D"
      user_hash: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4"
      label: "hiker-radio-1"
    - node_num: "0x5E6F7A8B"
      user_hash: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4"   # same user, second device
      label: "car-radio"
```

**Planned**: CLI commands (`/bridge-register`, `/bridge-unregister`) and BoltDB persistence for runtime registration without config file changes.

### 15.3 Active Device Selection

When the relay needs to deliver a message to a user via LoRa, it must pick which of their registered devices to target. The selection algorithm:

```
ActiveDeviceForUser(userHash):
    devices = byUser[userHash]
    cutoff = now - activeTimeout       // default: 5 minutes

    best = nil
    for each device in devices:
        if device.LastHeard < cutoff:
            skip                       // device is stale
        if best == nil OR device.LastHeard > best.LastHeard:
            best = device

    return best                        // nil if no active devices
```

**Key properties:**
- **Most-recently-heard wins**: If a user has two active devices, the one heard most recently gets the message. This naturally tracks which device is physically closer to a relay node.
- **Configurable timeout**: The `active_timeout` (default 5 min) determines how long a silent device remains eligible. Meshtastic devices typically broadcast position every 15-120 seconds, so 5 minutes is generous.
- **No BLE required**: Device activity is detected from any radio traffic (text, position, telemetry, nodeinfo) -- the user does not need a BLE connection to the RPi.

### 15.4 Example Scenario

```
Time    Event                                       Active Device
─────   ──────────────────────────────────────────   ──────────────
09:00   User leaves home with hiker-radio-1          hiker-radio-1
09:01   hiker-radio-1 sends position beacon          hiker-radio-1 (LastHeard=09:01)
09:15   car-radio at home sends position beacon      hiker-radio-1 (09:01 > cutoff)
09:30   hiker-radio-1 sends position beacon          hiker-radio-1 (LastHeard=09:30)
10:00   hiker-radio-1 goes out of range              hiker-radio-1 (still within 5m)
10:06   activeTimeout expires for hiker-radio-1      car-radio (if heard) or nil
10:10   User returns home, hiker-radio-1 beacons     hiker-radio-1 (LastHeard=10:10)
```

---

## 16. Presence State Machine

The presence system determines the best delivery path for each user based on their current connection state.

### 16.1 Presence Modes

| Mode | Meaning | Delivery Path | Bandwidth | Latency |
|------|---------|---------------|-----------|---------|
| **websocket** | User has active WebSocket session to a relay node | WebSocket push via the connected node | High | <100ms |
| **lora** | User has an active registered Meshtastic device on the mesh | LoRa bridge via meshradio.SendText() | Low (~1 kbps) | 1-10s |
| **offline** | No active session or device | Store-and-forward (BoltDB queue) | N/A | Until reconnect |

### 16.2 Priority Resolution

```
UserPresenceMode(userHash, wsConnected):
    if wsConnected:
        return "websocket"           // highest priority

    if registry.ActiveDeviceForUser(userHash) != nil:
        return "lora"                // medium priority

    return "offline"                 // lowest priority
```

WebSocket always takes precedence because it offers:
- Higher bandwidth (no 228-byte packet limit)
- Lower latency (direct TCP vs mesh flooding)
- Bidirectional flow control
- No LoRa airtime consumption

### 16.3 Delivery Cascade in Node.handlePacket()

```
incoming packet for destHash:
    |
    +-- Is user connected via WebSocket to THIS node?
    |   YES -> deliverLocal(userHash, packet)        [WebSocket push]
    |
    +-- Is bridge delivery func configured?
    |   YES -> deliverBridge(destHash, senderHash, data)
    |          Bridge looks up active LoRa device
    |          |
    |          +-- Active device found?
    |          |   YES -> meshradio.SendText(nodeNum, formatted)
    |          |   NO  -> return error
    |          |
    |          +-- Bridge delivery failed?
    |              YES -> fall through to store
    |
    +-- Store for later delivery
        store.Enqueue(destHash, data)
```

### 16.4 State Transitions

```
                    +-- User connects via WebSocket
                    |
         +----------v-----------+
         |     WEBSOCKET        |<---+
         |  (highest priority)  |    |
         +----------+-----------+    |
                    |                |
    User disconnects from WS        | User reconnects
                    |                |
         +----------v-----------+    |
         |  Device still active |    |
         |  on LoRa mesh?       |    |
         +----------+-----------+    |
          /YES               \NO    |
         v                    v     |
+--------+--------+  +-------+-----+--+
|      LORA       |  |    OFFLINE     |
| (bridge active) |  | (store & fwd)  |
+--------+--------+  +-------+--------+
         |                    |
         |  Device goes       |  User reconnects
         |  silent > timeout  |  (WS or LoRa)
         |                    |
         +-------->OFFLINE<---+
                    |
                    +-------->WEBSOCKET (reconnect)
                    +-------->LORA (device heard)
```

### 16.5 Cross-Node Presence

Presence state is propagated across relay nodes via MQTT. When a user connects to Node A:

1. Node A publishes a presence announcement on `relay/{region}/presence/{user_hash}` (retained message).
2. Node B receives the announcement, updates its presence table.
3. When a message arrives at Node B for that user, Node B routes it via MQTT to Node A.
4. Node A delivers locally via WebSocket.

For LoRa presence, the bridge at each node tracks device activity. If a message arrives at Node B for a user whose device is heard by Node A's radio, Node B routes via MQTT to Node A, which delivers via the LoRa bridge.

---

## Appendix A: Meshtastic Platform Research Summary

### Current MQTT Bridge Capabilities (as of 2026)

Meshtastic firmware includes a built-in MQTT client that can bridge mesh traffic to an MQTT broker. Key findings from research:

- **Topic structure:** `msh/REGION/2/e/CHANNELNAME/USERID` (protobuf) and `msh/REGION/2/json/CHANNELNAME/USERID` (JSON, ESP32 only)
- **Default broker:** `mqtt.meshtastic.org` with zero-hop policy (packets don't propagate beyond directly connected nodes)
- **Private brokers:** Supported; removes zero-hop restriction; must change default PSK
- **Encryption:** Payload remains encrypted with channel key on MQTT; headers are in cleartext
- **JSON support:** Available on ESP32 only (not nRF52); supports TEXT_MESSAGE, TELEMETRY, NODEINFO, POSITION, WAYPOINT, NEIGHBORINFO, TRACEROUTE, DETECTION_SENSOR, PAXCOUNTER, REMOTE_HARDWARE message types
- **Downlink:** Can send messages from MQTT into the mesh via gateway nodes
- **Limitations:** The built-in MQTT bridge is designed for extending mesh coverage, not for multi-user relay; it forwards raw mesh packets, not user-level messages

### Why We Build a Separate Relay Layer

The existing MQTT bridge is insufficient for the relay use case because:

1. **Single user per device:** Cannot serve multiple users from one Meshtastic node
2. **No user identity system:** Meshtastic identifies nodes, not users
3. **No presence system:** No mechanism to track which user is at which node
4. **No message routing:** MQTT bridge forwards all mesh traffic; no per-user routing
5. **No E2E encryption at relay level:** Channel encryption uses shared PSK; no per-user encryption for relay purposes
6. **No graceful degradation logic:** No automatic path selection or fallback

The relay layer sits above Meshtastic, using it as a LoRa transport while adding the user-centric features needed for a multi-user relay network.

### LoRa Modem Presets (Data Rates)

| Preset | Data Rate | Spreading Factor | Bandwidth | Coding Rate | Link Budget |
|--------|-----------|------------------|-----------|-------------|-------------|
| Short Turbo | 21.88 kbps | SF7 | 500 kHz | 4/5 | 140 dB |
| Short Fast | 10.94 kbps | SF7 | 250 kHz | 4/5 | 143 dB |
| Short Slow | 6.25 kbps | SF8 | 250 kHz | 4/5 | 145.5 dB |
| Medium Fast | 3.52 kbps | SF9 | 250 kHz | 4/5 | 148 dB |
| Medium Slow | 1.95 kbps | SF10 | 250 kHz | 4/5 | 150.5 dB |
| Long Turbo | 1.34 kbps | SF11 | 500 kHz | 4/8 | 150 dB |
| Long Fast (default) | 1.07 kbps | SF11 | 250 kHz | 4/5 | 153 dB |
| Long Moderate | 0.34 kbps | SF11 | 125 kHz | 4/8 | 156 dB |

### Mesh Routing Algorithm

Meshtastic uses "managed flooding" with SNR-based contention:
- Nodes with weaker received signal (farther away) rebroadcast sooner
- ROUTER/REPEATER roles get priority rebroadcast
- Since firmware v2.6, direct messages use next-hop routing (tracks relaying node for subsequent attempts)
- Default max hops: 3 (configurable up to 7)
- Node traffic scales with interval formula: `ScaledInterval = Interval * (1.0 + ((NumberOfOnlineNodes - 40) * 0.075))`
- Approximately 30 packets stored in device memory

### Encryption

- **Channel encryption:** AES256-CTR with pre-shared key per channel
- **Direct messages:** Ed25519/Curve25519 public key cryptography (firmware v2.5+)
- **Limitations:** No perfect forward secrecy; no integrity verification on channel messages; node identity based on MAC address (spoofable)

---

## Appendix B: Decision Log

| Decision | Options Considered | Choice | Date | Rationale |
|----------|-------------------|--------|------|-----------|
| Node hardware | ESP32 standalone / Pi + radio / Custom PCB | Pi + radio | 2026-03-12 | Single-user limit on Meshtastic devices requires companion computer for multi-user support |
| Relay protocol | MQTT / WebSocket / gRPC / Custom UDP | MQTT | 2026-03-12 | Pub/sub model matches broadcast mesh; existing Meshtastic MQTT support; proven at scale; low overhead |
| Relay language | Rust / Python / Go / Node.js | **Go** | 2026-03-12 | Goroutines for concurrency; single-binary deployment; trivial ARM cross-compilation; strong stdlib; fast compilation |
| User identity | Hardware MAC / Username+password / Keypair | Ed25519 keypair | 2026-03-12 | Portable, self-sovereign, enables E2E encryption, no registration server needed |
| Client platform | Native mobile app / PWA / Both | CLI (phase 1), PWA planned | 2026-03-12 | CLI validates protocol; PWA for production; no app store gatekeeping |
| Presence system | Central server / DHT / MQTT retained messages | MQTT retained messages + heartbeats | 2026-03-12 | No additional infrastructure; leverages existing MQTT broker; eventual consistency acceptable |
| Database | PostgreSQL / SQLite / BoltDB / LevelDB | **BoltDB (bbolt)** | 2026-03-12 | Embedded, no CGo, single-file, safe on SD cards, adequate for node-scale data |
| MQTT broker | Mosquitto / EMQX / HiveMQ / VerneMQ | EMQX (production), Mosquitto (small) | 2026-03-12 | EMQX: native clustering, high connection count, rule engine; Mosquitto: simple for small deployments |
| Bridge message format | Custom binary / Protobuf / Plain text @hash | **@hash text** | 2026-03-13 | Human-readable on stock Meshtastic devices; no firmware modification needed; easy to type |
| Active device detection | BLE proximity / GPS geofence / Last-heard timeout | **Last-heard timeout** | 2026-03-13 | Works without BLE connection to RPi; any radio activity (position, telemetry) counts as "heard" |
| Presence priority | Equal weight / Configurable / Fixed hierarchy | **WebSocket > LoRa > Offline** | 2026-03-13 | WebSocket = higher bandwidth + lower latency; LoRa = reachable but constrained; clear, predictable |
