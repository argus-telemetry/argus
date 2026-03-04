package telemetry

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds Argus self-observability metrics. Registered at startup,
// emitted alongside 5G KPIs on the /metrics endpoint.
type Metrics struct {
	CollectorScrapeDuration *prometheus.HistogramVec
	CollectorScrapeSuccess  *prometheus.GaugeVec
	CollectorScrapeErrors   *prometheus.CounterVec
	CollectorRecordsTotal   *prometheus.CounterVec
	NormalizerRecordsTotal  *prometheus.CounterVec
	NormalizerPartialFails  *prometheus.CounterVec
	NormalizerTotalFails    *prometheus.CounterVec
	PipelinePublishTotal    *prometheus.CounterVec
	OutputWriteTotal        *prometheus.CounterVec
	OutputWriteErrors       *prometheus.CounterVec
	SchemaLoadSuccess       *prometheus.GaugeVec
	CorrelatorEvaluations   *prometheus.CounterVec
	CorrelatorEventsTotal   *prometheus.CounterVec
}

// NewMetrics creates all self-observability metrics with argus_ prefix.
func NewMetrics() *Metrics {
	return &Metrics{
		CollectorScrapeDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "argus_collector_scrape_duration_seconds",
			Help:    "Time spent scraping a collector endpoint.",
			Buckets: prometheus.DefBuckets,
		}, []string{"collector"}),

		CollectorScrapeSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "argus_collector_scrape_success",
			Help: "Whether the last scrape of a collector succeeded (1) or failed (0).",
		}, []string{"collector"}),

		CollectorScrapeErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "argus_collector_scrape_error_total",
			Help: "Total scrape failures per vendor, NF type, and error class.",
		}, []string{"vendor", "nf_type", "error_class"}),

		CollectorRecordsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "argus_collector_records_total",
			Help: "Total raw records emitted by each collector.",
		}, []string{"collector"}),

		NormalizerRecordsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "argus_normalizer_records_total",
			Help: "Total normalized records produced per vendor and NF type.",
		}, []string{"vendor", "nf_type"}),

		NormalizerPartialFails: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "argus_normalizer_partial_failures_total",
			Help: "Total individual KPI normalization failures (partial — other KPIs in the same record succeeded).",
		}, []string{"vendor", "nf_type"}),

		NormalizerTotalFails: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "argus_normalizer_total_failures_total",
			Help: "Total records that failed normalization entirely (corrupt payload, wrong protocol).",
		}, []string{"vendor", "nf_type"}),

		PipelinePublishTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "argus_pipeline_publish_total",
			Help: "Total messages published to each pipeline topic.",
		}, []string{"topic"}),

		OutputWriteTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "argus_output_write_total",
			Help: "Total records written to each output backend.",
		}, []string{"writer"}),

		OutputWriteErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "argus_output_write_errors_total",
			Help: "Total write errors per output backend.",
		}, []string{"writer"}),

		SchemaLoadSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "argus_schema_load_success",
			Help: "Whether the schema loaded successfully (1) or failed (0) per version.",
		}, []string{"schema_version"}),

		CorrelatorEvaluations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "argus_correlator_window_evaluations_total",
			Help: "Total correlation window evaluations.",
		}, []string{}),

		CorrelatorEventsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "argus_correlator_events_total",
			Help: "Total correlation events fired.",
		}, []string{"rule_name", "severity"}),
	}
}

// Register registers all metrics with the given prometheus registerer.
func (m *Metrics) Register(reg prometheus.Registerer) {
	reg.MustRegister(
		m.CollectorScrapeDuration,
		m.CollectorScrapeSuccess,
		m.CollectorScrapeErrors,
		m.CollectorRecordsTotal,
		m.NormalizerRecordsTotal,
		m.NormalizerPartialFails,
		m.NormalizerTotalFails,
		m.PipelinePublishTotal,
		m.OutputWriteTotal,
		m.OutputWriteErrors,
		m.SchemaLoadSuccess,
		m.CorrelatorEvaluations,
		m.CorrelatorEventsTotal,
	)
}

// RecordScrape records a collector scrape duration and success/failure.
func (m *Metrics) RecordScrape(collector string, duration time.Duration, success bool) {
	m.CollectorScrapeDuration.WithLabelValues(collector).Observe(duration.Seconds())
	val := 0.0
	if success {
		val = 1.0
	}
	m.CollectorScrapeSuccess.WithLabelValues(collector).Set(val)
}

// RecordScrapeError increments the scrape error counter with classification labels.
func (m *Metrics) RecordScrapeError(vendor, nfType, errorClass string) {
	m.CollectorScrapeErrors.WithLabelValues(vendor, nfType, errorClass).Inc()
}

// RecordNormalize records normalization results for a vendor/NF type.
func (m *Metrics) RecordNormalize(vendor, nfType string, count int, partialFails int) {
	m.NormalizerRecordsTotal.WithLabelValues(vendor, nfType).Add(float64(count))
	if partialFails > 0 {
		m.NormalizerPartialFails.WithLabelValues(vendor, nfType).Add(float64(partialFails))
	}
}

// RecordPublish records messages published to a pipeline topic.
func (m *Metrics) RecordPublish(topic string, count int) {
	m.PipelinePublishTotal.WithLabelValues(topic).Add(float64(count))
}

// RecordWrite records output write results.
func (m *Metrics) RecordWrite(writer string, count int, err error) {
	m.OutputWriteTotal.WithLabelValues(writer).Add(float64(count))
	if err != nil {
		m.OutputWriteErrors.WithLabelValues(writer).Inc()
	}
}

// RecordSchemaLoad records schema load success/failure.
func (m *Metrics) RecordSchemaLoad(version string, success bool) {
	val := 0.0
	if success {
		val = 1.0
	}
	m.SchemaLoadSuccess.WithLabelValues(version).Set(val)
}
