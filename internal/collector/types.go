package collector

import (
	"context"
	"time"
)

// Protocol identifies the telemetry transport protocol.
type Protocol string

const (
	// ProtocolPrometheus indicates Prometheus exposition format over HTTP.
	ProtocolPrometheus Protocol = "prometheus"
	// ProtocolGNMI indicates gNMI Subscribe RPCs over gRPC.
	ProtocolGNMI Protocol = "gnmi"
	// ProtocolNETCONF indicates NETCONF over SSH.
	ProtocolNETCONF Protocol = "netconf"
	// ProtocolREST indicates JSON over HTTP REST APIs.
	ProtocolREST Protocol = "rest"
)

// SourceInfo identifies the network function that emitted a telemetry record.
type SourceInfo struct {
	Vendor     string // "free5gc" | "open5gs" | "oai" | "nokia" | "ericsson"
	NFType     string // "AMF" | "SMF" | "UPF" | "gNB" | "PCF"
	InstanceID string // NF instance identifier
	Endpoint   string // connection endpoint (host:port or URL)
}

// RawRecord is an unprocessed telemetry payload from a network function.
type RawRecord struct {
	Source        SourceInfo
	Payload       []byte            // raw vendor payload (JSON, XML, protobuf, Prometheus exposition)
	Protocol      Protocol
	Timestamp     time.Time
	Meta          map[string]string // vendor-specific metadata
	SchemaVersion string            // "v1" — for pipeline compatibility across version skew
}

// TLSConfig holds TLS settings for collector connections.
type TLSConfig struct {
	CertFile   string
	KeyFile    string
	CAFile     string
	SkipVerify bool
}

// CredentialConfig holds authentication credentials for collector connections.
type CredentialConfig struct {
	Username string
	Password string
	Token    string
}

// CollectorConfig configures a collector plugin instance.
type CollectorConfig struct {
	Endpoint    string
	Interval    time.Duration
	TLS         *TLSConfig
	Credentials *CredentialConfig
	Extra       map[string]any // plugin-specific config — each plugin must document its keys
}

// Collector defines the interface for vendor-specific telemetry collection plugins.
// Implementations fetch raw telemetry from a network function and emit RawRecords.
type Collector interface {
	// Name returns a human-readable identifier (e.g. "free5gc-amf").
	Name() string

	// Connect initializes the connection to the NF endpoint.
	// Called once at startup. Must fail fast if the endpoint is unreachable.
	Connect(ctx context.Context, cfg CollectorConfig) error

	// Collect blocks and continuously emits raw telemetry records to ch.
	// Returns when ctx is cancelled or on unrecoverable error.
	Collect(ctx context.Context, ch chan<- RawRecord) error

	// Close releases resources and connections.
	Close() error
}
