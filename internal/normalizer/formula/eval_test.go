package formula_test

import (
	"testing"

	"github.com/argus-5g/argus/internal/normalizer/formula"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEval_Arithmetic(t *testing.T) {
	tests := []struct {
		name    string
		formula string
		vars    map[string]float64
		want    float64
	}{
		{"addition", "a + b", map[string]float64{"a": 1, "b": 2}, 3},
		{"subtraction", "a - b", map[string]float64{"a": 5, "b": 3}, 2},
		{"multiplication", "a * b", map[string]float64{"a": 3, "b": 4}, 12},
		{"division", "a / b", map[string]float64{"a": 10, "b": 4}, 2.5},
		{"parentheses", "(a + b) * c", map[string]float64{"a": 1, "b": 2, "c": 3}, 9},
		{"complex", "(a - b) / a", map[string]float64{"a": 100, "b": 3}, 0.97},
		{"number_literal", "42.5", nil, 42.5},
		{"mixed_literal_and_var", "a + 1", map[string]float64{"a": 5}, 6},
		{"nested_parens", "((a + b) * (c - d))", map[string]float64{"a": 1, "b": 2, "c": 5, "d": 2}, 9},
		{"precedence", "a + b * c", map[string]float64{"a": 1, "b": 2, "c": 3}, 7}, // not 9
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formula.Eval(tt.formula, tt.vars)
			require.NoError(t, err)
			assert.InDelta(t, tt.want, got, 0.0001)
		})
	}
}

func TestEval_Comparison(t *testing.T) {
	tests := []struct {
		name    string
		formula string
		vars    map[string]float64
		want    float64
	}{
		{"greater_true", "a > 0", map[string]float64{"a": 5}, 1},
		{"greater_false", "a > 0", map[string]float64{"a": 0}, 0},
		{"less_true", "a < 10", map[string]float64{"a": 5}, 1},
		{"less_false", "a < 10", map[string]float64{"a": 15}, 0},
		{"gte_equal", "a >= 5", map[string]float64{"a": 5}, 1},
		{"gte_greater", "a >= 5", map[string]float64{"a": 6}, 1},
		{"gte_less", "a >= 5", map[string]float64{"a": 4}, 0},
		{"lte_equal", "a <= 5", map[string]float64{"a": 5}, 1},
		{"lte_less", "a <= 5", map[string]float64{"a": 4}, 1},
		{"lte_greater", "a <= 5", map[string]float64{"a": 6}, 0},
		{"eq_true", "a == 0", map[string]float64{"a": 0}, 1},
		{"eq_false", "a == 0", map[string]float64{"a": 1}, 0},
		{"neq_true", "a != 0", map[string]float64{"a": 5}, 1},
		{"neq_false", "a != 0", map[string]float64{"a": 0}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formula.Eval(tt.formula, tt.vars)
			require.NoError(t, err)
			assert.InDelta(t, tt.want, got, 0.0001)
		})
	}
}

func TestEval_Ternary(t *testing.T) {
	tests := []struct {
		name    string
		formula string
		vars    map[string]float64
		want    float64
	}{
		{"true_branch", "a > 0 ? a / b : 0", map[string]float64{"a": 100, "b": 5}, 20},
		{"false_branch", "a > 0 ? a / b : 0", map[string]float64{"a": 0, "b": 5}, 0},
		{
			"success_rate_formula",
			"attempt_count > 0 ? (attempt_count - failure_count) / attempt_count : 0",
			map[string]float64{"attempt_count": 1000, "failure_count": 3},
			0.997,
		},
		{
			"zero_attempts",
			"attempt_count > 0 ? (attempt_count - failure_count) / attempt_count : 0",
			map[string]float64{"attempt_count": 0, "failure_count": 0},
			0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formula.Eval(tt.formula, tt.vars)
			require.NoError(t, err)
			assert.InDelta(t, tt.want, got, 0.001)
		})
	}
}

func TestEval_DotInIdentifiers(t *testing.T) {
	// KPI names use dots: "registration.attempt_count"
	got, err := formula.Eval(
		"registration.attempt_count > 0 ? (registration.attempt_count - registration.failure_count) / registration.attempt_count : 0",
		map[string]float64{
			"registration.attempt_count": 1000,
			"registration.failure_count": 3,
		},
	)
	require.NoError(t, err)
	assert.InDelta(t, 0.997, got, 0.001)
}

func TestEval_Errors(t *testing.T) {
	tests := []struct {
		name    string
		formula string
		vars    map[string]float64
		errMsg  string
	}{
		{"division_by_zero", "a / b", map[string]float64{"a": 1, "b": 0}, "division by zero"},
		{"unknown_variable", "a + unknown", map[string]float64{"a": 1}, "unknown variable"},
		{"empty_formula", "", nil, "empty formula"},
		{"unclosed_paren", "(a + b", map[string]float64{"a": 1, "b": 2}, ""},
		{"unexpected_token", "a +", map[string]float64{"a": 1}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := formula.Eval(tt.formula, tt.vars)
			assert.Error(t, err)
			if tt.errMsg != "" {
				assert.ErrorContains(t, err, tt.errMsg)
			}
		})
	}
}
