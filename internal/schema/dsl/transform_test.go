package dsl

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeSamples(vals ...float64) []Sample {
	now := time.Now()
	samples := make([]Sample, len(vals))
	for i, v := range vals {
		samples[i] = Sample{Value: v, Timestamp: now.Add(time.Duration(i) * time.Second)}
	}
	return samples
}

func TestApplyTransform_Identity(t *testing.T) {
	val, err := ApplyTransform("identity", makeSamples(1, 2, 3))
	require.NoError(t, err)
	assert.Equal(t, 3.0, val)
}

func TestApplyTransform_EmptyIsIdentity(t *testing.T) {
	val, err := ApplyTransform("", makeSamples(5))
	require.NoError(t, err)
	assert.Equal(t, 5.0, val)
}

func TestApplyTransform_Delta(t *testing.T) {
	val, err := ApplyTransform("delta", makeSamples(100, 150))
	require.NoError(t, err)
	assert.Equal(t, 50.0, val)
}

func TestApplyTransform_Delta_SingleSample(t *testing.T) {
	val, err := ApplyTransform("delta", makeSamples(42))
	require.NoError(t, err)
	assert.Equal(t, 42.0, val)
}

func TestApplyTransform_Rate(t *testing.T) {
	val, err := ApplyTransform("rate(30s)", makeSamples(100, 400))
	require.NoError(t, err)
	assert.Equal(t, 10.0, val) // (400-100) / 30
}

func TestApplyTransform_Rate_1s(t *testing.T) {
	val, err := ApplyTransform("rate(1s)", makeSamples(0, 100))
	require.NoError(t, err)
	assert.Equal(t, 100.0, val)
}

func TestApplyTransform_Ratio(t *testing.T) {
	val, err := ApplyTransform("ratio(0,1)", makeSamples(30, 100))
	require.NoError(t, err)
	assert.Equal(t, 0.3, val) // 30 / 100
}

func TestApplyTransform_Ratio_DivByZero(t *testing.T) {
	val, err := ApplyTransform("ratio(0,1)", makeSamples(30, 0))
	require.NoError(t, err)
	assert.Equal(t, 0.0, val) // safe division by zero
}

func TestApplyTransform_NoSamples(t *testing.T) {
	_, err := ApplyTransform("identity", nil)
	assert.Error(t, err)
}

func TestApplyTransform_Unsupported(t *testing.T) {
	_, err := ApplyTransform("nonexistent", makeSamples(1))
	assert.Error(t, err)
}

func TestApplyTransform_Rate_InvalidDuration(t *testing.T) {
	_, err := ApplyTransform("rate(bad)", makeSamples(1, 2))
	assert.Error(t, err)
}

func TestApplyTransform_Ratio_OutOfRange(t *testing.T) {
	_, err := ApplyTransform("ratio(0,5)", makeSamples(1, 2))
	assert.Error(t, err)
}
