package dsl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandTemplate_AllVars(t *testing.T) {
	tmpl := "/pm/stats/{{.NF}}/{{.Vendor}}/{{.Metric}}/{{.Instance}}/{{.PLMN}}/{{.Slice}}"
	vars := Vars{
		NF: "AMF", Vendor: "nokia", Metric: "reg_attempts",
		Instance: "amf-001", PLMN: "310-260", Slice: "1:010203",
	}
	result, err := ExpandTemplate(tmpl, vars)
	require.NoError(t, err)
	assert.Equal(t, "/pm/stats/AMF/nokia/reg_attempts/amf-001/310-260/1:010203", result)
}

func TestExpandTemplate_Partial(t *testing.T) {
	tmpl := "/pm/stats/{{.NF}}/reg/{{.Instance}}/success"
	vars := Vars{NF: "AMF", Instance: "amf-001"}
	result, err := ExpandTemplate(tmpl, vars)
	require.NoError(t, err)
	assert.Equal(t, "/pm/stats/AMF/reg/amf-001/success", result)
}

func TestExpandTemplate_InvalidTemplate(t *testing.T) {
	_, err := ExpandTemplate("{{.Invalid", Vars{})
	assert.Error(t, err)
}

func TestMatchTemplate_Exact(t *testing.T) {
	vars, ok := MatchTemplate(
		"/pm/stats/{{.NF}}/reg/{{.Instance}}/success",
		"/pm/stats/AMF/reg/amf-001/success",
	)
	assert.True(t, ok)
	assert.Equal(t, "AMF", vars["NF"])
	assert.Equal(t, "amf-001", vars["Instance"])
}

func TestMatchTemplate_NoMatch_DifferentLength(t *testing.T) {
	_, ok := MatchTemplate(
		"/pm/stats/{{.NF}}/reg",
		"/pm/stats/AMF/reg/extra",
	)
	assert.False(t, ok)
}

func TestMatchTemplate_NoMatch_LiteralMismatch(t *testing.T) {
	_, ok := MatchTemplate(
		"/pm/stats/{{.NF}}/reg/{{.Instance}}/success",
		"/pm/stats/AMF/handover/amf-001/success",
	)
	assert.False(t, ok)
}

func TestMatchTemplate_NoVars(t *testing.T) {
	_, ok := MatchTemplate("/pm/stats/fixed/path", "/pm/stats/fixed/path")
	assert.True(t, ok)
}

func TestMatchTemplate_NoVars_Mismatch(t *testing.T) {
	_, ok := MatchTemplate("/pm/stats/fixed/path", "/pm/stats/other/path")
	assert.False(t, ok)
}

func TestMatchTemplate_RoundTrip(t *testing.T) {
	tmpl := "/pm/stats/{{.NF}}/reg/{{.Instance}}/success"
	vars := Vars{NF: "SMF", Instance: "smf-002"}

	expanded, err := ExpandTemplate(tmpl, vars)
	require.NoError(t, err)

	extracted, ok := MatchTemplate(tmpl, expanded)
	assert.True(t, ok)
	assert.Equal(t, "SMF", extracted["NF"])
	assert.Equal(t, "smf-002", extracted["Instance"])
}

func TestMatchTemplate_ColonDelimited(t *testing.T) {
	vars, ok := MatchTemplate(
		"ericsson_pm:{{.Job}}:{{.Reader}}:pmNrRegInitAttSum:{{.Instance}}",
		"ericsson_pm:pm_job_1:reader_1:pmNrRegInitAttSum:amf_001",
	)
	assert.True(t, ok, "colon-delimited template should match")
	assert.Equal(t, "pm_job_1", vars["Job"])
	assert.Equal(t, "reader_1", vars["Reader"])
	assert.Equal(t, "amf_001", vars["Instance"])
}

func TestMatchTemplate_ColonDelimited_Mismatch(t *testing.T) {
	_, ok := MatchTemplate(
		"ericsson_pm:{{.Job}}:{{.Reader}}:pmNrRegInitAttSum:{{.Instance}}",
		"ericsson_pm:pm_job_1:reader_1:pmNrRegInitFailSum:amf_001",
	)
	assert.False(t, ok, "counter name mismatch should not match")
}

func TestExtractVarNames(t *testing.T) {
	names := extractVarNames("/pm/{{.NF}}/{{.Instance}}/{{.Metric}}")
	assert.Equal(t, []string{"NF", "Instance", "Metric"}, names)
}

func TestExtractVarNames_None(t *testing.T) {
	names := extractVarNames("/pm/fixed/path")
	assert.Empty(t, names)
}
