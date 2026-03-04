package normalizer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/argus-5g/argus/internal/telemetry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStore_GetPut(t *testing.T) {
	s := NewMemoryStore()

	_, ok := s.Get("src1", "kpi1")
	assert.False(t, ok, "empty store returns false")

	s.Put("src1", "kpi1", 42.0)
	val, ok := s.Get("src1", "kpi1")
	assert.True(t, ok)
	assert.Equal(t, 42.0, val)

	// Different source key is independent.
	_, ok = s.Get("src2", "kpi1")
	assert.False(t, ok)
}

func TestMemoryStore_Overwrite(t *testing.T) {
	s := NewMemoryStore()
	s.Put("src1", "kpi1", 10.0)
	s.Put("src1", "kpi1", 20.0)

	val, ok := s.Get("src1", "kpi1")
	assert.True(t, ok)
	assert.Equal(t, 20.0, val)
}

func TestBoltStore_PersistAndRecover(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "counters.db")

	// Write some state.
	s1, err := NewBoltStore(dbPath)
	require.NoError(t, err)

	s1.Put("free5gc:AMF:amf-001", "registration.attempt_count", 5000.0)
	s1.Put("free5gc:AMF:amf-001", "registration.failure_count", 30.0)
	s1.Put("oai:GNB:gnb-001", "handover.attempt_count", 1200.0)

	require.NoError(t, s1.Close())

	// Reopen and verify state recovered.
	s2, err := NewBoltStore(dbPath)
	require.NoError(t, err)
	defer s2.Close()

	val, ok := s2.Get("free5gc:AMF:amf-001", "registration.attempt_count")
	assert.True(t, ok)
	assert.Equal(t, 5000.0, val)

	val, ok = s2.Get("free5gc:AMF:amf-001", "registration.failure_count")
	assert.True(t, ok)
	assert.Equal(t, 30.0, val)

	val, ok = s2.Get("oai:GNB:gnb-001", "handover.attempt_count")
	assert.True(t, ok)
	assert.Equal(t, 1200.0, val)

	// Non-existent key returns false.
	_, ok = s2.Get("oai:GNB:gnb-001", "nonexistent")
	assert.False(t, ok)
}

func TestBoltStore_NegativeValues(t *testing.T) {
	dir := t.TempDir()
	s, err := NewBoltStore(filepath.Join(dir, "counters.db"))
	require.NoError(t, err)
	defer s.Close()

	s.Put("src", "interference", -95.5)
	val, ok := s.Get("src", "interference")
	assert.True(t, ok)
	assert.Equal(t, -95.5, val)
}

func TestBoltStore_OpenFailure(t *testing.T) {
	// Attempt to open bbolt at a path that can't be created.
	_, err := NewBoltStore("/nonexistent/path/db")
	assert.Error(t, err)
}

func TestBoltStore_EmptyDB(t *testing.T) {
	dir := t.TempDir()
	s, err := NewBoltStore(filepath.Join(dir, "empty.db"))
	require.NoError(t, err)
	defer s.Close()

	_, ok := s.Get("anything", "anything")
	assert.False(t, ok)
}

func TestBoltStore_FileCreated(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "counters.db")

	s, err := NewBoltStore(dbPath)
	require.NoError(t, err)
	s.Close()

	_, err = os.Stat(dbPath)
	assert.NoError(t, err, "bbolt file should exist after creation")
}

func newTestMetrics(t *testing.T) *telemetry.Metrics {
	t.Helper()
	m := telemetry.NewMetrics()
	reg := prometheus.NewRegistry()
	m.Register(reg)
	return m
}

func TestBoltStore_RecoveredMetric_NonZero(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "counters.db")

	// Populate state without metrics.
	s1, err := NewBoltStore(dbPath)
	require.NoError(t, err)
	s1.Put("src1", "kpi1", 100.0)
	s1.Put("src1", "kpi2", 200.0)
	s1.Put("src2", "kpi1", 300.0)
	require.NoError(t, s1.Close())

	// Reopen with metrics — should report 3 recovered keys.
	m := newTestMetrics(t)
	s2, err := NewBoltStore(dbPath, WithMetrics(m))
	require.NoError(t, err)
	defer s2.Close()

	recovered := testutil.ToFloat64(m.CounterStateRecovered.WithLabelValues("bbolt"))
	assert.Equal(t, 3.0, recovered)
}

func TestBoltStore_RecoveredMetric_Zero(t *testing.T) {
	dir := t.TempDir()
	m := newTestMetrics(t)

	s, err := NewBoltStore(filepath.Join(dir, "empty.db"), WithMetrics(m))
	require.NoError(t, err)
	defer s.Close()

	recovered := testutil.ToFloat64(m.CounterStateRecovered.WithLabelValues("bbolt"))
	assert.Equal(t, 0.0, recovered)
}

func TestBoltStore_PersistedMetric(t *testing.T) {
	dir := t.TempDir()
	m := newTestMetrics(t)

	s, err := NewBoltStore(filepath.Join(dir, "counters.db"), WithMetrics(m))
	require.NoError(t, err)
	defer s.Close()

	s.Put("src1", "kpi1", 10.0)
	s.Put("src1", "kpi2", 20.0)

	persisted := testutil.ToFloat64(m.CounterStatePersisted.WithLabelValues("bbolt"))
	assert.Equal(t, 2.0, persisted)
}

func TestBoltStore_OpenErrorMetric(t *testing.T) {
	m := newTestMetrics(t)

	_, err := NewBoltStore("/nonexistent/path/db", WithMetrics(m))
	assert.Error(t, err)

	openErrors := testutil.ToFloat64(m.CounterStoreErrors.WithLabelValues("bbolt", "open"))
	assert.Equal(t, 1.0, openErrors)
}
