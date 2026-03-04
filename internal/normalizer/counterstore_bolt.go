package normalizer

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	bolt "go.etcd.io/bbolt"
)

// BoltStore persists counter values in a bbolt database, surviving process restarts.
// Maintains an in-memory cache for O(1) reads; writes go through to bbolt for durability.
// On startup, existing state is loaded from disk into the cache.
type BoltStore struct {
	db    *bolt.DB
	cache map[string]map[string]float64
}

// NewBoltStore opens (or creates) a bbolt database at path and loads existing
// counter state into memory. The file is created with 0600 permissions.
func NewBoltStore(path string) (*BoltStore, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open bbolt %s: %w", path, err)
	}

	cache := make(map[string]map[string]float64)

	// Load all existing state into the in-memory cache.
	err = db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			sk := string(name)
			cache[sk] = make(map[string]float64)
			return b.ForEach(func(k, v []byte) error {
				if len(v) != 8 {
					return nil // skip malformed entries
				}
				cache[sk][string(k)] = math.Float64frombits(binary.BigEndian.Uint64(v))
				return nil
			})
		})
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("load bbolt state: %w", err)
	}

	return &BoltStore{db: db, cache: cache}, nil
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
	_ = b.db.Update(func(tx *bolt.Tx) error {
		bkt, err := tx.CreateBucketIfNotExists([]byte(sourceKey))
		if err != nil {
			return err
		}
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], math.Float64bits(value))
		return bkt.Put([]byte(kpiName), buf[:])
	})
}

func (b *BoltStore) Close() error {
	return b.db.Close()
}
