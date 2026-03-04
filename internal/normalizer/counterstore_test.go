package normalizer

import (
	"os"
	"path/filepath"
	"testing"

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
