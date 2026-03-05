package normalizer

import (
	"time"

	"github.com/argus-5g/argus/internal/collector"
)

// NormalizedRecord is a single KPI measurement in the unified Argus 5G schema.
// Every field maps to an OpenTelemetry-compatible attribute with 3GPP grounding.
type NormalizedRecord struct {
	Namespace     string // "argus.5g.amf" | "argus.5g.smf" | "argus.5g.upf" | "argus.5g.gnb" | "argus.5g.slice"
	KPIName       string // e.g. "registration.success_rate", "session.active_count"
	Value         float64
	Unit          string // "ratio" | "count" | "bps" | "ms" | "dBm"
	Timestamp     time.Time
	Attributes    ResourceAttributes
	Labels        map[string]string // extracted labels from LabelExtract rules and metric labels
	SpecRef       string            // 3GPP spec reference, e.g. "3GPP TS 28.552 §5.1.1.3"
	SchemaVersion string            // "v1" — for pipeline compatibility across version skew
}

// ResourceAttributes are standard 5G resource identifiers attached to every normalized record.
type ResourceAttributes struct {
	PLMNID       string   // operator PLMN identity (MCC-MNC)
	NFInstanceID string   // NF instance UUID or identifier
	NFType       string   // "AMF" | "SMF" | "UPF" | "gNB" | "PCF"
	DataCenter   string   // where the NF runs, e.g. "dc-us-east-1"
	SliceID      *SliceID // non-nil for slice KPIs
	CellID       *CellID  // non-nil for RAN KPIs
	Vendor       string   // "free5gc" | "open5gs" | "oai" | "nokia" | "ericsson"
}

// SliceID identifies a 5G network slice per 3GPP TS 23.501.
type SliceID struct {
	SST int    // Slice/Service Type: 1=eMBB, 2=URLLC, 3=mIoT
	SD  string // Slice Differentiator (hex string, e.g. "000001")
}

// CellID identifies a 5G NR cell.
type CellID struct {
	GlobalCellID string // globally unique cell identifier
	NCI          uint64 // NR Cell Identity (36-bit)
	TAC          uint32 // Tracking Area Code
}

// NormalizeResult contains the output of normalizing a single RawRecord.
// A raw record from a real NF often contains many KPIs — some may parse
// correctly while others fail. This struct captures both outcomes.
type NormalizeResult struct {
	Records []NormalizedRecord // successfully normalized KPIs
	Partial []NormalizeError   // KPIs that failed individually
}

// NormalizeError describes a single KPI that failed to normalize.
// Total failures (corrupt payload, wrong protocol) are returned as error from Normalize().
// Partial failures (one bad KPI among 50 good ones) end up here.
type NormalizeError struct {
	KPIName     string // which KPI failed
	Raw         string // the raw data that caused the failure
	Reason      string // human-readable error description
	Unsupported bool   // true if the KPI has no vendor mapping (not a real failure)
}

// Normalizer transforms raw vendor telemetry into the unified Argus 5G schema.
type Normalizer interface {
	// CanHandle returns true if this normalizer can process the given raw record.
	// Checked against vendor + NF type + protocol.
	CanHandle(r collector.RawRecord) bool

	// Normalize transforms a raw record into normalized KPIs.
	// Returns error for total failures (corrupt payload, unsupported protocol).
	// Returns partial failures in NormalizeResult.Partial for individual KPI issues.
	Normalize(r collector.RawRecord) (NormalizeResult, error)
}
