package collector

import (
	"context"
	"errors"
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
	Payload       []byte // raw vendor payload (JSON, XML, protobuf, Prometheus exposition)
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

// ErrorClass categorizes scrape failures for operator alerting.
type ErrorClass string

const (
	ErrorClassNetwork ErrorClass = "network"
	ErrorClassAuth    ErrorClass = "auth"
	ErrorClassParse   ErrorClass = "parse"
	ErrorClassTimeout ErrorClass = "timeout"
)

// ScrapeError carries structured failure context from a collector scrape.
type ScrapeError struct {
	Err       error
	Class     ErrorClass
	Vendor    string
	NFType    string
	Collector string
}

func (e ScrapeError) Error() string { return e.Err.Error() }
func (e ScrapeError) Unwrap() error { return e.Err }

// ClassifyScrapeError maps an HTTP scrape failure into an ErrorClass.
// statusCode should be 0 for connection-level failures (no HTTP response).
func ClassifyScrapeError(err error, statusCode int) ErrorClass {
	switch {
	case statusCode == 401 || statusCode == 403:
		return ErrorClassAuth
	case statusCode > 0:
		// Got an HTTP response but non-2xx — treat as network-level issue.
		return ErrorClassNetwork
	default:
		// No HTTP response at all. Distinguish timeout from other network errors.
		if isTimeoutError(err) {
			return ErrorClassTimeout
		}
		return ErrorClassNetwork
	}
}

// isTimeoutError checks if err is a timeout (net.Error with Timeout() == true).
func isTimeoutError(err error) bool {
	type timeouter interface{ Timeout() bool }
	var te timeouter
	if errors.As(err, &te) {
		return te.Timeout()
	}
	return false
}

// CollectorConfig configures a collector plugin instance.
type CollectorConfig struct {
	Endpoint      string
	Interval      time.Duration
	TLS           *TLSConfig
	Credentials   *CredentialConfig
	Extra         map[string]any    // plugin-specific config — each plugin must document its keys
	OnScrapeError func(ScrapeError) // called on each scrape failure; nil = errors silently dropped
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
