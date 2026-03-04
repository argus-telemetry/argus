package normalizer

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/argus-5g/argus/internal/telemetry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMiniRedisStore(t *testing.T, opts ...RedisStoreOption) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	cfg := RedisStoreConfig{
		Addr:   mr.Addr(),
		KeyTTL: 120 * time.Second,
	}
	rs, err := NewRedisStore(cfg, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { rs.Close() })
	return rs, mr
}

func TestRedisStore_DeltaCalculation(t *testing.T) {
	rs, _ := newMiniRedisStore(t)

	// First Put: no previous value.
	_, ok := rs.Get("free5gc:AMF:amf-001", "registration.attempt_count")
	assert.False(t, ok, "empty store returns false")

	// Put a value, then Get it back.
	rs.Put("free5gc:AMF:amf-001", "registration.attempt_count", 100.0)
	val, ok := rs.Get("free5gc:AMF:amf-001", "registration.attempt_count")
	assert.True(t, ok)
	assert.Equal(t, 100.0, val)

	// Put a higher value — delta = 150 - 100 = 50 (computed by engine, not store).
	rs.Put("free5gc:AMF:amf-001", "registration.attempt_count", 150.0)
	val, ok = rs.Get("free5gc:AMF:amf-001", "registration.attempt_count")
	assert.True(t, ok)
	assert.Equal(t, 150.0, val)
}

func TestRedisStore_CounterReset(t *testing.T) {
	m := newTestMetrics(t)
	rs, _ := newMiniRedisStore(t, WithRedisMetrics(m))

	// Establish a high counter value.
	rs.Put("nokia:AMF:amf-001", "registration.attempt_count", 5000.0)

	// Counter reset: value drops (e.g., NF restart or Nokia midnight UTC reset).
	rs.Put("nokia:AMF:amf-001", "registration.attempt_count", 10.0)

	// The store persists the new value.
	val, ok := rs.Get("nokia:AMF:amf-001", "registration.attempt_count")
	assert.True(t, ok)
	assert.Equal(t, 10.0, val)

	// Counter reset metric incremented.
	resets := testutil.ToFloat64(m.CounterResetTotal.WithLabelValues("nokia", "AMF"))
	assert.Equal(t, 1.0, resets)
}

func TestRedisStore_OutOfOrder(t *testing.T) {
	// OOO detection requires timestamp context not available in the CounterStore
	// interface. The OOO metric is wired at the worker pool level (P2).
	// This test verifies the metric vector exists and is zero for a normal sequence.
	m := newTestMetrics(t)
	rs, _ := newMiniRedisStore(t, WithRedisMetrics(m))

	rs.Put("free5gc:AMF:amf-001", "kpi1", 10.0)
	rs.Put("free5gc:AMF:amf-001", "kpi1", 20.0)

	ooo := testutil.ToFloat64(m.CounterOOOTotal.WithLabelValues("free5gc", "AMF"))
	assert.Equal(t, 0.0, ooo)
}

func TestRedisStore_LuaScriptLoad(t *testing.T) {
	rs, _ := newMiniRedisStore(t)

	// Verify EVALSHA is used (scriptSHA is set on Open).
	assert.NotEmpty(t, rs.ScriptSHA(), "Lua script SHA should be loaded on Open")

	// Put uses EVALSHA, not EVAL — verify by checking the value is stored correctly.
	rs.Put("src1", "kpi1", 42.0)
	val, ok := rs.Get("src1", "kpi1")
	assert.True(t, ok)
	assert.Equal(t, 42.0, val)
}

func TestRedisStore_KeyExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	cfg := RedisStoreConfig{
		Addr:   mr.Addr(),
		KeyTTL: 10 * time.Second,
	}
	rs, err := NewRedisStore(cfg)
	require.NoError(t, err)
	defer rs.Close()

	rs.Put("src1", "kpi1", 100.0)

	// Key exists with TTL.
	ttl := mr.TTL(redisKey("src1", "kpi1"))
	assert.Equal(t, 10*time.Second, ttl)

	// Fast-forward past TTL.
	mr.FastForward(11 * time.Second)

	_, ok := rs.Get("src1", "kpi1")
	assert.False(t, ok, "key should have expired after TTL")
}

func TestRedisStore_SatisfiesInterface(t *testing.T) {
	// Compile-time check that RedisStore satisfies CounterStore.
	var _ CounterStore = (*RedisStore)(nil)
}

func TestRedisStore_NegativeValues(t *testing.T) {
	rs, _ := newMiniRedisStore(t)
	rs.Put("src", "interference", -95.5)
	val, ok := rs.Get("src", "interference")
	assert.True(t, ok)
	assert.Equal(t, -95.5, val)
}

func TestRedisStore_Overwrite(t *testing.T) {
	rs, _ := newMiniRedisStore(t)
	rs.Put("src1", "kpi1", 10.0)
	rs.Put("src1", "kpi1", 20.0)
	val, ok := rs.Get("src1", "kpi1")
	assert.True(t, ok)
	assert.Equal(t, 20.0, val)
}

func TestRedisStore_IndependentKeys(t *testing.T) {
	rs, _ := newMiniRedisStore(t)
	rs.Put("src1", "kpi1", 42.0)
	_, ok := rs.Get("src2", "kpi1")
	assert.False(t, ok, "different source key is independent")
}

func TestRedisStore_ConnectionFailure(t *testing.T) {
	m := newTestMetrics(t)
	cfg := RedisStoreConfig{
		Addr:        "localhost:1", // unreachable
		DialTimeout: 100 * time.Millisecond,
	}
	_, err := NewRedisStore(cfg, WithRedisMetrics(m))
	assert.Error(t, err)
	openErrors := testutil.ToFloat64(m.CounterStoreErrors.WithLabelValues("redis", "open"))
	assert.Equal(t, 1.0, openErrors)
}

func TestRedisStore_PersistedMetric(t *testing.T) {
	m := newTestMetrics(t)
	rs, _ := newMiniRedisStore(t, WithRedisMetrics(m))

	rs.Put("src1", "kpi1", 10.0)
	rs.Put("src1", "kpi2", 20.0)

	persisted := testutil.ToFloat64(m.CounterStatePersisted.WithLabelValues("redis"))
	assert.Equal(t, 2.0, persisted)
}

func TestRedisStore_LuaEvalDuration(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := telemetry.NewMetrics()
	m.Register(reg)

	rs, _ := newMiniRedisStore(t, WithRedisMetrics(m))
	rs.Put("src1", "kpi1", 10.0)

	// Histogram should have at least one observation.
	mfs, err := reg.Gather()
	require.NoError(t, err)
	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "argus_counter_lua_eval_duration_seconds" {
			for _, metric := range mf.GetMetric() {
				if metric.GetHistogram().GetSampleCount() > 0 {
					found = true
				}
			}
		}
	}
	assert.True(t, found, "expected at least one Lua eval duration observation")
}

func TestParseSourceKeyLabels(t *testing.T) {
	tests := []struct {
		sourceKey string
		vendor    string
		nfType    string
	}{
		{"nokia:AMF:amf-001", "nokia", "AMF"},
		{"free5gc:SMF:smf-001", "free5gc", "SMF"},
		{"singlepart", "unknown", "unknown"},
		{"vendor:nftype", "vendor", "unknown"},
	}
	for _, tt := range tests {
		vendor, nfType := parseSourceKeyLabels(tt.sourceKey)
		assert.Equal(t, tt.vendor, vendor, "sourceKey=%s", tt.sourceKey)
		assert.Equal(t, tt.nfType, nfType, "sourceKey=%s", tt.sourceKey)
	}
}
