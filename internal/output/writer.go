package output

import (
	"context"

	"github.com/argus-5g/argus/internal/normalizer"
)

// Writer outputs normalized 5G telemetry records to an external backend.
// Implementations include Prometheus (v0.1), OpenTelemetry Collector (v0.2),
// and OpenSearch (v0.2).
type Writer interface {
	// Name returns the writer's identifier, e.g. "prometheus".
	Name() string

	// BatchSize returns the preferred batch size for this writer.
	// The pipeline uses this as a hint for batching records before calling Write.
	// Return 0 for no preference (pipeline decides).
	BatchSize() int

	// Write sends a batch of normalized records to the backend.
	Write(ctx context.Context, records []normalizer.NormalizedRecord) error

	// Flush drains any internal buffers. Called during graceful shutdown
	// to ensure no records are lost.
	Flush(ctx context.Context) error

	// Close releases backend connections and resources.
	Close() error
}
