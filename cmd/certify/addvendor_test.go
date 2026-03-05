package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/argus-5g/argus/internal/schema"
)

// minimalSchema returns a small schema YAML with two base KPIs and one derived.
func minimalSchema() string {
	return `namespace: argus.5g.amf
nf_type: AMF
schema_version: v1
spec: "3GPP TS 28.552"

kpis:
  - name: registration.attempt_count
    description: Registration attempts
    unit: count
    spec_ref: "3GPP TS 28.552 §5.1.1.1"
    derived: false

  - name: registration.success_rate
    description: Success rate
    unit: ratio
    spec_ref: "3GPP TS 28.552 §5.1.1.3"
    derived: true
    formula: "registration.attempt_count > 0 ? 1 : 0"
    depends_on:
      - registration.attempt_count

  - name: ue.connected_count
    description: Connected UEs
    unit: count
    spec_ref: "3GPP TS 28.552 §5.1.1.8"
    derived: false

mappings:
  existing_vendor:
    source_protocol: prometheus
    metrics:
      registration.attempt_count:
        prometheus_metric: existing_metric
        type: counter
        reset_aware: true
        label_match_strategy: exact
`
}

func TestAddVendor_GeneratesPlaceholders(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "amf.yaml")
	require.NoError(t, os.WriteFile(schemaPath, []byte(minimalSchema()), 0644))

	updated, err := addVendorToSchema(schemaPath, "test_vendor")
	require.NoError(t, err)
	assert.True(t, updated)

	// Parse the result and verify the new vendor mapping.
	data, err := os.ReadFile(schemaPath)
	require.NoError(t, err)

	var s schema.NFSchema
	require.NoError(t, yaml.Unmarshal(data, &s))

	vm, exists := s.Mappings["test_vendor"]
	require.True(t, exists, "test_vendor mapping should exist")
	assert.Equal(t, "prometheus", vm.SourceProtocol)

	// Only non-derived KPIs should have placeholders.
	assert.Contains(t, vm.Metrics, "registration.attempt_count")
	assert.Contains(t, vm.Metrics, "ue.connected_count")
	assert.NotContains(t, vm.Metrics, "registration.success_rate", "derived KPIs should not get placeholders")

	// Verify placeholder structure.
	m := vm.Metrics["registration.attempt_count"]
	assert.Contains(t, m.SourceTemplate, "test_vendor:")
	assert.Contains(t, m.SourceTemplate, "REPLACE_WITH_COUNTER_NAME")
	assert.Equal(t, "exact", m.LabelMatchStrategy)
	require.Len(t, m.LabelExtract, 1)
	assert.Equal(t, 4, m.LabelExtract[0].PathSegment)
	assert.Equal(t, "instance_id", m.LabelExtract[0].LabelName)
}

func TestAddVendor_PreservesExistingFormatting(t *testing.T) {
	dir := t.TempDir()
	original := minimalSchema()
	schemaPath := filepath.Join(dir, "amf.yaml")
	require.NoError(t, os.WriteFile(schemaPath, []byte(original), 0644))

	_, err := addVendorToSchema(schemaPath, "new_vendor")
	require.NoError(t, err)

	data, err := os.ReadFile(schemaPath)
	require.NoError(t, err)
	content := string(data)

	// The original content should be preserved verbatim at the start.
	assert.True(t, strings.HasPrefix(content, original),
		"original file content should be preserved at the start")
}

func TestAddVendor_DoesNotOverwriteExistingVendor(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "amf.yaml")
	require.NoError(t, os.WriteFile(schemaPath, []byte(minimalSchema()), 0644))

	// First run: should add vendor.
	updated, err := addVendorToSchema(schemaPath, "new_vendor")
	require.NoError(t, err)
	assert.True(t, updated)

	// Snapshot file size after first run.
	info1, _ := os.Stat(schemaPath)

	// Second run: should be a no-op.
	updated, err = addVendorToSchema(schemaPath, "new_vendor")
	require.NoError(t, err)
	assert.False(t, updated, "second run should report no update (idempotent)")

	// File size unchanged.
	info2, _ := os.Stat(schemaPath)
	assert.Equal(t, info1.Size(), info2.Size(), "file should not change on second run")
}

func TestAddVendor_ExistingVendorSkipped(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "amf.yaml")
	require.NoError(t, os.WriteFile(schemaPath, []byte(minimalSchema()), 0644))

	// existing_vendor is already in the minimalSchema.
	updated, err := addVendorToSchema(schemaPath, "existing_vendor")
	require.NoError(t, err)
	assert.False(t, updated, "should skip vendor that already exists in schema")
}

func TestAddVendor_GeneratesScenarioFile(t *testing.T) {
	dir := t.TempDir()

	// Set up minimal schema and scenario directories.
	schemaDir := filepath.Join(dir, "schema")
	require.NoError(t, os.MkdirAll(schemaDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(schemaDir, "amf.yaml"), []byte(minimalSchema()), 0644))

	scenarioDir := filepath.Join(dir, "scenarios")
	require.NoError(t, os.MkdirAll(scenarioDir, 0755))

	// Override the scenario directory by calling generateScenario directly
	// (it uses a hardcoded path, so we test the output structure).
	// For the full integration, we test addVendorCmd which calls generateScenario.

	// Instead, test buildVendorSnippet directly for the structure.
	kpis := []schema.KPIDefinition{
		{Name: "reg.attempts", Unit: "count"},
		{Name: "latency", Unit: "ms"},
	}

	snippet := buildVendorSnippet("acme_vendor", kpis)

	assert.Contains(t, snippet, "acme_vendor:")
	assert.Contains(t, snippet, "source_protocol: prometheus")
	assert.Contains(t, snippet, "reg.attempts:")
	assert.Contains(t, snippet, "type: counter") // count unit -> counter
	assert.Contains(t, snippet, "latency:")
	assert.Contains(t, snippet, "type: gauge") // ms unit -> gauge
	assert.Contains(t, snippet, "REPLACE_WITH_COUNTER_NAME")
	assert.Contains(t, snippet, "segment: 4")
}

func TestGenerateScenario_CreatesFile(t *testing.T) {
	dir := t.TempDir()

	// generateScenario uses hardcoded "test/scenarios/matrix" path.
	// Create the directory structure it expects.
	scenarioDir := filepath.Join(dir, "test", "scenarios", "matrix")
	require.NoError(t, os.MkdirAll(scenarioDir, 0755))

	// Create minimal AMF schema for the scenario generator to read.
	schemaDir := filepath.Join(dir, "schema")
	require.NoError(t, os.MkdirAll(schemaDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(schemaDir, "amf.yaml"), []byte(minimalSchema()), 0644))

	// We need to cd into the temp dir since generateScenario uses relative paths.
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(origDir)

	path, err := generateScenario("test_vendor", schemaDir)
	require.NoError(t, err)
	assert.Equal(t, "test/scenarios/matrix/test_vendor_amf_alarm_storm.yaml", path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "name: test_vendor_amf_alarm_storm")
	assert.Contains(t, content, "vendor: test_vendor")
	assert.Contains(t, content, "test_vendor:job_1:reader_1:")
	assert.Contains(t, content, "expected_events:")
}

func TestGenerateScenario_DoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()

	scenarioDir := filepath.Join(dir, "test", "scenarios", "matrix")
	require.NoError(t, os.MkdirAll(scenarioDir, 0755))

	schemaDir := filepath.Join(dir, "schema")
	require.NoError(t, os.MkdirAll(schemaDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(schemaDir, "amf.yaml"), []byte(minimalSchema()), 0644))

	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(origDir)

	// First call creates the file.
	path1, err := generateScenario("test_vendor", schemaDir)
	require.NoError(t, err)

	// Write a sentinel to verify no overwrite.
	require.NoError(t, os.WriteFile(path1, []byte("sentinel"), 0644))

	// Second call should return the path but not overwrite.
	path2, err := generateScenario("test_vendor", schemaDir)
	require.NoError(t, err)
	assert.Equal(t, path1, path2)

	data, err := os.ReadFile(path2)
	require.NoError(t, err)
	assert.Equal(t, "sentinel", string(data))
}
