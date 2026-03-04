package otlp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/argus-5g/argus/internal/normalizer"
)

// Config controls the OTLP/gRPC metric exporter.
type Config struct {
	Endpoint      string
	Insecure      bool
	Headers       map[string]string
	BatchInterval time.Duration // default 15s
	BatchSize     int           // default 100
}

func (c *Config) applyDefaults() {
	if c.BatchInterval == 0 {
		c.BatchInterval = 15 * time.Second
	}
	if c.BatchSize == 0 {
		c.BatchSize = 100
	}
}

// Writer exports normalized 5G KPIs as OTLP metrics over gRPC.
// Each KPI becomes a Float64Gauge named namespace.kpiName with resource
// attributes mapped to OTel attributes on the data point.
type Writer struct {
	provider *sdkmetric.MeterProvider
	meter    otelmetric.Meter
	mu       sync.Mutex
	gauges   map[string]otelmetric.Float64Gauge
	cfg      Config
	closed   bool
}

// NewWriter creates a Writer backed by a real gRPC OTLP exporter.
func NewWriter(cfg Config) (*Writer, error) {
	cfg.applyDefaults()

	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
	}
	if cfg.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlpmetricgrpc.WithHeaders(cfg.Headers))
	}

	exporter, err := otlpmetricgrpc.New(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP gRPC exporter: %w", err)
	}

	reader := sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(cfg.BatchInterval))
	return newWriter(cfg, reader)
}

// newWriterWithReader creates a Writer backed by a ManualReader for testing.
// Tests call reader.Collect() to trigger metric collection without a live collector.
func newWriterWithReader(cfg Config) (*Writer, *sdkmetric.ManualReader, error) {
	cfg.applyDefaults()
	reader := sdkmetric.NewManualReader()
	w, err := newWriter(cfg, reader)
	return w, reader, err
}

// newWriter is the shared constructor. reader is either a PeriodicReader (prod)
// or ManualReader (test).
func newWriter(cfg Config, reader sdkmetric.Reader) (*Writer, error) {
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			attribute.String("service.name", "argus"),
			attribute.String("service.version", "0.2"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create OTel resource: %w", err)
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
	)

	return &Writer{
		provider: provider,
		meter:    provider.Meter("argus"),
		gauges:   make(map[string]otelmetric.Float64Gauge),
		cfg:      cfg,
	}, nil
}

// Name returns the writer identifier.
func (w *Writer) Name() string { return "otlp" }

// BatchSize returns the configured batch size hint.
func (w *Writer) BatchSize() int { return w.cfg.BatchSize }

// Write converts NormalizedRecords to OTel gauge observations. Gauge instruments
// are lazily created per metric name (namespace.kpiName). Resource attributes
// are attached as OTel attributes on each data point.
func (w *Writer) Write(ctx context.Context, records []normalizer.NormalizedRecord) error {
	for _, rec := range records {
		name := rec.Namespace + "." + rec.KPIName

		gauge, err := w.getOrCreateGauge(name, rec)
		if err != nil {
			return fmt.Errorf("create gauge %q: %w", name, err)
		}

		attrs := buildAttributes(rec.Attributes)
		gauge.Record(ctx, rec.Value, otelmetric.WithAttributes(attrs...))
	}
	return nil
}

// Flush forces the meter provider to export all pending metrics.
func (w *Writer) Flush(ctx context.Context) error {
	return w.provider.ForceFlush(ctx)
}

// Close shuts down the meter provider. Safe to call multiple times.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.provider.Shutdown(context.Background())
}

// getOrCreateGauge returns an existing gauge or creates one with the correct
// unit and description from the record's metadata.
func (w *Writer) getOrCreateGauge(name string, rec normalizer.NormalizedRecord) (otelmetric.Float64Gauge, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if g, ok := w.gauges[name]; ok {
		return g, nil
	}

	g, err := w.meter.Float64Gauge(name,
		otelmetric.WithUnit(rec.Unit),
		otelmetric.WithDescription(rec.SpecRef),
	)
	if err != nil {
		return nil, err
	}
	w.gauges[name] = g
	return g, nil
}

// buildAttributes maps ResourceAttributes to OTel key-value pairs.
// Optional attributes (slice, cell) are only emitted when the source pointer is non-nil.
func buildAttributes(attrs normalizer.ResourceAttributes) []attribute.KeyValue {
	kvs := []attribute.KeyValue{
		attribute.String("nf.type", attrs.NFType),
		attribute.String("nf.vendor", attrs.Vendor),
		attribute.String("nf.instance_id", attrs.NFInstanceID),
		attribute.String("network.plmn_id", attrs.PLMNID),
		attribute.String("network.data_center", attrs.DataCenter),
	}
	if attrs.SliceID != nil {
		kvs = append(kvs,
			attribute.String("network.slice.sst", fmt.Sprintf("%d", attrs.SliceID.SST)),
			attribute.String("network.slice.sd", attrs.SliceID.SD),
		)
	}
	if attrs.CellID != nil {
		kvs = append(kvs, attribute.String("network.cell.id", attrs.CellID.GlobalCellID))
	}
	return kvs
}
