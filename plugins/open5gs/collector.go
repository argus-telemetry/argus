package open5gs

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

// Collector scrapes Prometheus /metrics from an Open5GS NF instance.
type Collector struct {
	nfType string
	cfg    collector.CollectorConfig
	client *http.Client
}

func (c *Collector) Name() string {
	return "open5gs-" + strings.ToLower(c.nfType)
}

func (c *Collector) Connect(_ context.Context, cfg collector.CollectorConfig) error {
	if cfg.Endpoint == "" {
		return fmt.Errorf("open5gs %s collector: endpoint must not be empty", c.nfType)
	}
	if cfg.Interval <= 0 {
		return fmt.Errorf("open5gs %s collector: interval must be positive", c.nfType)
	}
	c.cfg = cfg
	c.client = &http.Client{
		Timeout: cfg.Interval / 2,
	}
	return nil
}

func (c *Collector) Collect(ctx context.Context, ch chan<- collector.RawRecord) error {
	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()

	for {
		if err := c.scrape(ctx, ch); err != nil {
			// Non-fatal: continue scraping.
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Collector) Close() error {
	if c.client != nil {
		c.client.CloseIdleConnections()
	}
	return nil
}

func (c *Collector) scrape(ctx context.Context, ch chan<- collector.RawRecord) error {
	url := strings.TrimRight(c.cfg.Endpoint, "/") + "/metrics"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("scrape %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("scrape %s: unexpected status %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body from %s: %w", url, err)
	}

	record := collector.RawRecord{
		Source: collector.SourceInfo{
			Vendor:   "open5gs",
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
		return ctx.Err()
	}

	return nil
}
