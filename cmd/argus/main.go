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
	"github.com/argus-5g/argus/internal/correlator"
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
	defer func() { _ = pipe.Close() }()

	// Create counter store for delta computation persistence.
	counterStore, err := openCounterStore(cfg, metrics)
	if err != nil {
		return fmt.Errorf("open counter store: %w", err)
	}
	if counterStore != nil {
		defer func() { _ = counterStore.Close() }()
	}

	// Validate store+worker compatibility before creating engine.
	workerCount := cfg.Normalizer.WorkerCount
	if workerCount <= 0 {
		workerCount = 1
	}
	if err := normalizer.ValidateStoreForWorkerCount(cfg.Store.Type, workerCount); err != nil {
		return err
	}

	// Create normalization engine.
	engine := normalizer.NewEngine(reg, counterStore)

	// Create Prometheus output writer.
	writer := promwriter.NewWriter(cfg.Output.Prometheus.Listen)
	defer func() { _ = writer.Close() }()

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
		defer func() { _ = ow.Close() }()
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
			OnScrapeError: func(se collector.ScrapeError) {
				log.Printf("scrape error [%s/%s] (%s): %v", se.Vendor, se.NFType, se.Class, se.Err)
				metrics.RecordScrape(se.Collector, 0, false)
				metrics.RecordScrapeError(se.Vendor, se.NFType, string(se.Class))
			},
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

	// Create normalizer worker pool.
	queueDepth := cfg.Normalizer.QueueDepth
	if queueDepth <= 0 {
		queueDepth = 256
	}
	pool := normalizer.NewWorkerPool(engine, normalizer.PoolConfig{
		WorkerCount: workerCount,
		QueueDepth:  queueDepth,
	})
	pool.SetMetrics(metrics)
	pool.RegisterMetrics(writer.Registry())
	pool.Start(func(rec collector.RawRecord, result normalizer.NormalizeResult) {
		if len(result.Records) > 0 {
			data, err := json.Marshal(result.Records)
			if err != nil {
				log.Printf("marshal normalized records: %v", err)
				return
			}
			if err := pipe.Publish(ctx, "normalized", data); err != nil {
				log.Printf("publish normalized: %v", err)
			}
			metrics.RecordPublish("normalized", 1)
		}
	})
	defer pool.Stop()
	log.Printf("Normalizer: %d workers, queue_depth=%d", workerCount, queueDepth)

	// Subscribe to raw topic for normalization dispatch.
	rawCh, err := pipe.Subscribe(ctx, "raw")
	if err != nil {
		return fmt.Errorf("subscribe raw: %w", err)
	}

	// Dispatch goroutine: deserializes RawRecords and routes to worker pool.
	go func() {
		for data := range rawCh {
			var rec collector.RawRecord
			if err := json.Unmarshal(data, &rec); err != nil {
				log.Printf("unmarshal raw record: %v", err)
				continue
			}
			pool.Dispatch(rec)
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

	// Correlator: subscribe to normalized topic, evaluate rules on a ticker,
	// publish events to "correlation" topic and expose as Prometheus counters.
	if cfg.Correlator != nil {
		windowSize := cfg.Correlator.WindowSize.Duration
		if windowSize == 0 {
			windowSize = 30 * time.Second
		}
		evalInterval := cfg.Correlator.EvalInterval.Duration
		if evalInterval == 0 {
			evalInterval = 5 * time.Second
		}

		rules := []correlator.CorrelationRule{
			&correlator.RegistrationStorm{},
			&correlator.SessionDrop{},
			&correlator.RANCoreDivergence{},
		}
		corrEngine := correlator.NewEngine(windowSize, rules)

		corrCh, err := pipe.Subscribe(ctx, "normalized")
		if err != nil {
			return fmt.Errorf("subscribe normalized for correlator: %w", err)
		}

		// Ingest goroutine: feed normalized records into the correlator window.
		go func() {
			for data := range corrCh {
				var records []normalizer.NormalizedRecord
				if err := json.Unmarshal(data, &records); err != nil {
					continue
				}
				for _, rec := range records {
					corrEngine.Ingest(rec)
				}
			}
		}()

		// Evaluation goroutine: run rules on a ticker, publish events.
		go func() {
			ticker := time.NewTicker(evalInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					events := corrEngine.EvaluateAll(time.Now())
					metrics.CorrelatorEvaluations.WithLabelValues().Inc()
					for _, ev := range events {
						metrics.CorrelatorEventsTotal.WithLabelValues(ev.RuleName, ev.Severity).Inc()
						log.Printf("CORRELATION: %s [%s] plmn=%s affected=%v",
							ev.RuleName, ev.Severity, ev.PLMN, ev.AffectedNFs)
					}
					if len(events) > 0 {
						data, err := json.Marshal(events)
						if err != nil {
							continue
						}
						_ = pipe.Publish(ctx, "correlation", data)
						metrics.RecordPublish("correlation", 1)
					}
				}
			}
		}()

		log.Printf("Correlator enabled: window=%s eval_interval=%s", windowSize, evalInterval)
	}

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

// openCounterStore creates the configured counter store backend.
// Supports legacy counter_store_path for backwards compatibility.
func openCounterStore(cfg *Config, metrics *telemetry.Metrics) (normalizer.CounterStore, error) {
	storeType := cfg.Store.Type

	// Backwards compatibility: legacy counter_store_path maps to bbolt.
	if storeType == "" && cfg.CounterStorePath != "" {
		storeType = "bbolt"
		cfg.Store.BBolt.Path = cfg.CounterStorePath
	}

	switch storeType {
	case "redis":
		redisCfg := normalizer.RedisStoreConfig{
			Addr:         cfg.Store.Redis.Addr,
			Password:     cfg.Store.Redis.Password,
			DB:           cfg.Store.Redis.DB,
			KeyTTL:       cfg.Store.Redis.KeyTTL.Duration,
			DialTimeout:  cfg.Store.Redis.DialTimeout.Duration,
			ReadTimeout:  cfg.Store.Redis.ReadTimeout.Duration,
			WriteTimeout: cfg.Store.Redis.WriteTimeout.Duration,
			PoolSize:     cfg.Store.Redis.PoolSize,
		}
		rs, err := normalizer.NewRedisStore(redisCfg, normalizer.WithRedisMetrics(metrics))
		if err != nil {
			return nil, fmt.Errorf("redis store: %w", err)
		}
		log.Printf("Counter store: redis at %s", cfg.Store.Redis.Addr)
		return rs, nil

	case "bbolt":
		path := cfg.Store.BBolt.Path
		if path == "" {
			path = "/data/counters.db"
		}
		bs, err := normalizer.NewBoltStore(path, normalizer.WithMetrics(metrics))
		if err != nil {
			return nil, fmt.Errorf("bbolt store: %w", err)
		}
		log.Printf("Counter store: bbolt at %s", path)
		return bs, nil

	case "memory", "":
		log.Printf("Counter store: in-memory (volatile, state lost on restart)")
		return nil, nil // Engine defaults to MemoryStore when nil

	default:
		return nil, fmt.Errorf("unknown store type %q (supported: memory, bbolt, redis)", storeType)
	}
}
