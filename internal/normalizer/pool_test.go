package normalizer

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/argus-5g/argus/internal/collector"
	"github.com/argus-5g/argus/internal/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPoolTestRegistry(t *testing.T) *schema.Registry {
	t.Helper()
	reg, err := schema.LoadFromDir("../../schema/v1")
	require.NoError(t, err)
	return reg
}

func newTestPool(t *testing.T, workerCount int) *WorkerPool {
	t.Helper()
	reg := newPoolTestRegistry(t)
	engine := NewEngine(reg, NewMemoryStore())
	pool := NewWorkerPool(engine, PoolConfig{
		WorkerCount: workerCount,
		QueueDepth:  64,
	})
	return pool
}

func makeRawRecord(vendor, nfType, instanceID string) collector.RawRecord {
	return collector.RawRecord{
		Source: collector.SourceInfo{
			Vendor:     vendor,
			NFType:     "AMF",
			InstanceID: instanceID,
		},
		Protocol:  collector.ProtocolPrometheus,
		Timestamp: time.Now(),
		Payload: []byte(`# HELP fivegs_amffunction_rm_registeredsubnbr Number of registered subs
# TYPE fivegs_amffunction_rm_registeredsubnbr gauge
fivegs_amffunction_rm_registeredsubnbr 42
`),
		SchemaVersion: "v1",
	}
}

func TestWorkerPool_SameNFRoutesSameWorker(t *testing.T) {
	pool := newTestPool(t, 4)

	src := collector.SourceInfo{Vendor: "free5gc", NFType: "AMF", InstanceID: "amf-001"}
	idx1 := pool.workerIndex(src)
	idx2 := pool.workerIndex(src)
	idx3 := pool.workerIndex(src)
	assert.Equal(t, idx1, idx2)
	assert.Equal(t, idx2, idx3)

	// Different instance returns a valid index.
	src2 := collector.SourceInfo{Vendor: "free5gc", NFType: "AMF", InstanceID: "amf-002"}
	idx := pool.workerIndex(src2)
	assert.GreaterOrEqual(t, idx, 0)
	assert.Less(t, idx, 4)
}

func TestWorkerPool_DistributionSkew(t *testing.T) {
	pool := newTestPool(t, 8)

	buckets := make([]int, 8)
	for i := 0; i < 100; i++ {
		src := collector.SourceInfo{
			Vendor:     "free5gc",
			NFType:     "AMF",
			InstanceID: fmt.Sprintf("amf-%03d", i),
		}
		idx := pool.workerIndex(src)
		buckets[idx]++
	}

	min, max := buckets[0], buckets[0]
	for _, b := range buckets {
		if b < min {
			min = b
		}
		if b > max {
			max = b
		}
	}
	assert.Less(t, max-min, 25, "hash distribution skew too high: min=%d max=%d buckets=%v", min, max, buckets)
}

func TestWorkerPool_SingleWorkerBackwardsCompat(t *testing.T) {
	pool := newTestPool(t, 1)

	var mu sync.Mutex
	var count int

	pool.Start(func(rec collector.RawRecord, result NormalizeResult) {
		mu.Lock()
		count++
		mu.Unlock()
	})

	pool.Dispatch(makeRawRecord("free5gc", "AMF", "amf-001"))
	pool.Dispatch(makeRawRecord("free5gc", "AMF", "amf-002"))

	pool.Stop()

	mu.Lock()
	assert.Equal(t, 2, count, "single worker should process all records")
	mu.Unlock()
}

func TestWorkerPool_MultiWorkerRequiresRedis(t *testing.T) {
	err := ValidateStoreForWorkerCount("memory", 4)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "multi-worker normalization requires store.type=redis")

	err = ValidateStoreForWorkerCount("bbolt", 2)
	assert.Error(t, err)

	err = ValidateStoreForWorkerCount("redis", 4)
	assert.NoError(t, err)

	err = ValidateStoreForWorkerCount("memory", 1)
	assert.NoError(t, err)

	err = ValidateStoreForWorkerCount("bbolt", 1)
	assert.NoError(t, err)
}

func TestWorkerPool_MultiWorkerProcesses(t *testing.T) {
	pool := newTestPool(t, 4)

	var mu sync.Mutex
	var count int

	pool.Start(func(rec collector.RawRecord, result NormalizeResult) {
		mu.Lock()
		count++
		mu.Unlock()
	})

	for i := 0; i < 20; i++ {
		pool.Dispatch(makeRawRecord("free5gc", "AMF", fmt.Sprintf("amf-%03d", i)))
	}

	pool.Stop()

	mu.Lock()
	assert.Equal(t, 20, count, "all records should be processed across workers")
	mu.Unlock()
}
