package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/argus-5g/argus/simulator/emitter"
	"github.com/argus-5g/argus/simulator/engine"
)

func main() {
	scenarioPath := flag.String("scenario", "simulator/scenarios/steady_state.yaml", "path to scenario YAML")
	flag.Parse()

	if err := run(*scenarioPath); err != nil {
		fmt.Fprintf(os.Stderr, "argus-sim: %v\n", err)
		os.Exit(1)
	}
}

func run(scenarioPath string) error {
	data, err := os.ReadFile(scenarioPath)
	if err != nil {
		return fmt.Errorf("read scenario: %w", err)
	}

	var scenario engine.Scenario
	if err := yaml.Unmarshal(data, &scenario); err != nil {
		return fmt.Errorf("parse scenario: %w", err)
	}

	log.Printf("Loaded scenario %q: %s", scenario.Name, scenario.Description)

	eng := engine.New(scenario)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start emitters for each NF.
	for _, nf := range scenario.NFs {
		switch nf.Protocol {
		case "prometheus":
			addr := fmt.Sprintf(":%d", nf.Port)
			p := emitter.NewPrometheus(eng, nf.InstanceID, addr)
			go func(id string, port int) {
				log.Printf("Starting Prometheus emitter for %s on :%d", id, port)
				if err := p.Start(ctx); err != nil {
					log.Printf("prometheus emitter %s: %v", id, err)
				}
			}(nf.InstanceID, nf.Port)

		case "gnmi":
			addr := fmt.Sprintf(":%d", nf.Port)
			// Build path-to-key mapping from the NF's metrics.
			// For gNMI, the metric names in the scenario ARE the keys.
			pathToKey := make(map[string]string)
			for _, m := range nf.Metrics {
				pathToKey["/"+m.Name] = m.Name
			}
			g := emitter.NewGNMI(eng, nf.InstanceID, addr, pathToKey, 15*time.Second)
			go func(id string, port int) {
				log.Printf("Starting gNMI emitter for %s on :%d", id, port)
				if err := g.Start(ctx); err != nil {
					log.Printf("gnmi emitter %s: %v", id, err)
				}
			}(nf.InstanceID, nf.Port)

		default:
			return fmt.Errorf("unsupported protocol %q for NF %s", nf.Protocol, nf.InstanceID)
		}
	}

	// Background clock: advance engine every 100ms.
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				eng.Advance(100 * time.Millisecond)
			}
		}
	}()

	// Allow emitters to bind before logging readiness.
	time.Sleep(200 * time.Millisecond)

	log.Printf("argus-sim running — scenario %q", scenario.Name)
	for _, nf := range scenario.NFs {
		log.Printf("  %s %s (%s) on :%d", nf.Vendor, nf.Type, nf.Protocol, nf.Port)
	}

	// Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("Received %s, shutting down...", sig)
	cancel()

	return nil
}
