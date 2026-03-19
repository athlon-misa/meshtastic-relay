// Command relayd is the Meshtastic Relay daemon.
//
// It runs a relay node that bridges internet (MQTT) and LoRa mesh (Meshtastic)
// transports, providing store-and-forward messaging with portable cryptographic
// identities.
//
// Usage:
//
//	relayd -config config.yaml
//	relayd -config config.yaml -debug
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/meshtastic-relay/relay/audit"
	"github.com/meshtastic-relay/relay/bridge"
	"github.com/meshtastic-relay/relay/config"
	"github.com/meshtastic-relay/relay/federation"
	"github.com/meshtastic-relay/relay/identity"
	"github.com/meshtastic-relay/relay/node"
	"github.com/meshtastic-relay/relay/power"
	"github.com/meshtastic-relay/relay/ratelimit"
	"github.com/meshtastic-relay/relay/server"
	"github.com/meshtastic-relay/relay/store"
	transportpkg "github.com/meshtastic-relay/relay/transport"
	"github.com/meshtastic-relay/relay/transport/meshradio"
	"github.com/meshtastic-relay/relay/transport/mqtt"
	"github.com/meshtastic-relay/relay/transport/router"
	"github.com/meshtastic-relay/relay/update"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "path to configuration file")
	showVersion := flag.Bool("version", false, "print version and exit")
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	if *showVersion {
		fmt.Printf("relayd %s\n", version)
		os.Exit(0)
	}

	if *debug {
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	} else {
		log.SetFlags(log.Ldate | log.Ltime)
	}

	log.Printf("relayd %s starting", version)

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	log.Printf("Configuration loaded from %s", *configPath)

	// Load or generate node identity
	nodeID, err := identity.LoadOrGenerate(cfg.Node.IdentityPath)
	if err != nil {
		log.Fatalf("Failed to load node identity: %v", err)
	}
	log.Printf("Node identity: %s", nodeID.Hash())

	// Open message store
	msgStore, err := store.Open(cfg.Store.DBPath, cfg.Store.MaxAge, cfg.Store.MaxPerRecipient)
	if err != nil {
		log.Fatalf("Failed to open message store: %v", err)
	}
	log.Printf("Message store opened at %s", cfg.Store.DBPath)

	// Initialize transports
	var transports []transportpkg.Transport

	// MQTT transport (primary, always enabled)
	mqttTransport := mqtt.New(cfg.MQTT, cfg.Node.Region, nodeID.Hash())
	transports = append(transports, mqttTransport)

	// Meshtastic serial transport (optional)
	var meshTransport *meshradio.Transport
	if cfg.MeshRadio.Enabled {
		meshTransport = meshradio.New(cfg.MeshRadio, nodeID.Hash())
		transports = append(transports, meshTransport)
	}

	// Create router with transports in priority order
	r := router.New(transports...)

	// Create the node daemon
	relay := node.New(cfg, nodeID, r, msgStore)

	// Parse and configure power management tier
	powerTier, err := cfg.ParsePowerTier()
	if err != nil {
		log.Printf("Warning: %v, defaulting to full power", err)
		powerTier = power.TierFull
	}
	pm := power.NewManager(powerTier)
	relay.SetPowerManager(pm)
	log.Printf("Power management tier: %s", powerTier)

	// Initialize the OTA update manager.
	binaryPath, err := os.Executable()
	if err != nil {
		log.Printf("Warning: could not determine executable path for OTA updates: %v", err)
		binaryPath = "relayd"
	}
	var signerKey []byte // Empty for now; set from config when signing is enabled.
	um := update.NewManager(version, binaryPath, signerKey)
	relay.SetUpdateManager(um)
	log.Printf("OTA update manager initialized (version=%s, binary=%s)", version, binaryPath)

	// Create the WebSocket user-connection server
	wsSrv := server.New(cfg.Daemon.ListenAddr, relay)

	// Register the server's delivery callback so the node can push
	// inbound messages to connected WebSocket users.
	relay.SetDeliveryFunc(wsSrv.DeliverToUser)

	// Wire MQTT presence: the node publishes/clears presence via MQTT
	// retained messages, and presence changes are pushed to WebSocket clients.
	relay.SetPresenceSender(mqttTransport)
	relay.SetPresenceNotifyFunc(wsSrv.BroadcastPresence)

	// Wire alert broadcasts: the node pushes emergency alerts to all
	// connected WebSocket clients via the server's BroadcastAlert method.
	relay.SetAlertBroadcastFunc(wsSrv.BroadcastAlert)

	// Configure rate limiting for WebSocket message processing.
	rl := ratelimit.NewLimiter(cfg.Daemon.RateLimitRate, cfg.Daemon.RateLimitBurst)
	wsSrv.SetRateLimiter(rl)
	log.Printf("Rate limiting: %.0f tokens/s, burst %d", cfg.Daemon.RateLimitRate, cfg.Daemon.RateLimitBurst)

	// Configure structured audit logging.
	al, err := audit.NewFileLogger(cfg.Daemon.AuditLogPath, nodeID.Hash().String())
	if err != nil {
		log.Fatalf("Failed to open audit log: %v", err)
	}
	wsSrv.SetAuditLogger(al)
	log.Printf("Audit logging to %s", cfg.Daemon.AuditLogPath)

	// Initialize OpenFederation identity linking if enabled.
	if cfg.Federation.Enabled {
		fedStore := federation.NewStore(cfg.Federation.PDSURL)
		wsSrv.SetFederationStore(fedStore)
		log.Printf("Federation identity linking enabled (PDS: %s)", cfg.Federation.PDSURL)
	}

	// Initialize LoRa gateway bridge if enabled
	var loraBridge *bridge.Bridge
	var registryStore *bridge.RegistryStore
	if cfg.Bridge.Enabled && cfg.MeshRadio.Enabled && meshTransport != nil {
		// Open a persistent registry store alongside the main message store.
		registryDBPath := filepath.Join(filepath.Dir(cfg.Store.DBPath), "bridge-registry.db")
		var rsErr error
		registryStore, rsErr = bridge.OpenRegistryStore(registryDBPath)
		if rsErr != nil {
			log.Fatalf("bridge: failed to open registry store: %v", rsErr)
		}
		log.Printf("bridge: registry store opened at %s", registryDBPath)

		reg, rsErr := bridge.NewPersistentRegistry(cfg.Bridge.ActiveTimeout, registryStore)
		if rsErr != nil {
			log.Fatalf("bridge: failed to create persistent registry: %v", rsErr)
		}

		// Register YAML-configured devices (idempotent — skip if already persisted
		// with the same node number from a previous run).
		for _, dev := range cfg.Bridge.Devices {
			nodeNum, parseErr := parseNodeNum(dev.NodeNum)
			if parseErr != nil {
				log.Fatalf("bridge: invalid node_num %q for device %q: %v", dev.NodeNum, dev.Label, parseErr)
			}
			userHash, parseErr := identity.HashFromHex(dev.UserHash)
			if parseErr != nil {
				log.Fatalf("bridge: invalid user_hash %q for device %q: %v", dev.UserHash, dev.Label, parseErr)
			}
			if reg.IsRegistered(nodeNum) {
				log.Printf("bridge: device %q (0x%08x) already registered, skipping", dev.Label, nodeNum)
				continue
			}
			if regErr := reg.Register(bridge.DeviceRegistration{
				MeshNodeNum:  nodeNum,
				UserHash:     userHash,
				Label:        dev.Label,
				RegisteredAt: time.Now(),
			}); regErr != nil {
				log.Fatalf("bridge: failed to register device %q: %v", dev.Label, regErr)
			}
		}

		wsSrv.SetRegistry(reg)

		bridgeCfg := bridge.BridgeConfig{
			Enabled:       true,
			ActiveTimeout: cfg.Bridge.ActiveTimeout,
			DedupWindow:   cfg.Bridge.DedupWindow,
		}
		loraBridge = bridge.New(bridgeCfg, reg, nodeID.Hash())

		// Set bridge delivery: formats message and sends via meshradio
		relay.SetBridgeDeliveryFunc(func(destHash, senderHash identity.Hash, data []byte) error {
			payload, targetNode, err := loraBridge.FormatOutbound(destHash, senderHash, data)
			if err != nil {
				return err
			}
			return meshTransport.SendText(targetNode, payload)
		})

		// Set inbound interceptor for bridge remapping
		relay.SetInboundInterceptor(func(pkt *transportpkg.Packet) *transportpkg.Packet {
			if pkt.Via == "meshradio" && pkt.MeshNodeNum != 0 {
				return loraBridge.ProcessInbound(pkt)
			}
			return pkt
		})

		log.Printf("bridge: LoRa gateway bridge configured with %d registered devices", len(reg.AllRegistrations()))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start rate limiter background cleanup.
	rl.StartCleanup(ctx)

	// Log daemon startup to the audit log.
	al.Log(audit.EventAdminAction, "", "", "daemon_start", true)

	if err := relay.Start(ctx); err != nil {
		log.Fatalf("Failed to start relay node: %v", err)
	}

	// Start bridge after node is running
	if loraBridge != nil {
		loraBridge.Start(ctx)
		log.Printf("bridge: LoRa gateway bridge started")
	}

	// Start the WebSocket server (non-blocking; it runs in the background)
	if err := wsSrv.Start(ctx); err != nil {
		log.Fatalf("Failed to start WebSocket server: %v", err)
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	log.Printf("Received signal %v, shutting down...", sig)

	cancel()
	if err := wsSrv.Stop(); err != nil {
		log.Printf("Error stopping WebSocket server: %v", err)
	}
	if err := relay.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	if registryStore != nil {
		if err := registryStore.Close(); err != nil {
			log.Printf("Error closing registry store: %v", err)
		}
	}

	log.Printf("relayd stopped")
}

// parseNodeNum parses a Meshtastic node number from a string that may be
// decimal ("439041101") or hexadecimal with 0x prefix ("0x1A2B3C4D").
func parseNodeNum(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		n, err := strconv.ParseUint(s[2:], 16, 32)
		if err != nil {
			return 0, fmt.Errorf("parse hex node num: %w", err)
		}
		return uint32(n), nil
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse decimal node num: %w", err)
	}
	return uint32(n), nil
}
