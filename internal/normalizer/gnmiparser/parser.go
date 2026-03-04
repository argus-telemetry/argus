// Package gnmiparser converts serialized gNMI SubscribeResponse protobufs into
// []promparser.ParsedMetric. This allows the normalization engine to process
// gNMI telemetry through the same pipeline as Prometheus exposition format data.
//
// Wire format contract: the gNMI collector serializes each SubscribeResponse via
// proto.Marshal(resp) and sets Protocol: ProtocolGNMI on the RawRecord. This
// package reverses that serialization.
//
// TypedValue handling: only numeric types (DoubleVal, FloatVal, IntVal, UintVal)
// produce metrics. Non-numeric types (StringVal, BoolVal, BytesVal, JsonVal,
// JsonIetfVal) are silently skipped — they carry config or state data, not
// telemetry values.
//
// Path construction: gNMI paths are rendered as slash-delimited canonical strings
// (e.g. /gnb/cell/prb/utilization). Path element keys (e.g. interface[name=eth0])
// are extracted as metric labels, not embedded in the path string.
package gnmiparser

import (
	"fmt"
	"strings"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/protobuf/proto"

	"github.com/argus-5g/argus/internal/normalizer/promparser"
)

// Parse deserializes a gNMI SubscribeResponse protobuf and extracts metrics
// from its Notification updates.
//
// Returns:
//   - empty slice (no error) for sync_response messages or empty notifications
//   - error for malformed protobuf input
//   - []ParsedMetric with one entry per numeric Update in the Notification
func Parse(data []byte) ([]promparser.ParsedMetric, error) {
	var resp gpb.SubscribeResponse
	if err := proto.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal gNMI SubscribeResponse: %w", err)
	}

	notif := resp.GetUpdate()
	if notif == nil {
		// sync_response or other non-update message — not an error.
		return nil, nil
	}

	var metrics []promparser.ParsedMetric
	for _, upd := range notif.GetUpdate() {
		val, ok := numericValue(upd.GetVal())
		if !ok {
			continue // non-numeric TypedValue — skip silently
		}

		name, labels := buildPath(notif.GetPrefix(), upd.GetPath())

		metrics = append(metrics, promparser.ParsedMetric{
			Name:   name,
			Labels: labels,
			Value:  val,
			Type:   "untyped",
		})
	}

	return metrics, nil
}

// numericValue extracts a float64 from a gNMI TypedValue.
// Returns (value, true) for numeric types, (0, false) for everything else.
func numericValue(tv *gpb.TypedValue) (float64, bool) {
	if tv == nil {
		return 0, false
	}
	switch v := tv.GetValue().(type) {
	case *gpb.TypedValue_DoubleVal:
		return v.DoubleVal, true
	case *gpb.TypedValue_FloatVal:
		return float64(v.FloatVal), true //nolint:staticcheck // FloatVal deprecated in gNMI spec but old devices still send it
	case *gpb.TypedValue_IntVal:
		return float64(v.IntVal), true
	case *gpb.TypedValue_UintVal:
		return float64(v.UintVal), true
	default:
		return 0, false
	}
}

// buildPath constructs the canonical path string and extracts labels from a
// gNMI prefix + update path. The prefix elems are prepended to the update elems.
//
// Path element keys (e.g. interface[name=eth0]) are collected into the labels map
// and excluded from the path string. Keys from both prefix and update paths are
// merged — key collisions favor the update path (last-write-wins).
func buildPath(prefix, path *gpb.Path) (string, map[string]string) {
	var elems []*gpb.PathElem
	if prefix != nil {
		elems = append(elems, prefix.GetElem()...)
	}
	if path != nil {
		elems = append(elems, path.GetElem()...)
	}

	var labels map[string]string
	parts := make([]string, 0, len(elems))
	for _, e := range elems {
		parts = append(parts, e.GetName())
		for k, v := range e.GetKey() {
			if labels == nil {
				labels = make(map[string]string)
			}
			labels[k] = v
		}
	}

	name := "/" + strings.Join(parts, "/")
	return name, labels
}
