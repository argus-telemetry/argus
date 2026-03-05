package normalizer

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storeFactory creates a CounterStore for conformance testing.
// The cleanup func is called after the test completes.
type storeFactory struct {
	name   string
	create func(t *testing.T) CounterStore
	hasTTL bool // whether the store supports key expiry
}

func conformanceStores(t *testing.T) []storeFactory {
	t.Helper()
	return []storeFactory{
		{
			name: "memory",
			create: func(t *testing.T) CounterStore {
				return NewMemoryStore()
			},
		},
		{
			name: "bbolt",
			create: func(t *testing.T) CounterStore {
				dir := t.TempDir()
				s, err := NewBoltStore(filepath.Join(dir, "counters.db"))
				require.NoError(t, err)
				t.Cleanup(func() { s.Close() })
				return s
			},
		},
		{
			name:   "redis",
			hasTTL: true,
			create: func(t *testing.T) CounterStore {
				mr := miniredis.RunT(t)
				cfg := RedisStoreConfig{
					Addr:   mr.Addr(),
					KeyTTL: 10 * time.Second,
				}
				s, err := NewRedisStore(cfg)
				require.NoError(t, err)
				t.Cleanup(func() { s.Close() })
				return s
			},
		},
	}
}

func TestConformance_GetMissingKey(t *testing.T) {
	for _, sf := range conformanceStores(t) {
		t.Run(sf.name, func(t *testing.T) {
			s := sf.create(t)
			val, ok := s.Get("nonexistent", "kpi")
			assert.False(t, ok)
			assert.Equal(t, 0.0, val)
		})
	}
}

func TestConformance_SetAndGet(t *testing.T) {
	for _, sf := range conformanceStores(t) {
		t.Run(sf.name, func(t *testing.T) {
			s := sf.create(t)

			s.Put("src1", "kpi1", 42.0)
			val, ok := s.Get("src1", "kpi1")
			assert.True(t, ok)
			assert.Equal(t, 42.0, val)

			// Different source key is independent.
			_, ok = s.Get("src2", "kpi1")
			assert.False(t, ok)

			// Different KPI name is independent.
			_, ok = s.Get("src1", "kpi2")
			assert.False(t, ok)
		})
	}
}

func TestConformance_Overwrite(t *testing.T) {
	for _, sf := range conformanceStores(t) {
		t.Run(sf.name, func(t *testing.T) {
			s := sf.create(t)
			s.Put("src1", "kpi1", 10.0)
			s.Put("src1", "kpi1", 20.0)
			val, ok := s.Get("src1", "kpi1")
			assert.True(t, ok)
			assert.Equal(t, 20.0, val)
		})
	}
}

func TestConformance_CounterReset(t *testing.T) {
	for _, sf := range conformanceStores(t) {
		t.Run(sf.name, func(t *testing.T) {
			s := sf.create(t)

			// Simulate a counter that increments then resets (NF restart).
			s.Put("src1", "kpi1", 1000.0)
			s.Put("src1", "kpi1", 5.0) // reset — new epoch

			// The store always persists the latest value regardless of direction.
			val, ok := s.Get("src1", "kpi1")
			assert.True(t, ok)
			assert.Equal(t, 5.0, val)
		})
	}
}

func TestConformance_NegativeValues(t *testing.T) {
	for _, sf := range conformanceStores(t) {
		t.Run(sf.name, func(t *testing.T) {
			s := sf.create(t)
			s.Put("src", "interference", -95.5)
			val, ok := s.Get("src", "interference")
			assert.True(t, ok)
			assert.Equal(t, -95.5, val)
		})
	}
}

func TestConformance_MultipleSourceKeys(t *testing.T) {
	for _, sf := range conformanceStores(t) {
		t.Run(sf.name, func(t *testing.T) {
			s := sf.create(t)
			s.Put("free5gc:AMF:amf-001", "reg.attempt", 100.0)
			s.Put("nokia:AMF:amf-002", "reg.attempt", 200.0)

			val1, ok := s.Get("free5gc:AMF:amf-001", "reg.attempt")
			assert.True(t, ok)
			assert.Equal(t, 100.0, val1)

			val2, ok := s.Get("nokia:AMF:amf-002", "reg.attempt")
			assert.True(t, ok)
			assert.Equal(t, 200.0, val2)
		})
	}
}

func TestConformance_Close(t *testing.T) {
	for _, sf := range conformanceStores(t) {
		t.Run(sf.name, func(t *testing.T) {
			s := sf.create(t)
			s.Put("src1", "kpi1", 42.0)
			err := s.Close()
			assert.NoError(t, err)
		})
	}
}
