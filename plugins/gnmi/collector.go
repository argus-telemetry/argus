package gnmi

import (
	"context"
	"fmt"
	"strings"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	"github.com/argus-5g/argus/internal/collector"
)

// Extra config keys (via CollectorConfig.Extra):
//
//	gnmi_paths:      []any ([]string) — gNMI subscription paths (e.g. ["/gnb/cell/prb/utilization"])
//	sample_interval: string — sample interval duration (default: CollectorConfig.Interval)

// Collector subscribes to gNMI SAMPLE-mode telemetry from a gNodeB.
type Collector struct {
	nfType   string
	cfg      collector.CollectorConfig
	conn     *grpc.ClientConn
	paths    []string
	interval time.Duration
}

// Name returns the collector's identifier (e.g. "gnmi-gnb").
func (c *Collector) Name() string {
	return "gnmi-" + strings.ToLower(c.nfType)
}

// Connect initializes the gRPC connection and stores the configuration.
// Validates that an endpoint, positive interval, and gnmi_paths are configured.
func (c *Collector) Connect(ctx context.Context, cfg collector.CollectorConfig) error {
	if cfg.Endpoint == "" {
		return fmt.Errorf("gnmi %s collector: endpoint must not be empty", c.nfType)
	}
	if cfg.Interval <= 0 {
		return fmt.Errorf("gnmi %s collector: interval must be positive", c.nfType)
	}
	c.cfg = cfg
	c.interval = cfg.Interval

	// Extract gNMI paths from Extra config.
	if paths, ok := cfg.Extra["gnmi_paths"]; ok {
		if pathList, ok := paths.([]any); ok {
			for _, p := range pathList {
				if s, ok := p.(string); ok {
					c.paths = append(c.paths, s)
				}
			}
		}
	}
	if len(c.paths) == 0 {
		return fmt.Errorf("gnmi %s collector: gnmi_paths must be configured in Extra", c.nfType)
	}

	// Extract optional sample_interval from Extra.
	if si, ok := cfg.Extra["sample_interval"]; ok {
		if s, ok := si.(string); ok {
			d, err := time.ParseDuration(s)
			if err != nil {
				return fmt.Errorf("gnmi %s collector: invalid sample_interval %q: %w", c.nfType, s, err)
			}
			c.interval = d
		}
	}

	// Establish gRPC connection.
	var creds grpc.DialOption
	if cfg.TLS != nil && !cfg.TLS.SkipVerify {
		tlsCreds, err := credentials.NewClientTLSFromFile(cfg.TLS.CAFile, "")
		if err != nil {
			return fmt.Errorf("gnmi %s collector: TLS config: %w", c.nfType, err)
		}
		creds = grpc.WithTransportCredentials(tlsCreds)
	} else {
		creds = grpc.WithTransportCredentials(insecure.NewCredentials())
	}

	conn, err := grpc.NewClient(cfg.Endpoint, creds)
	if err != nil {
		return fmt.Errorf("gnmi %s collector: dial %s: %w", c.nfType, cfg.Endpoint, err)
	}
	c.conn = conn
	return nil
}

// Collect blocks and continuously receives gNMI SAMPLE-mode telemetry updates.
// Each SubscribeResponse is serialized as a protobuf RawRecord. Returns when
// ctx is cancelled or on unrecoverable stream error.
func (c *Collector) Collect(ctx context.Context, ch chan<- collector.RawRecord) error {
	client := gpb.NewGNMIClient(c.conn)

	// Build subscription list.
	var subs []*gpb.Subscription
	for _, p := range c.paths {
		subs = append(subs, &gpb.Subscription{
			Path:           parsePath(p),
			Mode:           gpb.SubscriptionMode_SAMPLE,
			SampleInterval: uint64(c.interval.Nanoseconds()),
		})
	}

	req := &gpb.SubscribeRequest{
		Request: &gpb.SubscribeRequest_Subscribe{
			Subscribe: &gpb.SubscriptionList{
				Subscription: subs,
				Mode:         gpb.SubscriptionList_STREAM,
			},
		},
	}

	stream, err := client.Subscribe(ctx)
	if err != nil {
		return fmt.Errorf("gnmi subscribe: %w", err)
	}

	if err := stream.Send(req); err != nil {
		return fmt.Errorf("gnmi send subscribe request: %w", err)
	}

	for {
		resp, err := stream.Recv()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("gnmi recv: %w", err)
		}

		// Only process update responses (skip sync_response).
		if resp.GetUpdate() == nil {
			continue
		}

		payload, err := proto.Marshal(resp)
		if err != nil {
			continue // skip malformed responses
		}

		record := collector.RawRecord{
			Source: collector.SourceInfo{
				Vendor:   "oai", // gNMI gNB defaults to OAI vendor
				NFType:   c.nfType,
				Endpoint: c.cfg.Endpoint,
			},
			Payload:       payload,
			Protocol:      collector.ProtocolGNMI,
			Timestamp:     time.Now(),
			SchemaVersion: "v1",
		}

		select {
		case ch <- record:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Close releases the gRPC connection.
func (c *Collector) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// parsePath converts a slash-delimited gNMI path string to a gnmi.Path proto.
func parsePath(path string) *gpb.Path {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return &gpb.Path{}
	}
	parts := strings.Split(path, "/")
	var elems []*gpb.PathElem
	for _, p := range parts {
		elems = append(elems, &gpb.PathElem{Name: p})
	}
	return &gpb.Path{Elem: elems}
}
