module github.com/meshtastic-relay/relay

go 1.25.0

require (
	buf.build/gen/go/meshtastic/protobufs/protocolbuffers/go v1.36.11-20260311001937-5d866262a0ed.1
	filippo.io/edwards25519 v1.2.0
	github.com/eclipse/paho.mqtt.golang v1.5.1
	github.com/gorilla/websocket v1.5.3
	github.com/vmihailenco/msgpack/v5 v5.4.1
	go.bug.st/serial v1.6.4
	go.etcd.io/bbolt v1.4.3
	golang.org/x/crypto v0.52.0
	google.golang.org/protobuf v1.36.11
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/creack/goselect v0.1.2 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	golang.org/x/net v0.54.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
)
