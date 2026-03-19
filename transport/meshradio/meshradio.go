// Package meshradio implements the Meshtastic serial transport for LoRa mesh
// communication. It communicates with a locally attached Meshtastic radio
// via serial protobuf protocol to send and receive mesh packets.
//
// This transport serves as the fallback path when internet (MQTT) connectivity
// is unavailable.
//
// Serial wire format: [0x94] [0xC3] [len_MSB] [len_LSB] [protobuf bytes...]
package meshradio

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	pb "buf.build/gen/go/meshtastic/protobufs/protocolbuffers/go/meshtastic"
	"go.bug.st/serial"
	"google.golang.org/protobuf/proto"

	"github.com/meshtastic-relay/relay/config"
	"github.com/meshtastic-relay/relay/identity"
	"github.com/meshtastic-relay/relay/transport"
)

// MaxLoRaPayload is the maximum payload size for a LoRa mesh packet.
const MaxLoRaPayload = 233

// maxFrameSize is the maximum serial frame payload to accept (bytes).
const maxFrameSize = 512

// broadcastAddr is the Meshtastic broadcast address.
const broadcastAddr = 0xFFFFFFFF

// Frame start magic bytes.
var frameStart = []byte{0x94, 0xC3}

// Transport implements the transport.Transport interface using a Meshtastic
// radio connected via serial port.
type Transport struct {
	cfg       config.MeshRadioConfig
	nodeID    identity.Hash
	port      serial.Port
	incoming  chan *transport.Packet
	health    atomic.Int32
	mu        sync.RWMutex
	writeMu   sync.Mutex // serializes writes to the serial port
	started   bool
	ready     bool // true after config handshake completes
	cancel    context.CancelFunc
	myNodeNum uint32 // local Meshtastic node number, learned during config
}

// New creates a new Meshtastic serial transport from configuration.
func New(cfg config.MeshRadioConfig, nodeID identity.Hash) *Transport {
	t := &Transport{
		cfg:      cfg,
		nodeID:   nodeID,
		incoming: make(chan *transport.Packet, 64),
	}
	t.health.Store(0)
	return t
}

// Name returns "meshradio".
func (t *Transport) Name() string {
	return "meshradio"
}

// Start opens the serial port, performs the config handshake with the
// Meshtastic radio, and begins reading mesh packets.
func (t *Transport) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.started {
		return fmt.Errorf("meshradio: already started")
	}

	ctx, cancel := context.WithCancel(ctx)
	t.cancel = cancel

	mode := &serial.Mode{
		BaudRate: t.cfg.BaudRate,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}

	port, err := serial.Open(t.cfg.Port, mode)
	if err != nil {
		cancel()
		return fmt.Errorf("meshradio: open serial port %s: %w", t.cfg.Port, err)
	}

	if err := port.SetReadTimeout(100 * time.Millisecond); err != nil {
		port.Close()
		cancel()
		return fmt.Errorf("meshradio: set read timeout: %w", err)
	}

	t.port = port
	t.started = true

	// Wake the device by sending 32 bytes of START2 (0xC3).
	wake := make([]byte, 32)
	for i := range wake {
		wake[i] = 0xC3
	}
	if _, err := port.Write(wake); err != nil {
		port.Close()
		cancel()
		t.started = false
		return fmt.Errorf("meshradio: wake device: %w", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Start reading frames in background. The handshake runs inside the
	// read loop — it watches for config_complete_id to know when the
	// device is ready.
	go t.readLoop(ctx)
	go t.healthMonitor(ctx)

	// Request device config to complete the handshake.
	if err := t.requestConfig(); err != nil {
		log.Printf("meshradio: config request failed: %v (will retry on reconnect)", err)
	}

	log.Printf("meshradio: started on %s at %d baud", t.cfg.Port, t.cfg.BaudRate)
	return nil
}

// requestConfig sends a want_config_id request to the device. The device
// will respond with its full configuration, then a config_complete_id.
func (t *Transport) requestConfig() error {
	configID, err := randomUint32()
	if err != nil {
		return fmt.Errorf("generate config id: %w", err)
	}

	toRadio := &pb.ToRadio{
		PayloadVariant: &pb.ToRadio_WantConfigId{
			WantConfigId: configID,
		},
	}

	return t.writeProto(toRadio)
}

// readLoop continuously reads framed protobuf messages from the serial port.
func (t *Transport) readLoop(ctx context.Context) {
	defer func() {
		t.health.Store(0)
	}()

	buf := make([]byte, 4096)
	var frameBuf []byte

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := t.port.Read(buf)
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return
			}
			continue
		}
		if n == 0 {
			continue
		}

		frameBuf = append(frameBuf, buf[:n]...)

		for {
			frame, remainder, ok := extractFrame(frameBuf)
			if !ok {
				break
			}
			frameBuf = remainder
			t.handleFrame(frame)
		}

		// Prevent unbounded buffer growth.
		if len(frameBuf) > 16384 {
			frameBuf = frameBuf[len(frameBuf)-4096:]
		}
	}
}

// extractFrame attempts to extract a complete Meshtastic serial frame.
// Frame format: [0x94, 0xC3] + [2-byte big-endian length] + [protobuf data]
func extractFrame(buf []byte) ([]byte, []byte, bool) {
	for i := 0; i < len(buf)-1; i++ {
		if buf[i] == frameStart[0] && buf[i+1] == frameStart[1] {
			if len(buf) < i+4 {
				return nil, buf[i:], false
			}
			frameLen := int(binary.BigEndian.Uint16(buf[i+2 : i+4]))
			if frameLen > maxFrameSize {
				// Corrupt length — skip this marker and keep scanning.
				continue
			}
			totalLen := i + 4 + frameLen
			if len(buf) < totalLen {
				return nil, buf[i:], false
			}
			frame := buf[i+4 : totalLen]
			remainder := buf[totalLen:]
			return frame, remainder, true
		}
	}

	if len(buf) > 1 {
		return nil, buf[len(buf)-1:], false
	}
	return nil, buf, false
}

// handleFrame decodes a FromRadio protobuf and dispatches the payload.
func (t *Transport) handleFrame(frame []byte) {
	var fromRadio pb.FromRadio
	if err := proto.Unmarshal(frame, &fromRadio); err != nil {
		// Not a valid protobuf frame — likely debug log bytes.
		return
	}

	switch v := fromRadio.GetPayloadVariant().(type) {
	case *pb.FromRadio_Packet:
		t.handleMeshPacket(v.Packet)

	case *pb.FromRadio_MyInfo:
		info := v.MyInfo
		t.myNodeNum = info.GetMyNodeNum()
		log.Printf("meshradio: my node num: %d (0x%08x)", t.myNodeNum, t.myNodeNum)

	case *pb.FromRadio_ConfigCompleteId:
		t.mu.Lock()
		t.ready = true
		t.mu.Unlock()
		t.health.Store(100)
		log.Printf("meshradio: config complete, radio ready")

	case *pb.FromRadio_NodeInfo:
		info := v.NodeInfo
		if user := info.GetUser(); user != nil {
			log.Printf("meshradio: discovered node %s (0x%08x)",
				user.GetLongName(), info.GetNum())
		}

	case *pb.FromRadio_Rebooted:
		log.Printf("meshradio: device rebooted, re-requesting config")
		t.mu.Lock()
		t.ready = false
		t.mu.Unlock()
		t.health.Store(50)
		go func() {
			time.Sleep(500 * time.Millisecond)
			if err := t.requestConfig(); err != nil {
				log.Printf("meshradio: re-config request failed: %v", err)
			}
		}()

	case *pb.FromRadio_LogRecord:
		rec := v.LogRecord
		log.Printf("meshradio: device log: %s", rec.GetMessage())

	default:
		// Config, Channel, ModuleConfig, etc. — ignore during normal operation.
	}
}

// handleMeshPacket processes a decoded MeshPacket from the radio.
func (t *Transport) handleMeshPacket(mp *pb.MeshPacket) {
	if mp == nil {
		return
	}

	// Skip packets we sent ourselves.
	if mp.GetFrom() == t.myNodeNum {
		return
	}

	decoded := mp.GetDecoded()
	if decoded == nil {
		// Encrypted packet we can't read (wrong channel key, or PKI).
		return
	}

	portNum := decoded.GetPortnum()
	payload := decoded.GetPayload()

	switch portNum {
	case pb.PortNum_TEXT_MESSAGE_APP:
		t.handleTextMessage(mp, payload)

	case pb.PortNum_NODEINFO_APP:
		t.handleActivityPacket(mp)

	case pb.PortNum_POSITION_APP, pb.PortNum_TELEMETRY_APP:
		t.handleActivityPacket(mp)

	default:
		log.Printf("meshradio: received portnum %d from 0x%08x (%d bytes)",
			portNum, mp.GetFrom(), len(payload))
	}
}

// handleTextMessage converts an incoming Meshtastic text message into a
// transport.Packet and pushes it to the incoming channel.
func (t *Transport) handleTextMessage(mp *pb.MeshPacket, payload []byte) {
	// Build source hash from the Meshtastic node number. Since Meshtastic
	// node IDs are 32-bit and our identity hashes are 16-byte, we zero-pad
	// and place the node number in the first 4 bytes. This allows relay users
	// to see which mesh node sent the message.
	var sourceHash identity.Hash
	binary.BigEndian.PutUint32(sourceHash[:4], mp.GetFrom())

	var destHash identity.Hash
	to := mp.GetTo()
	if to != broadcastAddr {
		binary.BigEndian.PutUint32(destHash[:4], to)
	}
	// If broadcast, destHash stays zero — node will handle it.

	// Prepend source hash to payload so it matches the format the server expects.
	data := make([]byte, identity.HashSize+len(payload))
	copy(data[:identity.HashSize], sourceHash[:])
	copy(data[identity.HashSize:], payload)

	pkt := &transport.Packet{
		DestHash:     destHash,
		SourceHash:   sourceHash,
		Data:         payload,
		Via:          "meshradio",
		MeshNodeNum:  mp.GetFrom(),
		MeshPacketID: mp.GetId(),
	}

	select {
	case t.incoming <- pkt:
		t.health.Store(100)
		log.Printf("meshradio: text from 0x%08x: %s", mp.GetFrom(), string(payload))
	default:
		log.Printf("meshradio: incoming queue full, dropping text message")
	}
}

// handleActivityPacket emits a metadata-only packet for non-text portnums
// (POSITION_APP, TELEMETRY_APP, NODEINFO_APP) so the bridge can track device
// activity and update LastHeard timestamps.
func (t *Transport) handleActivityPacket(mp *pb.MeshPacket) {
	pkt := &transport.Packet{
		Via:          "meshradio",
		MeshNodeNum:  mp.GetFrom(),
		MeshPacketID: mp.GetId(),
	}

	select {
	case t.incoming <- pkt:
	default:
		log.Printf("meshradio: incoming queue full, dropping activity packet")
	}
}

// SendText sends a text message to a specific Meshtastic node number.
// This is used by the bridge for outbound LoRa delivery to individual nodes
// rather than broadcast.
func (t *Transport) SendText(nodeNum uint32, text []byte) error {
	t.mu.RLock()
	ready := t.ready
	started := t.started
	t.mu.RUnlock()

	if !started || t.port == nil {
		return transport.ErrTransportDown
	}
	if !ready {
		return fmt.Errorf("meshradio: radio not ready (config handshake incomplete)")
	}

	if len(text) > MaxLoRaPayload {
		return transport.ErrPayloadTooLarge
	}

	packetID, err := randomUint32()
	if err != nil {
		return fmt.Errorf("meshradio: generate packet id: %w", err)
	}

	meshPacket := &pb.MeshPacket{
		To:       nodeNum,
		Channel:  0,
		Id:       packetID,
		WantAck:  false,
		HopLimit: 3,
		PayloadVariant: &pb.MeshPacket_Decoded{
			Decoded: &pb.Data{
				Portnum: pb.PortNum_TEXT_MESSAGE_APP,
				Payload: text,
			},
		},
	}

	toRadio := &pb.ToRadio{
		PayloadVariant: &pb.ToRadio_Packet{
			Packet: meshPacket,
		},
	}

	if err := t.writeProto(toRadio); err != nil {
		return fmt.Errorf("meshradio: send text: %w", err)
	}

	log.Printf("meshradio: sent %d bytes to node 0x%08x via LoRa", len(text), nodeNum)
	return nil
}

// healthMonitor periodically decays the health score if no data is received.
func (t *Transport) healthMonitor(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := int(t.health.Load())
			if current > 20 {
				t.health.Store(int32(current - 5))
			}
		}
	}
}

// Stop closes the serial port and stops the read loop.
func (t *Transport) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.started {
		return nil
	}

	if t.cancel != nil {
		t.cancel()
	}

	if t.port != nil {
		t.port.Close()
	}

	close(t.incoming)
	t.started = false
	t.ready = false
	t.health.Store(0)
	log.Printf("meshradio: stopped")
	return nil
}

// Send transmits a text message via the Meshtastic radio. The packet data
// is sent as a TEXT_MESSAGE_APP payload on channel 0.
func (t *Transport) Send(ctx context.Context, pkt *transport.Packet) error {
	t.mu.RLock()
	ready := t.ready
	started := t.started
	t.mu.RUnlock()

	if !started || t.port == nil {
		return transport.ErrTransportDown
	}
	if !ready {
		return fmt.Errorf("meshradio: radio not ready (config handshake incomplete)")
	}

	payload := pkt.Data
	if len(payload) > MaxLoRaPayload {
		return transport.ErrPayloadTooLarge
	}

	// Determine the destination Meshtastic node number.
	// If destHash is zero (broadcast) or doesn't map to a known node,
	// send as broadcast on the mesh.
	var destNode uint32 = broadcastAddr
	if !pkt.DestHash.IsZero() {
		// Use the first 4 bytes of the dest hash as the Meshtastic node number.
		// This works when the dest hash was derived from a Meshtastic node.
		// For relay-to-relay messages, broadcast is the right choice since
		// the recipient relay will filter by its own logic.
		destNode = binary.BigEndian.Uint32(pkt.DestHash[:4])
		if destNode == 0 {
			destNode = broadcastAddr
		}
	}

	packetID, err := randomUint32()
	if err != nil {
		return fmt.Errorf("meshradio: generate packet id: %w", err)
	}

	meshPacket := &pb.MeshPacket{
		To:       destNode,
		Channel:  0, // Primary channel
		Id:       packetID,
		WantAck:  false,
		HopLimit: 3,
		PayloadVariant: &pb.MeshPacket_Decoded{
			Decoded: &pb.Data{
				Portnum: pb.PortNum_TEXT_MESSAGE_APP,
				Payload: payload,
			},
		},
	}

	toRadio := &pb.ToRadio{
		PayloadVariant: &pb.ToRadio_Packet{
			Packet: meshPacket,
		},
	}

	if err := t.writeProto(toRadio); err != nil {
		return fmt.Errorf("meshradio: send: %w", err)
	}

	log.Printf("meshradio: sent %d bytes to 0x%08x via LoRa", len(payload), destNode)
	return nil
}

// writeProto marshals a protobuf message and writes it as a framed serial
// packet to the Meshtastic device.
func (t *Transport) writeProto(msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal proto: %w", err)
	}

	frame := make([]byte, 4+len(data))
	frame[0] = frameStart[0]
	frame[1] = frameStart[1]
	binary.BigEndian.PutUint16(frame[2:4], uint16(len(data)))
	copy(frame[4:], data)

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	_, err = t.port.Write(frame)
	if err != nil {
		t.health.Store(0)
		return fmt.Errorf("write serial: %w", err)
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

// randomUint32 generates a cryptographically random uint32.
func randomUint32() (uint32, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<32))
	if err != nil {
		return 0, err
	}
	return uint32(n.Uint64()), nil
}
