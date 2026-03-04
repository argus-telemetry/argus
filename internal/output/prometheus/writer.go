package prometheus

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/argus-5g/argus/internal/normalizer"
)

// Writer exposes normalized 5G telemetry as Prometheus metrics via a pull /metrics endpoint.
// Naming: namespace.kpiName with dots replaced by underscores
// (e.g. argus.5g.amf.registration.success_rate -> argus_5g_amf_registration_success_rate).
// ResourceAttributes are mapped to Prometheus labels: vendor, nf_type, nf_instance_id,
// plmn_id, data_center, and optional slice_sst, slice_sd, cell_id.
type Writer struct {
	registry *prometheus.Registry
	mu       sync.Mutex
	gauges   map[string]*prometheus.GaugeVec
	server   *http.Server
	addr     string
}

// labelNames defines the standard set of labels emitted on every Prometheus gauge.
// Optional labels (slice_sst, slice_sd, cell_id) are always present but set to ""
// when not applicable — Prometheus drops empty labels at scrape time.
var labelNames = []string{
	"vendor",
	"nf_type",
	"nf_instance_id",
	"plmn_id",
	"data_center",
	"slice_sst",
	"slice_sd",
	"cell_id",
}

// NewWriter creates a Prometheus pull writer. All KPIs are exposed as gauges
// (even counters, because Argus computes deltas before output).
func NewWriter(listenAddr string) *Writer {
	return &Writer{
		registry: prometheus.NewRegistry(),
		gauges:   make(map[string]*prometheus.GaugeVec),
		addr:     listenAddr,
	}
}

// Name returns the writer identifier.
func (w *Writer) Name() string { return "prometheus" }

// BatchSize returns 0 — the pull model doesn't batch writes.
func (w *Writer) BatchSize() int { return 0 }

// Write converts NormalizedRecords to Prometheus gauges. GaugeVec instances are
// lazily created per metric name on first encounter.
func (w *Writer) Write(_ context.Context, records []normalizer.NormalizedRecord) error {
	for _, rec := range records {
		name := sanitizeMetricName(rec.Namespace + "." + rec.KPIName)
		labels := buildLabels(rec.Attributes)

		gv, err := w.getOrCreateGauge(name)
		if err != nil {
			return fmt.Errorf("create gauge %q: %w", name, err)
		}
		gv.With(labels).Set(rec.Value)
	}
	return nil
}

// Flush is a no-op for the pull model.
func (w *Writer) Flush(_ context.Context) error { return nil }

// Close shuts down the HTTP server if it was started.
func (w *Writer) Close() error {
	if w.server != nil {
		return w.server.Close()
	}
	return nil
}

// Start begins serving /metrics on the configured listen address.
// Called separately from NewWriter so callers can optionally use Handler()
// to embed in an existing server instead.
func (w *Writer) Start() error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", w.Handler())
	w.server = &http.Server{
		Addr:    w.addr,
		Handler: mux,
	}
	return w.server.ListenAndServe()
}

// Registry returns the underlying prometheus registry for registering
// additional metrics (e.g. self-telemetry) on the same /metrics endpoint.
func (w *Writer) Registry() *prometheus.Registry {
	return w.registry
}

// Handler returns the HTTP handler for /metrics. Useful for embedding
// in another server without calling Start().
func (w *Writer) Handler() http.Handler {
	return promhttp.HandlerFor(w.registry, promhttp.HandlerOpts{})
}

// getOrCreateGauge returns an existing GaugeVec or creates and registers a new one.
func (w *Writer) getOrCreateGauge(name string) (*prometheus.GaugeVec, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if gv, ok := w.gauges[name]; ok {
		return gv, nil
	}

	gv := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: name,
		Help: "5G KPI: " + name,
	}, labelNames)

	if err := w.registry.Register(gv); err != nil {
		return nil, err
	}
	w.gauges[name] = gv
	return gv, nil
}

// sanitizeMetricName replaces dots with underscores to form valid Prometheus metric names.
func sanitizeMetricName(name string) string {
	return strings.ReplaceAll(name, ".", "_")
}

// buildLabels extracts Prometheus labels from ResourceAttributes.
func buildLabels(attrs normalizer.ResourceAttributes) prometheus.Labels {
	labels := prometheus.Labels{
		"vendor":         attrs.Vendor,
		"nf_type":        attrs.NFType,
		"nf_instance_id": attrs.NFInstanceID,
		"plmn_id":        attrs.PLMNID,
		"data_center":    attrs.DataCenter,
		"slice_sst":      "",
		"slice_sd":       "",
		"cell_id":        "",
	}
	if attrs.SliceID != nil {
		labels["slice_sst"] = fmt.Sprintf("%d", attrs.SliceID.SST)
		labels["slice_sd"] = attrs.SliceID.SD
	}
	if attrs.CellID != nil {
		labels["cell_id"] = attrs.CellID.GlobalCellID
	}
	return labels
}
