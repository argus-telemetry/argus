package correlator

import (
	"math"
	"time"
)

// RegistrationStorm fires when AMF registration attempts spike >3 sigma above
// the rolling mean while the success rate drops below 0.9.
type RegistrationStorm struct{}

func (r *RegistrationStorm) Name() string     { return "RegistrationStorm" }
func (r *RegistrationStorm) Severity() string { return "critical" }

func (r *RegistrationStorm) Evaluate(w WindowSnapshot) []CorrelationEvent {
	attempts := w.Samples["argus.5g.amf"]["registration.attempt_count"]
	rates := w.Samples["argus.5g.amf"]["registration.success_rate"]

	if len(attempts) < 4 || len(rates) < 2 {
		return nil // insufficient data
	}

	mean, stddev := meanStddev(attempts)
	latest := attempts[len(attempts)-1].Value
	latestRate := rates[len(rates)-1].Value

	if stddev > 0 && latest > mean+3*stddev && latestRate < 0.9 {
		return []CorrelationEvent{{
			RuleName:    r.Name(),
			Severity:    r.Severity(),
			PLMN:        w.PLMN,
			SliceID:     w.SliceID,
			AffectedNFs: []string{"AMF"},
			Evidence: map[string]float64{
				"registration.attempt_count":  latest,
				"registration.attempt_mean":   mean,
				"registration.attempt_stddev": stddev,
				"registration.success_rate":   latestRate,
			},
			WindowStart: w.Start,
			WindowEnd:   w.End,
			Timestamp:   time.Now(),
		}}
	}
	return nil
}

// SessionDrop fires when SMF session count drops >20% within the window
// while AMF registration success rate remains normal (>= 0.9).
type SessionDrop struct{}

func (r *SessionDrop) Name() string     { return "SessionDrop" }
func (r *SessionDrop) Severity() string { return "warning" }

func (r *SessionDrop) Evaluate(w WindowSnapshot) []CorrelationEvent {
	sessions := w.Samples["argus.5g.smf"]["session.active_count"]
	rates := w.Samples["argus.5g.amf"]["registration.success_rate"]

	if len(sessions) < 2 || len(rates) < 1 {
		return nil
	}

	peak := maxValue(sessions)
	latest := sessions[len(sessions)-1].Value
	latestRate := rates[len(rates)-1].Value

	dropPct := (peak - latest) / peak
	if peak > 0 && dropPct > 0.20 && latestRate >= 0.9 {
		return []CorrelationEvent{{
			RuleName:    r.Name(),
			Severity:    r.Severity(),
			PLMN:        w.PLMN,
			SliceID:     w.SliceID,
			AffectedNFs: []string{"SMF"},
			Evidence: map[string]float64{
				"session.active_count.peak":     peak,
				"session.active_count.latest":   latest,
				"session.drop_pct":              dropPct,
				"amf.registration.success_rate": latestRate,
			},
			WindowStart: w.Start,
			WindowEnd:   w.End,
			Timestamp:   time.Now(),
		}}
	}
	return nil
}

// RANCoreDivergence fires when gNB downlink throughput drops >30% while
// UPF downlink throughput remains stable within 10%.
type RANCoreDivergence struct{}

func (r *RANCoreDivergence) Name() string     { return "RANCoreDivergence" }
func (r *RANCoreDivergence) Severity() string { return "warning" }

func (r *RANCoreDivergence) Evaluate(w WindowSnapshot) []CorrelationEvent {
	gnbDL := w.Samples["argus.5g.gnb"]["throughput.downlink_bps"]
	upfDL := w.Samples["argus.5g.upf"]["throughput.downlink_bps"]

	if len(gnbDL) < 2 || len(upfDL) < 2 {
		return nil
	}

	gnbPeak := maxValue(gnbDL)
	gnbLatest := gnbDL[len(gnbDL)-1].Value

	upfMean, _ := meanStddev(upfDL)
	upfLatest := upfDL[len(upfDL)-1].Value

	gnbDrop := 0.0
	if gnbPeak > 0 {
		gnbDrop = (gnbPeak - gnbLatest) / gnbPeak
	}
	upfDeviation := 0.0
	if upfMean > 0 {
		upfDeviation = math.Abs(upfLatest-upfMean) / upfMean
	}

	if gnbDrop > 0.30 && upfDeviation < 0.10 {
		return []CorrelationEvent{{
			RuleName:    r.Name(),
			Severity:    r.Severity(),
			PLMN:        w.PLMN,
			SliceID:     w.SliceID,
			AffectedNFs: []string{"gNB"},
			Evidence: map[string]float64{
				"gnb.throughput.downlink_peak":   gnbPeak,
				"gnb.throughput.downlink_latest": gnbLatest,
				"gnb.throughput.drop_pct":        gnbDrop,
				"upf.throughput.downlink_mean":   upfMean,
				"upf.throughput.downlink_latest": upfLatest,
			},
			WindowStart: w.Start,
			WindowEnd:   w.End,
			Timestamp:   time.Now(),
		}}
	}
	return nil
}

// --- math helpers ---

func meanStddev(samples []Sample) (float64, float64) {
	if len(samples) == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, s := range samples {
		sum += s.Value
	}
	mean := sum / float64(len(samples))

	variance := 0.0
	for _, s := range samples {
		d := s.Value - mean
		variance += d * d
	}
	variance /= float64(len(samples))
	return mean, math.Sqrt(variance)
}

func maxValue(samples []Sample) float64 {
	if len(samples) == 0 {
		return 0
	}
	m := samples[0].Value
	for _, s := range samples[1:] {
		if s.Value > m {
			m = s.Value
		}
	}
	return m
}
