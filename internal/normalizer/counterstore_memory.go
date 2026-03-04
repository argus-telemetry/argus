package normalizer

// MemoryStore is a volatile in-memory CounterStore. State is lost on restart.
// Used as the default when no persistence path is configured.
type MemoryStore struct {
	state map[string]map[string]float64
}

// NewMemoryStore creates an empty in-memory counter store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{state: make(map[string]map[string]float64)}
}

func (m *MemoryStore) Get(sourceKey, kpiName string) (float64, bool) {
	inner, ok := m.state[sourceKey]
	if !ok {
		return 0, false
	}
	val, ok := inner[kpiName]
	return val, ok
}

func (m *MemoryStore) Put(sourceKey, kpiName string, value float64) {
	if m.state[sourceKey] == nil {
		m.state[sourceKey] = make(map[string]float64)
	}
	m.state[sourceKey][kpiName] = value
}

func (m *MemoryStore) Close() error { return nil }
