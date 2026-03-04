package emitter

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"

	"github.com/argus-5g/argus/simulator/engine"
)

// GNMI serves gNMI SAMPLE-mode telemetry for a simulated NF instance.
// On each sample interval tick, it reads current metric values from the engine
// and pushes a SubscribeResponse containing Update notifications to all
// connected subscribers.
type GNMI struct {
	gpb.UnimplementedGNMIServer
	eng            *engine.Engine
	instanceID     string
	addr           string
	pathToKey      map[string]string // gNMI path string -> engine metric key
	sampleInterval time.Duration
	grpcServer     *grpc.Server
	listener       net.Listener
	mu             sync.Mutex
}

// NewGNMI creates a gNMI emitter.
// pathToKey maps gNMI path strings (e.g. "/gnb/cell/prb/utilization") to engine
// metric keys (e.g. "prb_utilization").
func NewGNMI(eng *engine.Engine, instanceID, addr string, pathToKey map[string]string, sampleInterval time.Duration) *GNMI {
	return &GNMI{
		eng:            eng,
		instanceID:     instanceID,
		addr:           addr,
		pathToKey:      pathToKey,
		sampleInterval: sampleInterval,
	}
}

// Start begins serving gNMI on the configured address. Binds the listener
// synchronously, then blocks on Serve until Stop is called or ctx is cancelled.
func (g *GNMI) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", g.addr)
	if err != nil {
		return fmt.Errorf("gnmi emitter listen on %s: %w", g.addr, err)
	}

	srv := grpc.NewServer()
	gpb.RegisterGNMIServer(srv, g)

	g.mu.Lock()
	g.listener = ln
	g.grpcServer = srv
	g.mu.Unlock()

	go func() {
		<-ctx.Done()
		g.Stop()
	}()

	if err := srv.Serve(ln); err != nil {
		// Serve returns nil on GracefulStop; treat context cancellation as clean exit.
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}

// Port returns the actual listening port. Thread-safe — safe to call while
// Start is running in another goroutine.
func (g *GNMI) Port() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.listener == nil {
		return 0
	}
	return g.listener.Addr().(*net.TCPAddr).Port
}

// Stop gracefully stops the gRPC server.
func (g *GNMI) Stop() {
	g.mu.Lock()
	srv := g.grpcServer
	g.mu.Unlock()
	if srv != nil {
		srv.GracefulStop()
	}
}

// Subscribe implements the gNMI Subscribe RPC. Only STREAM mode with SAMPLE
// subscriptions is supported — matching the gNodeB telemetry pattern where the
// server pushes periodic updates at the configured sample interval.
func (g *GNMI) Subscribe(stream gpb.GNMI_SubscribeServer) error {
	req, err := stream.Recv()
	if err != nil {
		return err
	}

	sub := req.GetSubscribe()
	if sub == nil {
		return fmt.Errorf("expected SubscribeRequest with Subscribe, got %T", req.GetRequest())
	}

	if sub.GetMode() != gpb.SubscriptionList_STREAM {
		return fmt.Errorf("only STREAM mode supported, got %s", sub.GetMode())
	}

	// Use client-requested sample interval if provided, otherwise fall back
	// to the configured default.
	interval := g.sampleInterval
	if len(sub.Subscription) > 0 && sub.Subscription[0].SampleInterval > 0 {
		interval = time.Duration(sub.Subscription[0].SampleInterval)
	}

	// Send initial sync_response per gNMI spec — signals the client that
	// initial state has been delivered (we have none, so sync immediately).
	if err := stream.Send(&gpb.SubscribeResponse{
		Response: &gpb.SubscribeResponse_SyncResponse{SyncResponse: true},
	}); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			g.eng.Advance(interval)

			values := g.eng.GNMIValues(g.instanceID, g.pathToKey)

			var updates []*gpb.Update
			for path, val := range values {
				updates = append(updates, &gpb.Update{
					Path: gnmiParsePath(path),
					Val:  &gpb.TypedValue{Value: &gpb.TypedValue_DoubleVal{DoubleVal: val}},
				})
			}

			resp := &gpb.SubscribeResponse{
				Response: &gpb.SubscribeResponse_Update{
					Update: &gpb.Notification{
						Timestamp: time.Now().UnixNano(),
						Update:    updates,
					},
				},
			}

			if err := stream.Send(resp); err != nil {
				return err
			}
		}
	}
}

// gnmiParsePath converts a slash-delimited gNMI path string to a gnmi.Path.
func gnmiParsePath(path string) *gpb.Path {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return &gpb.Path{}
	}
	parts := strings.Split(path, "/")
	elems := make([]*gpb.PathElem, len(parts))
	for i, p := range parts {
		elems[i] = &gpb.PathElem{Name: p}
	}
	return &gpb.Path{Elem: elems}
}
