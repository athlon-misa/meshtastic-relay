// Package router implements path selection and failover logic for the relay
// transport layer. It monitors the health of each transport and routes
// packets via the best available path (internet-first with LoRa fallback).
package router

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/meshtastic-relay/relay/transport"
)

// HealthThreshold is the minimum health score (0-100) required for a
// transport to be considered viable for sending.
const HealthThreshold = 20

// Router manages multiple transports and selects the best path for
// each outgoing packet. Incoming packets from all transports are
// multiplexed onto a single receive channel.
type Router struct {
	transports []transport.Transport
	incoming   chan *transport.Packet
	mu         sync.RWMutex
	started    bool
	cancel     context.CancelFunc
}

// New creates a new Router with the given transports.
// Transports are tried in priority order (first is highest priority).
func New(transports ...transport.Transport) *Router {
	return &Router{
		transports: transports,
		incoming:   make(chan *transport.Packet, 256),
	}
}

// Start initializes all transports and begins multiplexing incoming packets.
func (r *Router) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return fmt.Errorf("router: already started")
	}

	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	// Start all transports
	var startedTransports []transport.Transport
	for _, t := range r.transports {
		if err := t.Start(ctx); err != nil {
			log.Printf("router: failed to start transport %s: %v (continuing with others)", t.Name(), err)
			continue
		}
		startedTransports = append(startedTransports, t)
		log.Printf("router: started transport %s", t.Name())
	}

	if len(startedTransports) == 0 {
		cancel()
		return fmt.Errorf("router: no transports started successfully")
	}

	// Multiplex incoming packets from all transports
	for _, t := range startedTransports {
		go r.forwardIncoming(ctx, t)
	}

	r.started = true
	log.Printf("router: started with %d/%d transports", len(startedTransports), len(r.transports))
	return nil
}

// forwardIncoming reads from a transport's receive channel and forwards
// packets to the router's unified incoming channel.
func (r *Router) forwardIncoming(ctx context.Context, t transport.Transport) {
	for {
		select {
		case <-ctx.Done():
			return
		case pkt, ok := <-t.Receive():
			if !ok {
				return
			}
			select {
			case r.incoming <- pkt:
			case <-ctx.Done():
				return
			default:
				log.Printf("router: incoming queue full, dropping packet from %s", t.Name())
			}
		}
	}
}

// Stop shuts down all transports and the router.
func (r *Router) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return nil
	}

	if r.cancel != nil {
		r.cancel()
	}

	var firstErr error
	for _, t := range r.transports {
		if err := t.Stop(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	r.started = false
	log.Printf("router: stopped")
	return firstErr
}

// Send routes a packet via the best available transport.
// It tries transports in priority order, falling back to lower-priority
// transports if higher-priority ones are unhealthy or fail.
func (r *Router) Send(ctx context.Context, pkt *transport.Packet) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if !r.started {
		return fmt.Errorf("router: not started")
	}

	var lastErr error
	for _, t := range r.transports {
		health := t.Health()
		if health < HealthThreshold {
			log.Printf("router: skipping %s (health=%d, threshold=%d)", t.Name(), health, HealthThreshold)
			continue
		}

		if err := t.Send(ctx, pkt); err != nil {
			lastErr = err
			log.Printf("router: send via %s failed: %v, trying fallback", t.Name(), err)
			continue
		}

		log.Printf("router: sent %d bytes via %s to %s", len(pkt.Data), t.Name(), pkt.DestHash)
		return nil
	}

	if lastErr != nil {
		return fmt.Errorf("router: all transports failed, last error: %w", lastErr)
	}
	return fmt.Errorf("router: no healthy transports available")
}

// SendDualPath sends a packet via all healthy transports simultaneously.
// This is useful for high-priority messages where redundancy is desired.
func (r *Router) SendDualPath(ctx context.Context, pkt *transport.Packet) []error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var errs []error
	sent := 0

	for _, t := range r.transports {
		if t.Health() < HealthThreshold {
			continue
		}
		if err := t.Send(ctx, pkt); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.Name(), err))
		} else {
			sent++
		}
	}

	if sent == 0 && len(errs) == 0 {
		errs = append(errs, fmt.Errorf("no healthy transports available"))
	}

	return errs
}

// Receive returns the unified channel delivering incoming packets
// from all transports.
func (r *Router) Receive() <-chan *transport.Packet {
	return r.incoming
}

// HealthReport returns the health status of all transports.
func (r *Router) HealthReport() map[string]int {
	report := make(map[string]int, len(r.transports))
	for _, t := range r.transports {
		report[t.Name()] = t.Health()
	}
	return report
}

// BestTransport returns the name and health score of the highest-priority
// healthy transport, or empty string and 0 if none are available.
func (r *Router) BestTransport() (string, int) {
	for _, t := range r.transports {
		h := t.Health()
		if h >= HealthThreshold {
			return t.Name(), h
		}
	}
	return "", 0
}

// IsOnline reports whether at least one transport is above the health threshold.
func (r *Router) IsOnline() bool {
	for _, t := range r.transports {
		if t.Health() >= HealthThreshold {
			return true
		}
	}
	return false
}
