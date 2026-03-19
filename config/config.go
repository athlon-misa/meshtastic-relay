// Package config provides configuration loading and defaults for the relay daemon.
package config

import (
	"fmt"
	"os"
	"time"

	"github.com/meshtastic-relay/relay/power"
	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for the relay daemon.
type Config struct {
	Node       NodeConfig       `yaml:"node"`
	MQTT       MQTTConfig       `yaml:"mqtt"`
	MeshRadio  MeshRadioConfig  `yaml:"meshradio"`
	Bridge     BridgeConfig     `yaml:"bridge"`
	Store      StoreConfig      `yaml:"store"`
	Daemon     DaemonConfig     `yaml:"daemon"`
	Logging    LoggingConfig    `yaml:"logging"`
	Federation FederationConfig `yaml:"federation"`
}

// NodeConfig configures the relay node identity and region.
type NodeConfig struct {
	// IdentityPath is the filesystem path to the node's keypair file.
	IdentityPath string `yaml:"identity_path"`
	// Name is an optional human-readable node name.
	Name string `yaml:"name"`
	// Region is the geographic region for MQTT topic namespacing.
	Region string `yaml:"region"`
}

// MQTTConfig configures the MQTT transport.
type MQTTConfig struct {
	Broker      string    `yaml:"broker"`
	ClientID    string    `yaml:"client_id"`
	Username    string    `yaml:"username"`
	Password    string    `yaml:"password"`
	TLS         TLSConfig `yaml:"tls"`
	TopicPrefix string    `yaml:"topic_prefix"`
	QoS         byte      `yaml:"qos"`
	Keepalive   int       `yaml:"keepalive"`
}

// TLSConfig configures TLS for the MQTT connection.
type TLSConfig struct {
	Enabled    bool   `yaml:"enabled"`
	CACert     string `yaml:"ca_cert"`
	ClientCert string `yaml:"client_cert"`
	ClientKey  string `yaml:"client_key"`
}

// MeshRadioConfig configures the Meshtastic serial transport.
type MeshRadioConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Port     string `yaml:"port"`
	BaudRate int    `yaml:"baud_rate"`
}

// BridgeConfig configures the LoRa gateway bridge that maps Meshtastic
// devices to relay identity hashes, enabling bidirectional message delivery.
type BridgeConfig struct {
	Enabled       bool                `yaml:"enabled"`
	ActiveTimeout time.Duration       `yaml:"active_timeout"`
	DedupWindow   time.Duration       `yaml:"dedup_window"`
	Devices       []DeviceConfigEntry `yaml:"devices"`
}

// DeviceConfigEntry maps a Meshtastic radio node number to a relay identity hash.
type DeviceConfigEntry struct {
	NodeNum  string `yaml:"node_num"`  // hex (0x1A2B3C4D) or decimal
	UserHash string `yaml:"user_hash"` // 32-char hex identity hash
	Label    string `yaml:"label"`     // human-readable label
}

// StoreConfig configures the store-and-forward message queue.
type StoreConfig struct {
	DBPath          string        `yaml:"db_path"`
	MaxAge          time.Duration `yaml:"max_age"`
	MaxPerRecipient int           `yaml:"max_per_recipient"`
	CleanupInterval time.Duration `yaml:"cleanup_interval"`
}

// DaemonConfig configures the relay node daemon.
type DaemonConfig struct {
	ListenAddr        string        `yaml:"listen_addr"`
	MaxSessions       int           `yaml:"max_sessions"`
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
	SessionTimeout    time.Duration `yaml:"session_timeout"`
	// PowerTier selects the power management profile: "full", "normal",
	// "conserve", or "emergency". Defaults to "full".
	PowerTier string `yaml:"power_tier"`
	// RateLimitRate is the token refill rate in tokens per second.
	RateLimitRate float64 `yaml:"rate_limit_rate"`
	// RateLimitBurst is the maximum number of tokens (burst capacity).
	RateLimitBurst int `yaml:"rate_limit_burst"`
	// AuditLogPath is the filesystem path for the structured audit log.
	AuditLogPath string `yaml:"audit_log_path"`
}

// LoggingConfig configures logging output.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// FederationConfig configures optional OpenFederation identity linking.
type FederationConfig struct {
	// Enabled controls whether OpenFederation identity binding is available.
	Enabled bool `yaml:"enabled"`
	// PDSURL is the base URL of the ATProto Personal Data Server used for
	// token verification.
	PDSURL string `yaml:"pds_url"`
}

// Default returns a Config populated with sane default values.
func Default() *Config {
	return &Config{
		Node: NodeConfig{
			IdentityPath: "node-identity.key",
			Name:         "relay-node",
			Region:       "default",
		},
		MQTT: MQTTConfig{
			Broker:      "tcp://localhost:1883",
			ClientID:    "relay-node",
			TopicPrefix: "relay",
			QoS:         1,
			Keepalive:   30,
		},
		MeshRadio: MeshRadioConfig{
			Enabled:  false,
			Port:     "/dev/ttyUSB0",
			BaudRate: 115200,
		},
		Bridge: BridgeConfig{
			Enabled:       false,
			ActiveTimeout: 5 * time.Minute,
			DedupWindow:   2 * time.Minute,
		},
		Store: StoreConfig{
			DBPath:          "relay-store.db",
			MaxAge:          30 * 24 * time.Hour,
			MaxPerRecipient: 1000,
			CleanupInterval: time.Hour,
		},
		Daemon: DaemonConfig{
			ListenAddr:        ":9370",
			MaxSessions:       100,
			HeartbeatInterval: 30 * time.Second,
			SessionTimeout:    120 * time.Second,
			RateLimitRate:     30,
			RateLimitBurst:    60,
			AuditLogPath:      "audit.log",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
		Federation: FederationConfig{
			Enabled: false,
			PDSURL:  "https://pds.openfederation.net",
		},
	}
}

// Load reads a YAML configuration file and returns a Config.
// Missing fields are filled with default values.
func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read file %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config: validate: %w", err)
	}

	return cfg, nil
}

// ParsePowerTier converts the DaemonConfig.PowerTier string to a power.Tier.
// An empty string defaults to TierFull.
func (c *Config) ParsePowerTier() (power.Tier, error) {
	return power.ParseTier(c.Daemon.PowerTier)
}

// validate checks that required configuration values are present and sensible.
func (c *Config) validate() error {
	if c.Node.IdentityPath == "" {
		return fmt.Errorf("node.identity_path is required")
	}
	if c.Node.Region == "" {
		return fmt.Errorf("node.region is required")
	}
	if c.MQTT.Broker == "" {
		return fmt.Errorf("mqtt.broker is required")
	}
	if c.MQTT.ClientID == "" {
		return fmt.Errorf("mqtt.client_id is required")
	}
	if c.Store.MaxAge <= 0 {
		return fmt.Errorf("store.max_age must be positive")
	}
	if c.Store.MaxPerRecipient <= 0 {
		return fmt.Errorf("store.max_per_recipient must be positive")
	}
	if c.Daemon.MaxSessions <= 0 {
		return fmt.Errorf("daemon.max_sessions must be positive")
	}

	// TLS: client_cert and client_key must both be set or both be empty.
	if c.MQTT.TLS.Enabled {
		hasCert := c.MQTT.TLS.ClientCert != ""
		hasKey := c.MQTT.TLS.ClientKey != ""
		if hasCert != hasKey {
			return fmt.Errorf("mqtt.tls: client_cert and client_key must both be set or both be empty")
		}
	}

	return nil
}
