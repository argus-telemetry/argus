package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Registry indexes loaded NF schemas for fast lookup by namespace, vendor, and KPI name.
// Built once at startup via LoadFromDir; all accessors are read-only after construction.
type Registry struct {
	schemas   map[string]*NFSchema // namespace → schema
	evalOrder map[string][]string  // namespace → KPI names in dependency-safe evaluation order
}

// LoadFromDir reads all *.yaml files from dir, parses them, validates,
// and builds the registry. Returns error on validation failure (missing spec_ref,
// circular dependencies, dangling depends_on references, derived KPI without formula).
func LoadFromDir(dir string) (*Registry, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("glob schema dir %s: %w", dir, err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no *.yaml files found in %s", dir)
	}

	r := &Registry{
		schemas:   make(map[string]*NFSchema),
		evalOrder: make(map[string][]string),
	}

	for _, path := range matches {
		s, err := loadSchemaFile(path)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", filepath.Base(path), err)
		}
		if err := validate(s); err != nil {
			return nil, fmt.Errorf("validate %s: %w", filepath.Base(path), err)
		}
		order, err := topoSort(s)
		if err != nil {
			return nil, fmt.Errorf("dependency sort %s: %w", filepath.Base(path), err)
		}
		r.schemas[s.Namespace] = s
		r.evalOrder[s.Namespace] = order
	}

	return r, nil
}

// GetSchema returns the NFSchema for the given namespace.
func (r *Registry) GetSchema(namespace string) (*NFSchema, error) {
	s, ok := r.schemas[namespace]
	if !ok {
		return nil, fmt.Errorf("schema %q not found", namespace)
	}
	return s, nil
}

// GetKPI returns the KPIDefinition for the given namespace and KPI name.
func (r *Registry) GetKPI(namespace, kpiName string) (*KPIDefinition, error) {
	s, err := r.GetSchema(namespace)
	if err != nil {
		return nil, err
	}
	for i := range s.KPIs {
		if s.KPIs[i].Name == kpiName {
			return &s.KPIs[i], nil
		}
	}
	return nil, fmt.Errorf("KPI %q not found in namespace %q", kpiName, namespace)
}

// GetMapping returns the MetricMapping for a vendor's metric mapped to a KPI.
func (r *Registry) GetMapping(namespace, vendor, kpiName string) (*MetricMapping, error) {
	s, err := r.GetSchema(namespace)
	if err != nil {
		return nil, err
	}
	vm, ok := s.Mappings[vendor]
	if !ok {
		return nil, fmt.Errorf("vendor %q not found in namespace %q", vendor, namespace)
	}
	mm, ok := vm.Metrics[kpiName]
	if !ok {
		return nil, fmt.Errorf("mapping for KPI %q not found for vendor %q in namespace %q", kpiName, vendor, namespace)
	}
	return &mm, nil
}

// EvaluationOrder returns KPI names in dependency-safe order (topological sort).
// Base KPIs come before derived KPIs that depend on them. Returns nil if the
// namespace is not loaded.
func (r *Registry) EvaluationOrder(namespace string) []string {
	return r.evalOrder[namespace]
}

// Namespaces returns all loaded namespace names in sorted order.
func (r *Registry) Namespaces() []string {
	ns := make([]string, 0, len(r.schemas))
	for k := range r.schemas {
		ns = append(ns, k)
	}
	sort.Strings(ns)
	return ns
}

// loadSchemaFile reads and unmarshals a single YAML schema file.
func loadSchemaFile(path string) (*NFSchema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	var s NFSchema
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("unmarshal YAML: %w", err)
	}
	return &s, nil
}

// validate checks structural invariants on a parsed NFSchema:
//   - every KPI must have a non-empty spec_ref
//   - every derived KPI must have a non-empty formula and non-empty depends_on
//   - every depends_on reference must point to an existing KPI in the same schema
func validate(s *NFSchema) error {
	kpiSet := make(map[string]struct{}, len(s.KPIs))
	for _, k := range s.KPIs {
		kpiSet[k.Name] = struct{}{}
	}

	for _, k := range s.KPIs {
		if k.SpecRef == "" {
			return fmt.Errorf("KPI %q: spec_ref is required", k.Name)
		}
		if k.Derived {
			if k.Formula == "" {
				return fmt.Errorf("KPI %q: derived KPI must have a formula", k.Name)
			}
			if len(k.DependsOn) == 0 {
				return fmt.Errorf("KPI %q: derived KPI must have depends_on", k.Name)
			}
			for _, dep := range k.DependsOn {
				if _, ok := kpiSet[dep]; !ok {
					return fmt.Errorf("KPI %q: depends_on references nonexistent KPI %q", k.Name, dep)
				}
			}
		}
	}
	return nil
}

// topoSort produces a dependency-safe evaluation order for the KPIs in a schema.
// Uses DFS-based topological sort. Returns an error with the cycle path if
// circular dependencies are detected.
func topoSort(s *NFSchema) ([]string, error) {
	// Build adjacency: KPI name → list of KPIs it depends on.
	deps := make(map[string][]string, len(s.KPIs))
	for _, k := range s.KPIs {
		deps[k.Name] = k.DependsOn
	}

	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)

	state := make(map[string]int, len(s.KPIs))
	var order []string

	// path tracks the current DFS stack for cycle reporting.
	var path []string

	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case visited:
			return nil
		case visiting:
			// Build cycle description from path.
			cycleStart := -1
			for i, p := range path {
				if p == name {
					cycleStart = i
					break
				}
			}
			cycle := append(path[cycleStart:], name)
			return fmt.Errorf("circular dependency: %s", strings.Join(cycle, " -> "))
		}

		state[name] = visiting
		path = append(path, name)

		for _, dep := range deps[name] {
			if err := visit(dep); err != nil {
				return err
			}
		}

		path = path[:len(path)-1]
		state[name] = visited
		order = append(order, name)
		return nil
	}

	// Visit all KPIs in definition order for deterministic output.
	for _, k := range s.KPIs {
		if err := visit(k.Name); err != nil {
			return nil, err
		}
	}

	return order, nil
}
