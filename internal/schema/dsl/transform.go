package dsl

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Sample is a timestamped metric value for transform computation.
type Sample struct {
	Value     float64
	Timestamp time.Time
}

// ApplyTransform applies a value transformation to a series of samples.
// Supported transforms:
//   - "identity" or "": return the latest value unchanged
//   - "rate(Ns)": delta of last two values divided by window N seconds
//   - "delta": difference between last two values
//   - "ratio(a,b)": values[a] / values[b] (named index lookup)
func ApplyTransform(transform string, values []Sample) (float64, error) {
	if len(values) == 0 {
		return 0, fmt.Errorf("no samples")
	}

	transform = strings.TrimSpace(transform)
	if transform == "" || transform == "identity" {
		return values[len(values)-1].Value, nil
	}

	if transform == "delta" {
		return applyDelta(values)
	}

	if strings.HasPrefix(transform, "rate(") {
		return applyRate(transform, values)
	}

	if strings.HasPrefix(transform, "ratio(") {
		return applyRatio(transform, values)
	}

	return 0, fmt.Errorf("unsupported transform %q", transform)
}

func applyDelta(values []Sample) (float64, error) {
	if len(values) < 2 {
		return values[len(values)-1].Value, nil
	}
	return values[len(values)-1].Value - values[len(values)-2].Value, nil
}

func applyRate(transform string, values []Sample) (float64, error) {
	// Parse "rate(30s)" → 30s window.
	inner := strings.TrimPrefix(transform, "rate(")
	inner = strings.TrimSuffix(inner, ")")
	window, err := time.ParseDuration(inner)
	if err != nil {
		return 0, fmt.Errorf("parse rate window %q: %w", inner, err)
	}

	if len(values) < 2 {
		return values[len(values)-1].Value, nil
	}

	delta := values[len(values)-1].Value - values[len(values)-2].Value
	return delta / window.Seconds(), nil
}

func applyRatio(transform string, values []Sample) (float64, error) {
	// Parse "ratio(a,b)" → values[a] / values[b].
	// a and b are integer indices into the values slice.
	inner := strings.TrimPrefix(transform, "ratio(")
	inner = strings.TrimSuffix(inner, ")")
	parts := strings.Split(inner, ",")
	if len(parts) != 2 {
		return 0, fmt.Errorf("ratio requires exactly 2 arguments, got %d", len(parts))
	}

	a, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, fmt.Errorf("parse ratio index %q: %w", parts[0], err)
	}
	b, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, fmt.Errorf("parse ratio index %q: %w", parts[1], err)
	}

	if a < 0 || a >= len(values) || b < 0 || b >= len(values) {
		return 0, fmt.Errorf("ratio indices [%d,%d] out of range for %d values", a, b, len(values))
	}
	if values[b].Value == 0 {
		return 0, nil // avoid division by zero
	}
	return values[a].Value / values[b].Value, nil
}
