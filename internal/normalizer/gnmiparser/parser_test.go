package gnmiparser_test

import (
	"testing"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/argus-5g/argus/internal/normalizer/gnmiparser"
	"github.com/argus-5g/argus/internal/normalizer/promparser"
)

// marshalResponse serializes a SubscribeResponse to bytes, failing the test on error.
func marshalResponse(t *testing.T, resp *gpb.SubscribeResponse) []byte {
	t.Helper()
	data, err := proto.Marshal(resp)
	require.NoError(t, err, "marshaling SubscribeResponse")
	return data
}

// updateResponse builds a SubscribeResponse wrapping a Notification with optional prefix.
func updateResponse(prefix *gpb.Path, updates ...*gpb.Update) *gpb.SubscribeResponse {
	return &gpb.SubscribeResponse{
		Response: &gpb.SubscribeResponse_Update{
			Update: &gpb.Notification{
				Timestamp: 1700000000000000000,
				Prefix:    prefix,
				Update:    updates,
			},
		},
	}
}

func TestParse_SingleUpdate(t *testing.T) {
	resp := updateResponse(nil,
		&gpb.Update{
			Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "gnb"}, {Name: "cell"}, {Name: "prb"}, {Name: "utilization"}}},
			Val:  &gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: 72.5}},
		},
	)

	metrics, err := gnmiparser.Parse(marshalResponse(t, resp))
	require.NoError(t, err)
	require.Len(t, metrics, 1)

	assert.Equal(t, "/gnb/cell/prb/utilization", metrics[0].Name)
	assert.Equal(t, 72.5, metrics[0].Value)
	assert.Equal(t, "untyped", metrics[0].Type)
	assert.Nil(t, metrics[0].Labels)
}

func TestParse_MultipleUpdates(t *testing.T) {
	resp := updateResponse(nil,
		&gpb.Update{
			Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "gnb"}, {Name: "throughput"}, {Name: "dl"}}},
			Val:  &gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: 500.0}},
		},
		&gpb.Update{
			Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "gnb"}, {Name: "throughput"}, {Name: "ul"}}},
			Val:  &gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: 150.0}},
		},
		&gpb.Update{
			Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "gnb"}, {Name: "latency"}}},
			Val:  &gpb.TypedValue{Value: &gpb.TypedValue_IntVal{IntVal: 4}},
		},
	)

	metrics, err := gnmiparser.Parse(marshalResponse(t, resp))
	require.NoError(t, err)
	require.Len(t, metrics, 3)

	// Build name->metric index for order-independent assertions.
	byName := make(map[string]promparser.ParsedMetric, len(metrics))
	for _, m := range metrics {
		byName[m.Name] = m
	}

	assert.Equal(t, 500.0, byName["/gnb/throughput/dl"].Value)
	assert.Equal(t, 150.0, byName["/gnb/throughput/ul"].Value)
	assert.Equal(t, 4.0, byName["/gnb/latency"].Value)
}

func TestParse_PrefixConcatenation(t *testing.T) {
	prefix := &gpb.Path{Elem: []*gpb.PathElem{{Name: "gnb"}, {Name: "cell"}}}

	resp := updateResponse(prefix,
		&gpb.Update{
			Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "prb"}, {Name: "utilization"}}},
			Val:  &gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: 88.3}},
		},
	)

	metrics, err := gnmiparser.Parse(marshalResponse(t, resp))
	require.NoError(t, err)
	require.Len(t, metrics, 1)

	assert.Equal(t, "/gnb/cell/prb/utilization", metrics[0].Name)
	assert.Equal(t, 88.3, metrics[0].Value)
}

func TestParse_PathElementKeys(t *testing.T) {
	resp := updateResponse(nil,
		&gpb.Update{
			Path: &gpb.Path{Elem: []*gpb.PathElem{
				{Name: "interfaces"},
				{Name: "interface", Key: map[string]string{"name": "eth0"}},
				{Name: "counters"},
				{Name: "in-octets"},
			}},
			Val: &gpb.TypedValue{Value: &gpb.TypedValue_UintVal{UintVal: 123456}},
		},
	)

	metrics, err := gnmiparser.Parse(marshalResponse(t, resp))
	require.NoError(t, err)
	require.Len(t, metrics, 1)

	// Path string excludes keys.
	assert.Equal(t, "/interfaces/interface/counters/in-octets", metrics[0].Name)
	// Keys become labels.
	assert.Equal(t, map[string]string{"name": "eth0"}, metrics[0].Labels)
	assert.Equal(t, 123456.0, metrics[0].Value)
}

func TestParse_IntVal(t *testing.T) {
	resp := updateResponse(nil,
		&gpb.Update{
			Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "counter"}, {Name: "packets"}}},
			Val:  &gpb.TypedValue{Value: &gpb.TypedValue_IntVal{IntVal: -42}},
		},
	)

	metrics, err := gnmiparser.Parse(marshalResponse(t, resp))
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	assert.Equal(t, -42.0, metrics[0].Value)
}

func TestParse_FloatVal(t *testing.T) {
	resp := updateResponse(nil,
		&gpb.Update{
			Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "sensor"}, {Name: "temperature"}}},
			Val:  &gpb.TypedValue{Value: &gpb.TypedValue_FloatVal{FloatVal: 36.5}},
		},
	)

	metrics, err := gnmiparser.Parse(marshalResponse(t, resp))
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	// float32 -> float64 conversion: compare with tolerance for FP precision.
	assert.InDelta(t, 36.5, metrics[0].Value, 0.01)
}

func TestParse_NonNumericSkipped(t *testing.T) {
	resp := updateResponse(nil,
		&gpb.Update{
			Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "system"}, {Name: "hostname"}}},
			Val:  &gpb.TypedValue{Value: &gpb.TypedValue_StringVal{StringVal: "gnb-01"}},
		},
		&gpb.Update{
			Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "system"}, {Name: "uptime"}}},
			Val:  &gpb.TypedValue{Value: &gpb.TypedValue_UintVal{UintVal: 86400}},
		},
	)

	metrics, err := gnmiparser.Parse(marshalResponse(t, resp))
	require.NoError(t, err)
	// StringVal is silently dropped; only UintVal survives.
	require.Len(t, metrics, 1)
	assert.Equal(t, "/system/uptime", metrics[0].Name)
	assert.Equal(t, 86400.0, metrics[0].Value)
}

func TestParse_EmptyNotification(t *testing.T) {
	resp := updateResponse(nil) // no updates

	metrics, err := gnmiparser.Parse(marshalResponse(t, resp))
	require.NoError(t, err)
	assert.Empty(t, metrics)
}

func TestParse_SyncResponse(t *testing.T) {
	resp := &gpb.SubscribeResponse{
		Response: &gpb.SubscribeResponse_SyncResponse{SyncResponse: true},
	}

	metrics, err := gnmiparser.Parse(marshalResponse(t, resp))
	require.NoError(t, err)
	assert.Empty(t, metrics)
}

func TestParse_MalformedProtobuf(t *testing.T) {
	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03}

	_, err := gnmiparser.Parse(garbage)
	require.Error(t, err)
}

func TestParse_MultiplePathElementKeys(t *testing.T) {
	// Path with keys on multiple elements: all keys merge into labels.
	// When the same key appears on multiple elements, last-write-wins.
	resp := updateResponse(nil,
		&gpb.Update{
			Path: &gpb.Path{Elem: []*gpb.PathElem{
				{Name: "network-instances"},
				{Name: "network-instance", Key: map[string]string{"instance": "default"}},
				{Name: "protocol", Key: map[string]string{"identifier": "BGP", "name": "bgp"}},
				{Name: "neighbor-count"},
			}},
			Val: &gpb.TypedValue{Value: &gpb.TypedValue_IntVal{IntVal: 12}},
		},
	)

	metrics, err := gnmiparser.Parse(marshalResponse(t, resp))
	require.NoError(t, err)
	require.Len(t, metrics, 1)

	assert.Equal(t, "/network-instances/network-instance/protocol/neighbor-count", metrics[0].Name)
	assert.Equal(t, map[string]string{
		"instance":   "default",
		"identifier": "BGP",
		"name":       "bgp",
	}, metrics[0].Labels)
	assert.Equal(t, 12.0, metrics[0].Value)
}

func TestParse_PrefixWithKeys(t *testing.T) {
	// Prefix path with keys — keys from prefix and update are merged.
	prefix := &gpb.Path{Elem: []*gpb.PathElem{
		{Name: "gnb", Key: map[string]string{"id": "gnb-001"}},
		{Name: "cell"},
	}}

	resp := updateResponse(prefix,
		&gpb.Update{
			Path: &gpb.Path{Elem: []*gpb.PathElem{
				{Name: "prb", Key: map[string]string{"direction": "dl"}},
				{Name: "utilization"},
			}},
			Val: &gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: 91.7}},
		},
	)

	metrics, err := gnmiparser.Parse(marshalResponse(t, resp))
	require.NoError(t, err)
	require.Len(t, metrics, 1)

	assert.Equal(t, "/gnb/cell/prb/utilization", metrics[0].Name)
	assert.Equal(t, map[string]string{
		"id":        "gnb-001",
		"direction": "dl",
	}, metrics[0].Labels)
}
