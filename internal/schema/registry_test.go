package schema_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argus-5g/argus/internal/schema"
)

func indexOf(order []string, name string) int {
	for i, n := range order {
		if n == name {
			return i
		}
	}
	return -1
}

func TestRegistry_LoadFromTestData(t *testing.T) {
	r, err := schema.LoadFromDir("testdata/valid")
	require.NoError(t, err)

	s, err := r.GetSchema("argus.5g.amf")
	require.NoError(t, err)
	assert.Equal(t, "AMF", s.NFType)
	assert.Equal(t, "v1", s.SchemaVersion)
	assert.Equal(t, "3GPP TS 28.552", s.Spec)

	kpi, err := r.GetKPI("argus.5g.amf", "registration.success_rate")
	require.NoError(t, err)
	assert.Equal(t, "ratio", kpi.Unit)
	assert.True(t, kpi.Derived)
	assert.Contains(t, kpi.SpecRef, "3GPP")

	m, err := r.GetMapping("argus.5g.amf", "free5gc", "registration.attempt_count")
	require.NoError(t, err)
	assert.Equal(t, "counter", m.Type)
	assert.True(t, m.ResetAware)
	assert.Equal(t, "exact", m.LabelMatchStrategy)
	assert.Equal(t, "amf_n1_message_total", m.PrometheusMetric)
}

func TestRegistry_EvaluationOrder(t *testing.T) {
	r, err := schema.LoadFromDir("testdata/valid")
	require.NoError(t, err)

	order := r.EvaluationOrder("argus.5g.amf")
	require.NotNil(t, order)

	attemptIdx := indexOf(order, "registration.attempt_count")
	failureIdx := indexOf(order, "registration.failure_count")
	rateIdx := indexOf(order, "registration.success_rate")
	connectedIdx := indexOf(order, "ue.connected_count")

	// All KPIs present.
	assert.GreaterOrEqual(t, attemptIdx, 0)
	assert.GreaterOrEqual(t, failureIdx, 0)
	assert.GreaterOrEqual(t, rateIdx, 0)
	assert.GreaterOrEqual(t, connectedIdx, 0)

	// Derived KPIs come after their dependencies.
	assert.Less(t, attemptIdx, rateIdx)
	assert.Less(t, failureIdx, rateIdx)
}

func TestRegistry_CircularDependency(t *testing.T) {
	_, err := schema.LoadFromDir("testdata/circular")
	require.Error(t, err)
	assert.ErrorContains(t, err, "circular dependency")
}

func TestRegistry_GetNonexistentSchema(t *testing.T) {
	r, err := schema.LoadFromDir("testdata/valid")
	require.NoError(t, err)

	_, err = r.GetSchema("argus.5g.nonexistent")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "not found")
}

func TestRegistry_GetNonexistentKPI(t *testing.T) {
	r, err := schema.LoadFromDir("testdata/valid")
	require.NoError(t, err)

	_, err = r.GetKPI("argus.5g.amf", "does.not.exist")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "not found")
}

func TestRegistry_GetNonexistentVendor(t *testing.T) {
	r, err := schema.LoadFromDir("testdata/valid")
	require.NoError(t, err)

	_, err = r.GetMapping("argus.5g.amf", "nokia", "registration.attempt_count")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "not found")
}

func TestRegistry_GetNonexistentMappingKPI(t *testing.T) {
	r, err := schema.LoadFromDir("testdata/valid")
	require.NoError(t, err)

	_, err = r.GetMapping("argus.5g.amf", "free5gc", "does.not.exist")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "not found")
}

func TestRegistry_Namespaces(t *testing.T) {
	r, err := schema.LoadFromDir("testdata/valid")
	require.NoError(t, err)

	ns := r.Namespaces()
	assert.Equal(t, []string{"argus.5g.amf"}, ns)
}

func TestRegistry_DerivedWithoutFormula(t *testing.T) {
	_, err := schema.LoadFromDir("testdata/invalid_no_formula")
	require.Error(t, err)
	assert.ErrorContains(t, err, "must have a formula")
}

func TestRegistry_DanglingDependency(t *testing.T) {
	_, err := schema.LoadFromDir("testdata/invalid_bad_dep")
	require.Error(t, err)
	assert.ErrorContains(t, err, "nonexistent KPI")
}

func TestRegistry_NoYAMLFiles(t *testing.T) {
	_, err := schema.LoadFromDir(t.TempDir())
	require.Error(t, err)
	assert.ErrorContains(t, err, "no *.yaml files")
}

func TestRegistry_EvaluationOrderNonexistent(t *testing.T) {
	r, err := schema.LoadFromDir("testdata/valid")
	require.NoError(t, err)

	order := r.EvaluationOrder("argus.5g.nonexistent")
	assert.Nil(t, order)
}

func TestRegistry_MappingLabels(t *testing.T) {
	r, err := schema.LoadFromDir("testdata/valid")
	require.NoError(t, err)

	m, err := r.GetMapping("argus.5g.amf", "free5gc", "registration.attempt_count")
	require.NoError(t, err)
	assert.Equal(t, "registration_request", m.Labels["msg_type"])
}

func TestRegistry_GaugeMapping(t *testing.T) {
	r, err := schema.LoadFromDir("testdata/valid")
	require.NoError(t, err)

	m, err := r.GetMapping("argus.5g.amf", "free5gc", "ue.connected_count")
	require.NoError(t, err)
	assert.Equal(t, "gauge", m.Type)
	assert.False(t, m.ResetAware)
	assert.Equal(t, "amf_connected_ue", m.PrometheusMetric)
}

func TestRegistry_LoadFromSchemaV1(t *testing.T) {
	const schemaV1Dir = "../../schema/v1"
	r, err := schema.LoadFromDir(schemaV1Dir)
	require.NoError(t, err, "all schema/v1/*.yaml files must load and validate")

	ns := r.Namespaces()
	assert.Equal(t, []string{"argus.5g.amf", "argus.5g.gnb", "argus.5g.slice", "argus.5g.smf", "argus.5g.upf"}, ns)

	for _, n := range ns {
		order := r.EvaluationOrder(n)
		assert.NotEmpty(t, order, "namespace %s should have KPIs", n)
	}

	// gnb schema includes the interference KPI added in v0.2.1.
	_, err = r.GetKPI("argus.5g.gnb", "interference.dl_dBm")
	assert.NoError(t, err, "gnb schema must include interference.dl_dBm")
}

// TestSchema_RuleKPICoverage asserts every KPI referenced by correlation rules
// exists in the schema registry. Guards against rules referencing undefined KPIs.
func TestSchema_RuleKPICoverage(t *testing.T) {
	const schemaV1Dir = "../../schema/v1"
	r, err := schema.LoadFromDir(schemaV1Dir)
	require.NoError(t, err)

	// KPIs referenced by correlator rules (from internal/correlator/rules.go).
	// Keyed by namespace → list of KPI names.
	ruleKPIs := map[string][]string{
		"argus.5g.amf": {"registration.attempt_count", "registration.success_rate"},
		"argus.5g.smf": {"session.active_count"},
		"argus.5g.gnb": {"throughput.downlink_bps"},
		"argus.5g.upf": {"throughput.downlink_bps"},
	}

	for ns, kpis := range ruleKPIs {
		for _, kpi := range kpis {
			_, err := r.GetKPI(ns, kpi)
			assert.NoError(t, err, "rule references %s/%s but schema does not define it", ns, kpi)
		}
	}
}
