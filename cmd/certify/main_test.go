package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadScenario(t *testing.T) {
	s, err := loadScenario("../../simulator/scenarios/alarm_storm.yaml")
	require.NoError(t, err)
	assert.Equal(t, "alarm_storm", s.Name)
	assert.NotEmpty(t, s.ExpectedEvents)
}

func TestLoadScenario_NotFound(t *testing.T) {
	_, err := loadScenario("/nonexistent/scenario.yaml")
	assert.Error(t, err)
}

func TestListCmd(t *testing.T) {
	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := listCmd([]string{"../../simulator/scenarios"})

	w.Close()
	os.Stdout = old

	require.NoError(t, err)

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	assert.Contains(t, output, "alarm_storm")
	assert.Contains(t, output, "steady_state")
	assert.Contains(t, output, "SCENARIO")
}

func TestListCmd_NoDir(t *testing.T) {
	err := listCmd([]string{})
	assert.Error(t, err)
}

func TestListCmd_BadDir(t *testing.T) {
	err := listCmd([]string{"/nonexistent/dir"})
	assert.Error(t, err)
}

func TestBaseScenarioName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"free5gc_alarm_storm", "alarm_storm"},
		{"open5gs_alarm_storm", "alarm_storm"},
		{"oai_steady_state", "steady_state"},
		{"nokia_alarm_storm", "alarm_storm"},
		{"ericsson_alarm_storm", "alarm_storm"},
		{"unknown_vendor_test", "unknown_vendor_test"},
		{"alarm_storm", "alarm_storm"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.want, baseScenarioName(tc.input))
		})
	}
}

func TestMatrixCmd_NoDir(t *testing.T) {
	err := matrixCmd([]string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--matrix-dir is required")
}

func TestMatrixCmd_BadDir(t *testing.T) {
	err := matrixCmd([]string{"--matrix-dir", "/nonexistent/dir"})
	assert.Error(t, err)
}

func TestMatrixCmd_BadTimeout(t *testing.T) {
	dir := t.TempDir()
	err := matrixCmd([]string{"--matrix-dir", dir, "--timeout", "notaduration"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--timeout")
}

func TestLoadScenario_WithEvidenceAssertions(t *testing.T) {
	// Write a temporary scenario with evidence assertions.
	dir := t.TempDir()
	scenarioYAML := `
name: test_evidence
description: test
duration: 10
nfs: []
expected_events:
  - rule: TestRule
    severity: critical
    within_seconds: 5
    affected_nfs: [AMF]
    evidence:
      amf.registration.attempt_rate:
        gt: 0.0
      amf.registration.success_rate:
        lt: 0.9
`
	path := filepath.Join(dir, "test.yaml")
	require.NoError(t, os.WriteFile(path, []byte(scenarioYAML), 0644))

	s, err := loadScenario(path)
	require.NoError(t, err)
	assert.Equal(t, "test_evidence", s.Name)
	require.Len(t, s.ExpectedEvents, 1)

	ev := s.ExpectedEvents[0]
	assert.NotNil(t, ev.Evidence)
	assert.Contains(t, ev.Evidence, "amf.registration.attempt_rate")
	assert.Contains(t, ev.Evidence, "amf.registration.success_rate")
	assert.NotNil(t, ev.Evidence["amf.registration.attempt_rate"].GT)
	assert.NotNil(t, ev.Evidence["amf.registration.success_rate"].LT)
}
