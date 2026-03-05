// See docs/architecture/counter-store-evolution.md for the
// production Redis+WAL design and upgrade path.
package normalizer

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/argus-5g/argus/internal/telemetry"
)

// BoltStore persists counter values in a bbolt database, surviving process restarts.
// Maintains an in-memory cache for O(1) reads; writes go through to bbolt for durability.
// On startup, existing state is loaded from disk into the cache.
type BoltStore struct {
	db      *bolt.DB
	cache   map[string]map[string]float64
	metrics *telemetry.Metrics
}

// BoltStoreOption configures optional BoltStore behavior.
type BoltStoreOption func(*BoltStore)

// WithMetrics attaches telemetry metrics to the BoltStore for observability.
func WithMetrics(m *telemetry.Metrics) BoltStoreOption {
	return func(bs *BoltStore) {
		bs.metrics = m
	}
}

// NewBoltStore opens (or creates) a bbolt database at path and loads existing
// counter state into memory. The file is created with 0600 permissions.
func NewBoltStore(path string, opts ...BoltStoreOption) (*BoltStore, error) {
	bs := &BoltStore{
		cache: make(map[string]map[string]float64),
	}
	for _, opt := range opts {
		opt(bs)
	}

	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		if bs.metrics != nil {
			bs.metrics.CounterStoreErrors.WithLabelValues("bbolt", "open").Inc()
		}
		return nil, fmt.Errorf("open bbolt %s: %w", path, err)
	}
	bs.db = db

	// Load all existing state into the in-memory cache.
	var recovered int
	err = db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			sk := string(name)
			bs.cache[sk] = make(map[string]float64)
			return b.ForEach(func(k, v []byte) error {
				if len(v) != 8 {
					return nil // skip malformed entries
				}
				bs.cache[sk][string(k)] = math.Float64frombits(binary.BigEndian.Uint64(v))
				recovered++
				return nil
			})
		})
	})
	if err != nil {
		_ = db.Close()
		if bs.metrics != nil {
			bs.metrics.CounterStoreErrors.WithLabelValues("bbolt", "read").Inc()
		}
		return nil, fmt.Errorf("load bbolt state: %w", err)
	}

	if bs.metrics != nil {
		bs.metrics.CounterStateRecovered.WithLabelValues("bbolt").Set(float64(recovered))
	}

	return bs, nil
}

func (b *BoltStore) Get(sourceKey, kpiName string) (float64, bool) {
	inner, ok := b.cache[sourceKey]
	if !ok {
		return 0, false
	}
	val, ok := inner[kpiName]
	return val, ok
}

func (b *BoltStore) Put(sourceKey, kpiName string, value float64) {
	// Update in-memory cache.
	if b.cache[sourceKey] == nil {
		b.cache[sourceKey] = make(map[string]float64)
	}
	b.cache[sourceKey][kpiName] = value

	// Persist to bbolt.
	err := b.db.Update(func(tx *bolt.Tx) error {
		bkt, err := tx.CreateBucketIfNotExists([]byte(sourceKey))
		if err != nil {
			return err
		}
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], math.Float64bits(value))
		return bkt.Put([]byte(kpiName), buf[:])
	})
	if b.metrics != nil {
		if err != nil {
			b.metrics.CounterStoreErrors.WithLabelValues("bbolt", "write").Inc()
		} else {
			b.metrics.CounterStatePersisted.WithLabelValues("bbolt").Inc()
		}
	}
}

func (b *BoltStore) Close() error {
	return b.db.Close()
}
