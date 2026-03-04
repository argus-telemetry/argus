package emitter

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/argus-5g/argus/simulator/engine"
)

// Prometheus serves Prometheus exposition format metrics for a single simulated NF.
// On each /metrics request, it advances the engine clock by real elapsed time
// since the last request and returns the current state.
type Prometheus struct {
	eng        *engine.Engine
	instanceID string
	addr       string
	server     *http.Server
	listener   net.Listener

	mu       sync.Mutex
	lastTick time.Time
}

// NewPrometheus creates a Prometheus emitter for the given engine and NF instance.
func NewPrometheus(eng *engine.Engine, instanceID string, addr string) *Prometheus {
	return &Prometheus{
		eng:        eng,
		instanceID: instanceID,
		addr:       addr,
	}
}

// Start begins serving /metrics. Binds the listener synchronously, then blocks
// on Serve until Stop is called or context is cancelled.
func (p *Prometheus) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", p.handleMetrics)

	ln, err := net.Listen("tcp", p.addr)
	if err != nil {
		return fmt.Errorf("prometheus emitter listen on %s: %w", p.addr, err)
	}

	srv := &http.Server{Handler: mux}

	p.mu.Lock()
	p.listener = ln
	p.server = srv
	p.lastTick = time.Now()
	p.mu.Unlock()

	go func() {
		<-ctx.Done()
		p.Stop()
	}()

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Port returns the actual listening port. Thread-safe — safe to call while
// Start is running in another goroutine.
func (p *Prometheus) Port() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listener == nil {
		return 0
	}
	return p.listener.Addr().(*net.TCPAddr).Port
}

// Stop gracefully shuts down the HTTP server.
func (p *Prometheus) Stop() {
	p.mu.Lock()
	srv := p.server
	p.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

func (p *Prometheus) handleMetrics(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	now := time.Now()
	elapsed := now.Sub(p.lastTick)
	p.lastTick = now
	p.mu.Unlock()

	// Advance engine by real elapsed time since last scrape.
	p.eng.Advance(elapsed)

	body := p.eng.PrometheusOutput(p.instanceID)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
