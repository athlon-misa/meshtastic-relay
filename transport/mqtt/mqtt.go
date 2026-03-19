// Package mqtt implements the MQTT transport for internet-based message relay.
//
// Topic structure:
//
//	relay/{region}/presence/{user_hash}  - Presence announcements (retained)
//	relay/{region}/msg/{user_hash}       - Messages to a specific user
//	relay/{region}/node/{node_id}        - Node-to-node control messages
package mqtt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/meshtastic-relay/relay/config"
	"github.com/meshtastic-relay/relay/identity"
	"github.com/meshtastic-relay/relay/transport"
)

// MaxPayloadSize is the maximum MQTT message payload size (256 KB).
const MaxPayloadSize = 256 * 1024

// Transport implements the transport.Transport interface using MQTT.
type Transport struct {
	cfg      config.MQTTConfig
	region   string
	nodeID   identity.Hash
	client   pahomqtt.Client
	incoming chan *transport.Packet
	health   atomic.Int32
	mu       sync.RWMutex
	started  bool
	cancel   context.CancelFunc
}

// New creates a new MQTT transport from configuration.
func New(cfg config.MQTTConfig, region string, nodeID identity.Hash) *Transport {
	t := &Transport{
		cfg:      cfg,
		region:   region,
		nodeID:   nodeID,
		incoming: make(chan *transport.Packet, 256),
	}
	t.health.Store(0)
	return t
}

// Name returns "mqtt".
func (t *Transport) Name() string {
	return "mqtt"
}

// Start connects to the MQTT broker and subscribes to relevant topics.
func (t *Transport) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.started {
		return fmt.Errorf("mqtt: already started")
	}

	ctx, cancel := context.WithCancel(ctx)
	t.cancel = cancel

	opts := pahomqtt.NewClientOptions().
		AddBroker(t.cfg.Broker).
		SetClientID(t.cfg.ClientID).
		SetKeepAlive(time.Duration(t.cfg.Keepalive) * time.Second).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(30 * time.Second).
		SetConnectionLostHandler(func(_ pahomqtt.Client, err error) {
			log.Printf("mqtt: connection lost: %v", err)
			t.health.Store(0)
		}).
		SetOnConnectHandler(func(c pahomqtt.Client) {
			log.Printf("mqtt: connected to %s", t.cfg.Broker)
			t.health.Store(100)
			// Re-subscribe on reconnect
			t.subscribe(c)
		})

	if t.cfg.Username != "" {
		opts.SetUsername(t.cfg.Username)
		opts.SetPassword(t.cfg.Password)
	}

	if t.cfg.TLS.Enabled {
		tlsConfig, err := t.buildTLSConfig()
		if err != nil {
			cancel()
			return fmt.Errorf("mqtt: configure TLS: %w", err)
		}
		opts.SetTLSConfig(tlsConfig)
		log.Printf("mqtt: TLS enabled (ca=%s, client_cert=%s)", t.cfg.TLS.CACert, t.cfg.TLS.ClientCert)
	}

	client := pahomqtt.NewClient(opts)
	token := client.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		cancel()
		return fmt.Errorf("mqtt: connect to %s: %w", t.cfg.Broker, err)
	}

	t.client = client
	t.started = true

	// Monitor context for shutdown
	go func() {
		<-ctx.Done()
		t.Stop()
	}()

	return nil
}

// subscribe sets up MQTT topic subscriptions.
func (t *Transport) subscribe(client pahomqtt.Client) {
	prefix := t.topicPrefix()

	// Subscribe to messages addressed to users on this node
	msgTopic := fmt.Sprintf("%s/msg/+", prefix)
	client.Subscribe(msgTopic, t.cfg.QoS, t.handleMessage)

	// Subscribe to presence announcements (separate handler — presence
	// payloads are raw msgpack, NOT prefixed with a source hash).
	presenceTopic := fmt.Sprintf("%s/presence/+", prefix)
	client.Subscribe(presenceTopic, t.cfg.QoS, t.handlePresence)

	// Subscribe to node-specific control channel
	nodeTopic := fmt.Sprintf("%s/node/%s", prefix, t.nodeID.String())
	client.Subscribe(nodeTopic, t.cfg.QoS, t.handleMessage)

	// Subscribe to admin channel for OTA updates and control messages
	adminTopic := fmt.Sprintf("%s/admin/%s", prefix, t.nodeID.String())
	client.Subscribe(adminTopic, t.cfg.QoS, t.handleAdmin)

	log.Printf("mqtt: subscribed to %s/msg/+, %s/presence/+, %s/node/%s, %s/admin/%s",
		prefix, prefix, prefix, t.nodeID.String(), prefix, t.nodeID.String())
}

// handleMessage is the MQTT message handler for regular msg/ and node/ topics.
func (t *Transport) handleMessage(_ pahomqtt.Client, msg pahomqtt.Message) {
	// Parse the destination hash from the topic
	topic := msg.Topic()
	parts := strings.Split(topic, "/")
	if len(parts) < 3 {
		return
	}

	// Extract the hash from the last topic segment
	hashHex := parts[len(parts)-1]
	destHash, err := identity.HashFromHex(hashHex)
	if err != nil {
		// Not a user-hash topic (e.g., node topic), use zero hash
		destHash = identity.Hash{}
	}

	payload := msg.Payload()

	// Extract source hash from the payload prefix (first 16 bytes).
	// The Send method prepends the source hash so we can recover it here.
	var sourceHash identity.Hash
	if len(payload) > identity.HashSize {
		sh, shErr := identity.HashFromBytes(payload[:identity.HashSize])
		if shErr == nil {
			sourceHash = sh
			payload = payload[identity.HashSize:]
		}
	}

	pkt := &transport.Packet{
		Type:       transport.PacketMessage,
		DestHash:   destHash,
		SourceHash: sourceHash,
		Data:       payload,
		Via:        "mqtt",
	}

	select {
	case t.incoming <- pkt:
	default:
		log.Printf("mqtt: incoming packet queue full, dropping message on %s", topic)
	}
}

// handlePresence is the MQTT handler for presence/ topics. Presence payloads
// are raw msgpack announce.Packet bytes (NOT prefixed with a source hash).
// An empty payload signals that the user has gone offline (retained message
// cleared by ClearPresence).
func (t *Transport) handlePresence(_ pahomqtt.Client, msg pahomqtt.Message) {
	topic := msg.Topic()
	parts := strings.Split(topic, "/")
	if len(parts) < 3 {
		return
	}

	hashHex := parts[len(parts)-1]
	userHash, err := identity.HashFromHex(hashHex)
	if err != nil {
		return
	}

	payload := msg.Payload()

	pkt := &transport.Packet{
		Type:     transport.PacketPresence,
		DestHash: userHash,
		Data:     payload, // raw msgpack or empty (offline signal)
		Via:      "mqtt",
	}

	select {
	case t.incoming <- pkt:
	default:
		log.Printf("mqtt: incoming packet queue full, dropping presence on %s", topic)
	}
}

// handleAdmin is the MQTT handler for admin/ topics. Admin payloads carry
// control messages such as OTA update manifests.
func (t *Transport) handleAdmin(_ pahomqtt.Client, msg pahomqtt.Message) {
	payload := msg.Payload()
	if len(payload) == 0 {
		return
	}

	pkt := &transport.Packet{
		Type: transport.PacketAdmin,
		Data: payload,
		Via:  "mqtt",
	}

	select {
	case t.incoming <- pkt:
	default:
		log.Printf("mqtt: incoming packet queue full, dropping admin message on %s", msg.Topic())
	}
}

// Stop disconnects from the MQTT broker.
func (t *Transport) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.started {
		return nil
	}

	if t.cancel != nil {
		t.cancel()
	}

	if t.client != nil && t.client.IsConnected() {
		t.client.Disconnect(1000) // 1 second timeout
	}

	close(t.incoming)
	t.started = false
	t.health.Store(0)
	log.Printf("mqtt: stopped")
	return nil
}

// Send publishes a message to the appropriate MQTT topic.
// The source hash is prepended to the payload so the receiving node
// can attribute the message to the correct sender.
func (t *Transport) Send(ctx context.Context, pkt *transport.Packet) error {
	if !t.client.IsConnected() {
		return transport.ErrTransportDown
	}

	// Prepend source hash to payload so receivers can identify the sender.
	wire := make([]byte, identity.HashSize+len(pkt.Data))
	copy(wire[:identity.HashSize], pkt.SourceHash[:])
	copy(wire[identity.HashSize:], pkt.Data)

	if len(wire) > MaxPayloadSize {
		return transport.ErrPayloadTooLarge
	}

	topic := fmt.Sprintf("%s/msg/%s", t.topicPrefix(), pkt.DestHash.String())

	token := t.client.Publish(topic, t.cfg.QoS, false, wire)
	token.Wait()
	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt: publish: %w", err)
	}

	return nil
}

// SendPresence publishes a presence announcement as a retained message.
func (t *Transport) SendPresence(ctx context.Context, userHash identity.Hash, data []byte) error {
	if !t.client.IsConnected() {
		return transport.ErrTransportDown
	}

	topic := fmt.Sprintf("%s/presence/%s", t.topicPrefix(), userHash.String())

	token := t.client.Publish(topic, t.cfg.QoS, true, data) // retained=true
	token.Wait()
	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt: publish presence: %w", err)
	}

	return nil
}

// ClearPresence publishes an empty retained message to remove a user's
// presence from the MQTT broker. This is the MQTT convention for deleting
// a retained message.
func (t *Transport) ClearPresence(ctx context.Context, userHash identity.Hash) error {
	if !t.client.IsConnected() {
		return transport.ErrTransportDown
	}

	topic := fmt.Sprintf("%s/presence/%s", t.topicPrefix(), userHash.String())

	// Empty retained message clears the retained state on the broker.
	token := t.client.Publish(topic, t.cfg.QoS, true, []byte{})
	token.Wait()
	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt: clear presence: %w", err)
	}

	return nil
}

// Receive returns the channel delivering incoming packets.
func (t *Transport) Receive() <-chan *transport.Packet {
	return t.incoming
}

// Health returns the current health score (0-100).
func (t *Transport) Health() int {
	return int(t.health.Load())
}

// topicPrefix returns the MQTT topic prefix for this region.
func (t *Transport) topicPrefix() string {
	prefix := t.cfg.TopicPrefix
	if prefix == "" {
		prefix = "relay"
	}
	return fmt.Sprintf("%s/%s", prefix, t.region)
}

// buildTLSConfig creates a *tls.Config from the transport's TLS settings.
// When CACert is empty the system certificate pool is used. When both
// ClientCert and ClientKey are provided, mutual TLS (mTLS) is configured.
func (t *Transport) buildTLSConfig() (*tls.Config, error) {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Custom CA certificate
	if t.cfg.TLS.CACert != "" {
		caCert, err := os.ReadFile(t.cfg.TLS.CACert)
		if err != nil {
			return nil, fmt.Errorf("read CA cert %s: %w", t.cfg.TLS.CACert, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("parse CA cert %s: no valid certificates found", t.cfg.TLS.CACert)
		}
		tlsCfg.RootCAs = pool
	}

	// Client certificate for mutual TLS
	if t.cfg.TLS.ClientCert != "" && t.cfg.TLS.ClientKey != "" {
		cert, err := tls.LoadX509KeyPair(t.cfg.TLS.ClientCert, t.cfg.TLS.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return tlsCfg, nil
}
