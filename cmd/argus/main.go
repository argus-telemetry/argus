package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/argus-5g/argus/internal/collector"
	"github.com/argus-5g/argus/internal/normalizer"
	"github.com/argus-5g/argus/internal/output"
	otlpwriter "github.com/argus-5g/argus/internal/output/otlp"
	promwriter "github.com/argus-5g/argus/internal/output/prometheus"
	"github.com/argus-5g/argus/internal/pipeline"
	"github.com/argus-5g/argus/internal/schema"
	"github.com/argus-5g/argus/internal/telemetry"
)

func main() {
	configPath := flag.String("config", "argus.yaml", "path to config file")
	flag.Parse()

	if err := run(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "argus: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Load schema registry.
	reg, err := schema.LoadFromDir(cfg.SchemaDir)
	metrics := telemetry.NewMetrics()
	if err != nil {
		metrics.RecordSchemaLoad("v1", false)
		return fmt.Errorf("load schema: %w", err)
	}
	metrics.RecordSchemaLoad("v1", true)
	log.Printf("Loaded schemas: %v", reg.Namespaces())

	// Create pipeline.
	pipe := pipeline.NewChannelPipeline(256)
	defer pipe.Close()

	// Create normalization engine.
	engine := normalizer.NewEngine(reg)

	// Create Prometheus output writer.
	writer := promwriter.NewWriter(cfg.Output.Prometheus.Listen)
	defer writer.Close()

	// Register self-telemetry on the writer's Prometheus registry so all metrics
	// (5G KPIs + Argus internals) are served from the same /metrics endpoint.
	metrics.Register(writer.Registry())

	// Build the fan-out writers slice. Prometheus is always present;
	// OTLP is added when configured.
	var writers []output.Writer
	writers = append(writers, writer)

	if cfg.Output.OTLP != nil {
		otlpCfg := otlpwriter.Config{
			Endpoint:      cfg.Output.OTLP.Endpoint,
			Insecure:      cfg.Output.OTLP.Insecure,
			Headers:       cfg.Output.OTLP.Headers,
			BatchInterval: cfg.Output.OTLP.BatchInterval.Duration,
			BatchSize:     cfg.Output.OTLP.BatchSize,
		}
		ow, err := otlpwriter.NewWriter(otlpCfg)
		if err != nil {
			return fmt.Errorf("create OTLP writer: %w", err)
		}
		defer ow.Close()
		writers = append(writers, ow)
		log.Printf("OTLP output configured: %s", cfg.Output.OTLP.Endpoint)
	}

	// Start Prometheus HTTP server.
	go func() {
		log.Printf("Prometheus output listening on %s", cfg.Output.Prometheus.Listen)
		if err := writer.Start(); err != nil {
			log.Printf("prometheus writer: %v", err)
		}
	}()

	// Allow server to bind before collectors start publishing.
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start collectors.
	for _, entry := range cfg.Collectors {
		c, err := collector.DefaultRegistry.Get(entry.Name)
		if err != nil {
			return fmt.Errorf("collector %q: %w", entry.Name, err)
		}

		collCfg := collector.CollectorConfig{
			Endpoint: entry.Endpoint,
			Interval: entry.Interval.Duration,
			Extra:    entry.Extra,
		}

		if err := c.Connect(ctx, collCfg); err != nil {
			return fmt.Errorf("connect collector %q: %w", entry.Name, err)
		}

		collectorName := c.Name()
		ch := make(chan collector.RawRecord, 64)

		// Collector goroutine: scrapes the NF endpoint on interval and emits RawRecords.
		go func() {
			start := time.Now()
			err := c.Collect(ctx, ch)
			if err != nil && ctx.Err() == nil {
				log.Printf("collector %s stopped: %v", collectorName, err)
				metrics.RecordScrape(collectorName, time.Since(start), false)
			}
		}()

		// Publisher goroutine: marshals RawRecords to JSON and publishes to the "raw" topic.
		go func() {
			for rec := range ch {
				data, err := json.Marshal(rec)
				if err != nil {
					log.Printf("marshal raw record: %v", err)
					continue
				}
				metrics.RecordScrape(collectorName, 0, true)
				metrics.CollectorRecordsTotal.WithLabelValues(collectorName).Inc()
				if err := pipe.Publish(ctx, "raw", data); err != nil {
					log.Printf("publish raw: %v", err)
				}
				metrics.RecordPublish("raw", 1)
			}
		}()
	}

	// Subscribe to raw topic for normalization.
	rawCh, err := pipe.Subscribe(ctx, "raw")
	if err != nil {
		return fmt.Errorf("subscribe raw: %w", err)
	}

	// Normalizer goroutine: deserializes RawRecords, normalizes via schema engine,
	// and publishes NormalizedRecords to the "normalized" topic.
	go func() {
		for data := range rawCh {
			var rec collector.RawRecord
			if err := json.Unmarshal(data, &rec); err != nil {
				log.Printf("unmarshal raw record: %v", err)
				continue
			}

			if !engine.CanHandle(rec) {
				metrics.NormalizerTotalFails.WithLabelValues(rec.Source.Vendor, rec.Source.NFType).Inc()
				continue
			}

			result, err := engine.Normalize(rec)
			if err != nil {
				log.Printf("normalize: %v", err)
				metrics.NormalizerTotalFails.WithLabelValues(rec.Source.Vendor, rec.Source.NFType).Inc()
				continue
			}

			metrics.RecordNormalize(rec.Source.Vendor, rec.Source.NFType, len(result.Records), len(result.Partial))

			if len(result.Records) > 0 {
				data, err := json.Marshal(result.Records)
				if err != nil {
					log.Printf("marshal normalized records: %v", err)
					continue
				}
				if err := pipe.Publish(ctx, "normalized", data); err != nil {
					log.Printf("publish normalized: %v", err)
				}
				metrics.RecordPublish("normalized", 1)
			}
		}
	}()

	// Subscribe to normalized topic for output.
	normCh, err := pipe.Subscribe(ctx, "normalized")
	if err != nil {
		return fmt.Errorf("subscribe normalized: %w", err)
	}

	// Output goroutine: deserializes NormalizedRecords and fans out to all writers.
	go func() {
		for data := range normCh {
			var records []normalizer.NormalizedRecord
			if err := json.Unmarshal(data, &records); err != nil {
				log.Printf("unmarshal normalized records: %v", err)
				continue
			}

			for _, w := range writers {
				err := w.Write(ctx, records)
				metrics.RecordWrite(w.Name(), len(records), err)
				if err != nil {
					log.Printf("write to %s: %v", w.Name(), err)
				}
			}
		}
	}()

	// Block until SIGINT or SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("Received %s, shutting down...", sig)

	cancel()

	// Graceful shutdown: flush all writers before exiting.
	// Bound the flush to 10s so a stuck OTLP collector cannot block shutdown indefinitely.
	flushCtx, flushCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer flushCancel()
	for _, w := range writers {
		if err := w.Flush(flushCtx); err != nil {
			log.Printf("flush %s: %v", w.Name(), err)
		}
	}

	return nil
}
