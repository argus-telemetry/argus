package normalizer

// CounterStore persists per-source counter values between scrapes for delta
// computation. Get/Put are called under the Engine's mutex; implementations
// do not need internal synchronization.
type CounterStore interface {
	// Get returns the last stored counter value for a source+KPI pair.
	// Returns (0, false) if no previous value exists.
	Get(sourceKey, kpiName string) (float64, bool)

	// Put stores the current counter value for a source+KPI pair.
	Put(sourceKey, kpiName string, value float64)

	// Close releases resources. For persistent stores, ensures all data
	// is flushed to disk.
	Close() error
}
