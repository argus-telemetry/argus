package collector

import (
	"fmt"
	"sort"
	"sync"
)

// CollectorFactory is a constructor that returns a zero-value Collector.
// The returned Collector is not yet connected — callers must invoke Connect before Collect.
type CollectorFactory func() Collector

// Registry is a thread-safe store of named collector factories.
// Plugins register themselves at init-time; the pipeline retrieves them by name at startup.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]CollectorFactory
}

// DefaultRegistry is the global collector registry. Plugins register via init().
var DefaultRegistry = NewRegistry()

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]CollectorFactory),
	}
}

// Register adds a collector factory under the given name.
// If a factory with the same name already exists, it is silently overwritten.
func (r *Registry) Register(name string, f CollectorFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = f
}

// Get instantiates a new Collector from the factory registered under name.
// Returns an error if no factory is registered for that name.
func (r *Registry) Get(name string) (Collector, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.factories[name]
	if !ok {
		return nil, fmt.Errorf("collector %q not registered", name)
	}
	return f(), nil
}

// List returns the names of all registered collector factories in sorted order.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
