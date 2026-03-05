package emitter_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/argus-5g/argus/simulator/emitter"
	"github.com/argus-5g/argus/simulator/engine"
)

func gnbScenario() engine.Scenario {
	return engine.Scenario{
		Name: "gnmi-test",
		NFs: []engine.SimulatedNF{
			{
				Type: "gNB", Vendor: "oai", InstanceID: "gnb-001",
				Protocol: "gnmi", Port: 0,
				Metrics: []engine.BaseMetric{
					{Name: "prb_utilization", Type: "gauge", Baseline: 0.65, Jitter: 0.05},
					{Name: "dl_throughput", Type: "gauge", Baseline: 1e9, Jitter: 1e8},
				},
			},
		},
	}
}

func startGNMIEmitter(t *testing.T, eng *engine.Engine, pathToKey map[string]string, interval time.Duration) *emitter.GNMI {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	g := emitter.NewGNMI(eng, "gnb-001", ":0", pathToKey, interval)

	errCh := make(chan error, 1)
	go func() { errCh <- g.Start(ctx) }()

	// Wait for the listener to bind.
	deadline := time.Now().Add(2 * time.Second)
	for g.Port() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	require.NotEqual(t, 0, g.Port(), "gnmi emitter failed to bind listener")

	t.Cleanup(func() {
		cancel()
		<-errCh
	})

	return g
}

func dialGNMI(t *testing.T, port int) gpb.GNMIClient {
	t.Helper()

	conn, err := grpc.NewClient(
		fmt.Sprintf("localhost:%d", port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return gpb.NewGNMIClient(conn)
}

func TestGNMIEmitter_SyncResponse(t *testing.T) {
	eng := engine.New(gnbScenario())
	pathToKey := map[string]string{
		"/gnb/cell/prb/utilization": "prb_utilization",
	}

	g := startGNMIEmitter(t, eng, pathToKey, 100*time.Millisecond)
	client := dialGNMI(t, g.Port())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Subscribe(ctx)
	require.NoError(t, err)

	err = stream.Send(&gpb.SubscribeRequest{
		Request: &gpb.SubscribeRequest_Subscribe{
			Subscribe: &gpb.SubscriptionList{
				Mode: gpb.SubscriptionList_STREAM,
				Subscription: []*gpb.Subscription{
					{
						Path:           &gpb.Path{Elem: []*gpb.PathElem{{Name: "gnb"}, {Name: "cell"}, {Name: "prb"}, {Name: "utilization"}}},
						Mode:           gpb.SubscriptionMode_SAMPLE,
						SampleInterval: uint64(100 * time.Millisecond),
					},
				},
			},
		},
	})
	require.NoError(t, err)

	// First response must be sync_response = true.
	resp, err := stream.Recv()
	require.NoError(t, err)
	assert.True(t, resp.GetSyncResponse(), "first response should be sync_response=true")
}

func TestGNMIEmitter_Subscribe(t *testing.T) {
	eng := engine.New(gnbScenario())
	pathToKey := map[string]string{
		"/gnb/cell/prb/utilization":     "prb_utilization",
		"/gnb/cell/throughput/downlink": "dl_throughput",
	}

	g := startGNMIEmitter(t, eng, pathToKey, 100*time.Millisecond)
	client := dialGNMI(t, g.Port())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Subscribe(ctx)
	require.NoError(t, err)

	err = stream.Send(&gpb.SubscribeRequest{
		Request: &gpb.SubscribeRequest_Subscribe{
			Subscribe: &gpb.SubscriptionList{
				Mode: gpb.SubscriptionList_STREAM,
				Subscription: []*gpb.Subscription{
					{
						Path:           &gpb.Path{Elem: []*gpb.PathElem{{Name: "gnb"}, {Name: "cell"}, {Name: "prb"}, {Name: "utilization"}}},
						Mode:           gpb.SubscriptionMode_SAMPLE,
						SampleInterval: uint64(100 * time.Millisecond),
					},
				},
			},
		},
	})
	require.NoError(t, err)

	// Consume sync response.
	resp, err := stream.Recv()
	require.NoError(t, err)
	require.True(t, resp.GetSyncResponse())

	// Next response should be an Update notification with metric values.
	resp, err = stream.Recv()
	require.NoError(t, err)

	update := resp.GetUpdate()
	require.NotNil(t, update, "expected Update notification")
	assert.NotEmpty(t, update.Update, "expected at least one Update in notification")
	assert.Greater(t, update.Timestamp, int64(0), "timestamp should be set")

	// Both paths should be present in the response.
	pathsSeen := make(map[string]bool)
	for _, u := range update.Update {
		var elems []string
		for _, e := range u.Path.Elem {
			elems = append(elems, e.Name)
		}
		pathsSeen["/"+joinElems(elems)] = true

		// Every value should be a non-zero double (baselines are 0.65 and 1e9).
		assert.NotZero(t, u.GetVal().GetDoubleVal(), "metric value should be non-zero")
	}
	assert.True(t, pathsSeen["/gnb/cell/prb/utilization"], "expected prb_utilization path")
	assert.True(t, pathsSeen["/gnb/cell/throughput/downlink"], "expected dl_throughput path")
}

func TestGNMIEmitter_Port(t *testing.T) {
	eng := engine.New(gnbScenario())
	pathToKey := map[string]string{"/gnb/cell/prb/utilization": "prb_utilization"}

	g := startGNMIEmitter(t, eng, pathToKey, time.Second)

	port := g.Port()
	assert.NotEqual(t, 0, port, "Port() should return non-zero after Start")
	assert.Greater(t, port, 1024, "Port() should be an unprivileged port")
}

// joinElems joins path element names with "/".
func joinElems(elems []string) string {
	result := ""
	for i, e := range elems {
		if i > 0 {
			result += "/"
		}
		result += e
	}
	return result
}
