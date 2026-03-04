package free5gc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/argus-5g/argus/internal/collector"
)

// Extra config keys (via CollectorConfig.Extra):
//
//	nf_type: string — "AMF" | "SMF" | "UPF" (set by factory, not user config)

// Collector scrapes Prometheus /metrics from a free5GC NF instance.
// Each instance targets a single NF type (AMF, SMF, or UPF) and emits RawRecords
// with the corresponding source metadata.
type Collector struct {
	nfType string
	cfg    collector.CollectorConfig
	client *http.Client
}

// Name returns the collector's identifier (e.g. "free5gc-amf").
func (c *Collector) Name() string {
	return "free5gc-" + strings.ToLower(c.nfType)
}

// Connect initializes the HTTP client and stores the configuration.
// Validates that an endpoint is configured.
func (c *Collector) Connect(_ context.Context, cfg collector.CollectorConfig) error {
	if cfg.Endpoint == "" {
		return fmt.Errorf("free5gc %s collector: endpoint must not be empty", c.nfType)
	}
	if cfg.Interval <= 0 {
		return fmt.Errorf("free5gc %s collector: interval must be positive", c.nfType)
	}
	c.cfg = cfg
	c.client = &http.Client{
		Timeout: cfg.Interval / 2, // scrape timeout: half the interval
	}
	return nil
}

// Collect blocks and continuously scrapes the NF's /metrics endpoint at the
// configured interval. Emits one RawRecord per scrape containing the full
// Prometheus exposition payload. Returns when ctx is cancelled.
func (c *Collector) Collect(ctx context.Context, ch chan<- collector.RawRecord) error {
	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()

	// Scrape immediately on start, then on ticker.
	for {
		if scrapeErr := c.scrape(ctx, ch); scrapeErr != nil {
			if c.cfg.OnScrapeError != nil {
				c.cfg.OnScrapeError(*scrapeErr)
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Close releases the HTTP client's idle connections.
func (c *Collector) Close() error {
	if c.client != nil {
		c.client.CloseIdleConnections()
	}
	return nil
}

// scrape performs a single HTTP GET to the /metrics endpoint and sends
// the response body as a RawRecord. Returns a structured ScrapeError on failure
// with pre-classified error class for telemetry.
func (c *Collector) scrape(ctx context.Context, ch chan<- collector.RawRecord) *collector.ScrapeError {
	url := strings.TrimRight(c.cfg.Endpoint, "/") + "/metrics"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return c.makeScrapeError(err, 0)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return c.makeScrapeError(fmt.Errorf("scrape %s: %w", url, err), 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.makeScrapeError(
			fmt.Errorf("scrape %s: unexpected status %d", url, resp.StatusCode),
			resp.StatusCode,
		)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return c.makeScrapeError(fmt.Errorf("read body from %s: %w", url, err), 0)
	}

	record := collector.RawRecord{
		Source: collector.SourceInfo{
			Vendor:   "free5gc",
			NFType:   c.nfType,
			Endpoint: c.cfg.Endpoint,
		},
		Payload:       body,
		Protocol:      collector.ProtocolPrometheus,
		Timestamp:     time.Now(),
		SchemaVersion: "v1",
	}

	select {
	case ch <- record:
	case <-ctx.Done():
		return nil // context cancellation is not a scrape error
	}

	return nil
}

func (c *Collector) makeScrapeError(err error, statusCode int) *collector.ScrapeError {
	return &collector.ScrapeError{
		Err:       err,
		Class:     collector.ClassifyScrapeError(err, statusCode),
		Vendor:    "free5gc",
		NFType:    c.nfType,
		Collector: c.Name(),
	}
}
