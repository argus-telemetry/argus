// WorkerPool distributes RawRecords across N normalizer workers using
// consistent hash-based dispatch: hash(vendor + nfType + instanceID) % N.
// Same NF always routes to the same worker, preserving counter state locality.
package normalizer

import (
	"fmt"
	"hash/fnv"
	"sync"

	"github.com/argus-5g/argus/internal/collector"
	"github.com/argus-5g/argus/internal/telemetry"
	"github.com/prometheus/client_golang/prometheus"
)

// PoolConfig configures the worker pool.
type PoolConfig struct {
	WorkerCount int `yaml:"worker_count"` // number of parallel normalizer workers
	QueueDepth  int `yaml:"queue_depth"`  // per-worker channel buffer size
}

// WorkerPool dispatches RawRecords to a fixed set of normalizer workers.
type WorkerPool struct {
	workers  []chan collector.RawRecord
	engine   *Engine
	count    int
	wg       sync.WaitGroup
	metrics  *telemetry.Metrics
	poolMetrics *poolMetrics
}

type poolMetrics struct {
	queueDepth    *prometheus.GaugeVec
	recordsTotal  *prometheus.CounterVec
	dispatchSkew  prometheus.Gauge
}

func newPoolMetrics() *poolMetrics {
	return &poolMetrics{
		queueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "argus_normalizer_worker_queue_depth",
			Help: "Current queue depth per normalizer worker.",
		}, []string{"worker_id"}),
		recordsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "argus_normalizer_worker_records_total",
			Help: "Total records processed per normalizer worker.",
		}, []string{"worker_id"}),
		dispatchSkew: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "argus_normalizer_worker_dispatch_skew",
			Help: "Max queue depth minus min queue depth across workers. High values indicate poor hash distribution.",
		}),
	}
}

func (pm *poolMetrics) register(reg prometheus.Registerer) {
	reg.MustRegister(pm.queueDepth, pm.recordsTotal, pm.dispatchSkew)
}

// NewWorkerPool creates a pool of N normalizer workers. Each worker has its own
// buffered input channel and runs engine.Normalize() independently.
//
// Callers must call Start() to begin processing and Stop() to drain workers.
func NewWorkerPool(engine *Engine, cfg PoolConfig) *WorkerPool {
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 1
	}
	if cfg.QueueDepth <= 0 {
		cfg.QueueDepth = 256
	}

	pool := &WorkerPool{
		workers:     make([]chan collector.RawRecord, cfg.WorkerCount),
		engine:      engine,
		count:       cfg.WorkerCount,
		poolMetrics: newPoolMetrics(),
	}
	for i := range pool.workers {
		pool.workers[i] = make(chan collector.RawRecord, cfg.QueueDepth)
	}
	return pool
}

// RegisterMetrics registers worker pool metrics with the given registerer.
func (p *WorkerPool) RegisterMetrics(reg prometheus.Registerer) {
	p.poolMetrics.register(reg)
}

// SetMetrics attaches telemetry metrics for recording normalization results.
func (p *WorkerPool) SetMetrics(m *telemetry.Metrics) {
	p.metrics = m
}

// Start launches all worker goroutines. Each worker drains its channel,
// normalizes records, and invokes the callback with results.
func (p *WorkerPool) Start(onResult func(rec collector.RawRecord, result NormalizeResult)) {
	for i := 0; i < p.count; i++ {
		p.wg.Add(1)
		go p.runWorker(i, onResult)
	}
}

// Dispatch routes a RawRecord to the appropriate worker based on consistent
// hash of vendor + NFType + instanceID.
func (p *WorkerPool) Dispatch(rec collector.RawRecord) {
	idx := p.workerIndex(rec.Source)
	workerID := fmt.Sprintf("%d", idx)

	p.workers[idx] <- rec

	// Update queue depth metric after send.
	p.poolMetrics.queueDepth.WithLabelValues(workerID).Set(float64(len(p.workers[idx])))
	p.updateSkew()
}

// Stop closes all worker channels and waits for workers to drain.
func (p *WorkerPool) Stop() {
	for _, ch := range p.workers {
		close(ch)
	}
	p.wg.Wait()
}

// WorkerCount returns the number of workers in the pool.
func (p *WorkerPool) WorkerCount() int {
	return p.count
}

func (p *WorkerPool) runWorker(id int, onResult func(collector.RawRecord, NormalizeResult)) {
	defer p.wg.Done()
	workerID := fmt.Sprintf("%d", id)

	for rec := range p.workers[id] {
		p.poolMetrics.queueDepth.WithLabelValues(workerID).Set(float64(len(p.workers[id])))

		if !p.engine.CanHandle(rec) {
			if p.metrics != nil {
				p.metrics.NormalizerTotalFails.WithLabelValues(rec.Source.Vendor, rec.Source.NFType).Inc()
			}
			continue
		}

		result, err := p.engine.Normalize(rec)
		if err != nil {
			if p.metrics != nil {
				p.metrics.NormalizerTotalFails.WithLabelValues(rec.Source.Vendor, rec.Source.NFType).Inc()
			}
			continue
		}

		p.poolMetrics.recordsTotal.WithLabelValues(workerID).Inc()
		if p.metrics != nil {
			p.metrics.RecordNormalize(rec.Source.Vendor, rec.Source.NFType, len(result.Records), len(result.Partial))
		}

		if onResult != nil {
			onResult(rec, result)
		}
	}
}

// workerIndex computes the target worker for a given source using FNV-1a hash.
func (p *WorkerPool) workerIndex(src collector.SourceInfo) int {
	h := fnv.New32a()
	h.Write([]byte(src.Vendor))
	h.Write([]byte(src.NFType))
	h.Write([]byte(src.InstanceID))
	return int(h.Sum32()) % p.count
}

func (p *WorkerPool) updateSkew() {
	if p.count <= 1 {
		return
	}
	min, max := len(p.workers[0]), len(p.workers[0])
	for i := 1; i < p.count; i++ {
		depth := len(p.workers[i])
		if depth < min {
			min = depth
		}
		if depth > max {
			max = depth
		}
	}
	p.poolMetrics.dispatchSkew.Set(float64(max - min))
}

// ValidateStoreForWorkerCount fails fast if multi-worker is configured with
// a non-concurrent-safe store backend.
func ValidateStoreForWorkerCount(storeType string, workerCount int) error {
	if workerCount > 1 && storeType != "redis" {
		return fmt.Errorf(
			"multi-worker normalization requires store.type=redis; "+
				"bbolt and memory stores are single-writer only (worker_count=%d, store.type=%s)",
			workerCount, storeType)
	}
	return nil
}
