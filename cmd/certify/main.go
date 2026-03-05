// argus-certify validates that Argus correlation rules produce correct events
// for a given scenario. Exercises the full normalization + correlation pipeline
// in-process with the simulator engine as stimulus.
//
// Usage:
//
//	argus-certify run --scenario test/scenarios/alarm_storm.yaml
//	argus-certify list-scenarios ./scenarios/
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/argus-5g/argus/internal/collector"
	"github.com/argus-5g/argus/internal/correlator"
	"github.com/argus-5g/argus/internal/normalizer"
	"github.com/argus-5g/argus/internal/schema"
	"github.com/argus-5g/argus/internal/sim"
	simengine "github.com/argus-5g/argus/simulator/engine"
)

const version = "0.3.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		if err := runCmd(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "argus-certify run: %v\n", err)
			os.Exit(1)
		}
	case "list-scenarios":
		if err := listCmd(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "argus-certify list-scenarios: %v\n", err)
			os.Exit(1)
		}
	case "matrix":
		if err := matrixCmd(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "argus-certify matrix: %v\n", err)
			os.Exit(1)
		}
	case "add-vendor":
		if err := addVendorCmd(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "argus-certify add-vendor: %v\n", err)
			os.Exit(1)
		}
	case "version":
		fmt.Printf("argus-certify v%s\n", version)
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "  argus-certify run --scenario <path> [--timeout 60s] [--verbose] [--output text|json]\n")
	fmt.Fprintf(os.Stderr, "  argus-certify matrix --matrix-dir <directory> [--timeout 60s]\n")
	fmt.Fprintf(os.Stderr, "  argus-certify list-scenarios <directory>\n")
	fmt.Fprintf(os.Stderr, "  argus-certify add-vendor --vendor <name> [--nfs amf,smf,upf,gnb,slice] [--output schema/v1/]\n")
	fmt.Fprintf(os.Stderr, "  argus-certify version\n")
}

// runCmd executes a single scenario and reports results.
func runCmd(args []string) error {
	var scenarioPath string
	var timeoutStr string
	var verbose bool
	var outputFmt string

	// Simple flag parsing — avoid flag package to not collide with subcommands.
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--scenario":
			if i+1 < len(args) {
				scenarioPath = args[i+1]
				i++
			}
		case "--timeout":
			if i+1 < len(args) {
				timeoutStr = args[i+1]
				i++
			}
		case "--verbose":
			verbose = true
		case "--output":
			if i+1 < len(args) {
				outputFmt = args[i+1]
				i++
			}
		}
	}

	if scenarioPath == "" {
		return fmt.Errorf("--scenario is required")
	}
	if outputFmt == "" {
		outputFmt = "text"
	}

	timeout := 60 * time.Second
	if timeoutStr != "" {
		var err error
		timeout, err = time.ParseDuration(timeoutStr)
		if err != nil {
			return fmt.Errorf("parse --timeout: %w", err)
		}
	}

	scenario, err := loadScenario(scenarioPath)
	if err != nil {
		return err
	}

	result := executeScenario(scenario, timeout)
	result.Scenario = scenario.Name

	switch outputFmt {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	default:
		printTextResult(scenario, result, verbose)
	}

	if !result.Passed {
		os.Exit(1)
	}
	return nil
}

// executeScenario runs the full in-process pipeline: simulator → normalizer → correlator → asserter.
func executeScenario(scenario simengine.Scenario, timeout time.Duration) sim.AssertResult {
	// Load schema registry.
	reg, err := schema.LoadFromDir("schema/v1")
	if err != nil {
		return sim.AssertResult{
			Passed: false,
			Failures: []sim.AssertFailure{{
				Rule:   "_setup",
				Reason: fmt.Sprintf("load schema: %v", err),
			}},
		}
	}

	// Create normalizer engine.
	normEngine := normalizer.NewEngine(reg, nil)

	// Create correlator with all rules.
	rules := []correlator.CorrelationRule{
		&correlator.RegistrationStorm{},
		&correlator.SessionDrop{},
		&correlator.RANCoreDivergence{},
	}
	corrEngine := correlator.NewEngine(30*time.Second, rules)

	sink := make(chan correlator.CorrelationEvent, 256)
	corrEngine.RegisterEventSink(sink)

	// Create and advance simulator engine.
	simEng := simengine.New(scenario)

	// Warm-up scrape: prime the normalizer's counter store so the first real
	// scrape produces a clean delta instead of the raw counter baseline.
	for _, nf := range scenario.NFs {
		if nf.Protocol != "prometheus" {
			continue
		}
		payload := simEng.PrometheusOutput(nf.InstanceID)
		if len(payload) == 0 {
			continue
		}
		raw := collector.RawRecord{
			Source: collector.SourceInfo{
				Vendor: nf.Vendor, NFType: nf.Type, InstanceID: nf.InstanceID,
			},
			Payload: payload, Protocol: collector.ProtocolPrometheus,
			Timestamp: time.Now(), SchemaVersion: "v1",
		}
		// Discard warm-up results — only priming the counter store.
		_, _ = normEngine.Normalize(raw)
	}

	// Override within_seconds from timeout if timeout is shorter.
	for i := range scenario.ExpectedEvents {
		if scenario.ExpectedEvents[i].WithinSeconds == 0 {
			scenario.ExpectedEvents[i].WithinSeconds = int(timeout.Seconds())
		}
	}

	// Run the pipeline loop: advance simulator, scrape, normalize, correlate.
	// Sim time is decoupled from real time — advance 1s of sim time per tick
	// (10ms real sleep) so a 300s scenario completes in ~3s real time.
	done := make(chan struct{})
	go func() {
		defer close(done)
		simStep := 1 * time.Second
		realSleep := 10 * time.Millisecond
		evalEvery := 5 // evaluate correlator every N ticks (= every 5s sim time)
		tick := 0
		deadline := time.After(timeout)

		// Determine total sim duration from scenario.
		simDuration := time.Duration(scenario.Duration) * time.Second
		if simDuration == 0 {
			simDuration = timeout
		}
		var simElapsed time.Duration

		for simElapsed < simDuration {
			select {
			case <-deadline:
				return
			default:
			}

			simEng.Advance(simStep)
			simElapsed += simStep
			tick++

			// Scrape each NF's Prometheus output and normalize it.
			for _, nf := range scenario.NFs {
				if nf.Protocol != "prometheus" {
					continue
				}

				payload := simEng.PrometheusOutput(nf.InstanceID)
				if len(payload) == 0 {
					continue
				}

				raw := collector.RawRecord{
					Source: collector.SourceInfo{
						Vendor:     nf.Vendor,
						NFType:     nf.Type,
						InstanceID: nf.InstanceID,
					},
					Payload:       payload,
					Protocol:      collector.ProtocolPrometheus,
					Timestamp:     time.Now(),
					SchemaVersion: "v1",
				}

				result, err := normEngine.Normalize(raw)
				if err != nil {
					continue
				}

				for _, rec := range result.Records {
					corrEngine.Ingest(rec)
				}
			}

			// Evaluate correlator rules periodically.
			if tick%evalEvery == 0 {
				corrEngine.EvaluateAll(time.Now())
			}

			time.Sleep(realSleep)
		}
	}()

	// Run asserter against the event sink.
	asserter := sim.NewAsserter()
	result := asserter.Evaluate(scenario, sink)

	return result
}

func printTextResult(scenario simengine.Scenario, result sim.AssertResult, verbose bool) {
	fmt.Printf("argus-certify v%s\n", version)
	fmt.Printf("Running scenario: %s\n\n", scenario.Name)

	for _, detail := range result.Details {
		if detail.Passed {
			fmt.Printf("[PASS] %s fired within %s (limit: %s)\n",
				detail.Rule,
				detail.Elapsed.Truncate(time.Millisecond),
				detail.Limit)
		} else {
			fmt.Printf("[FAIL] %s\n", detail.Rule)
			if detail.Reason != "" {
				fmt.Printf("       %s\n", detail.Reason)
			}
		}

		if verbose {
			for _, check := range detail.Checks {
				symbol := "✓"
				if !check.Passed {
					symbol = "✗"
				}
				switch check.Op {
				case "eq", "subset":
					fmt.Printf("       %s %s %s\n", check.Field, symbol, check.Op)
				default:
					fmt.Printf("       %s=%.4g %s %.4g %s\n",
						check.Field, check.Actual, check.Op, check.Expected, symbol)
				}
			}
		}
	}

	// Handle zero-expected (steady_state) scenarios.
	if len(scenario.ExpectedEvents) == 0 {
		if result.Passed {
			fmt.Printf("[PASS] No false positives in steady_state window\n")
		} else {
			fmt.Printf("[FAIL] Unexpected events during steady_state\n")
			for _, f := range result.Failures {
				fmt.Printf("       %s\n", f.Reason)
			}
		}
	}

	total := result.Total
	if total == 0 && len(scenario.ExpectedEvents) == 0 {
		total = 1 // steady_state counts as 1 assertion
	}
	passCount := result.PassCount
	if passCount == 0 && result.Passed && len(scenario.ExpectedEvents) == 0 {
		passCount = 1
	}
	fmt.Printf("\nPassed: %d/%d assertions\n", passCount, total)
	fmt.Printf("Duration: %s\n", result.Duration.Truncate(time.Millisecond))
}

// listCmd lists scenario YAMLs in a directory.
func listCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("directory path is required")
	}
	dir := args[0]

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read directory: %w", err)
	}

	type scenarioInfo struct {
		name           string
		expectedEvents int
		description    string
	}

	var scenarios []scenarioInfo
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml")) {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var s simengine.Scenario
		if err := yaml.Unmarshal(data, &s); err != nil {
			continue
		}
		if s.Name == "" {
			continue
		}
		scenarios = append(scenarios, scenarioInfo{
			name:           s.Name,
			expectedEvents: len(s.ExpectedEvents),
			description:    s.Description,
		})
	}

	sort.Slice(scenarios, func(i, j int) bool {
		return scenarios[i].name < scenarios[j].name
	})

	fmt.Printf("%-25s %-17s %s\n", "SCENARIO", "EXPECTED EVENTS", "DESCRIPTION")
	for _, s := range scenarios {
		fmt.Printf("%-25s %-17d %s\n", s.name, s.expectedEvents, s.description)
	}

	return nil
}

// matrixCmd runs all scenarios in a directory and groups results by assertion.
func matrixCmd(args []string) error {
	var matrixDir string
	var timeoutStr string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--matrix-dir":
			if i+1 < len(args) {
				matrixDir = args[i+1]
				i++
			}
		case "--timeout":
			if i+1 < len(args) {
				timeoutStr = args[i+1]
				i++
			}
		}
	}

	if matrixDir == "" {
		return fmt.Errorf("--matrix-dir is required")
	}

	timeout := 60 * time.Second
	if timeoutStr != "" {
		var err error
		timeout, err = time.ParseDuration(timeoutStr)
		if err != nil {
			return fmt.Errorf("parse --timeout: %w", err)
		}
	}

	entries, err := os.ReadDir(matrixDir)
	if err != nil {
		return fmt.Errorf("read matrix dir: %w", err)
	}

	type scenarioResult struct {
		scenario simengine.Scenario
		result   sim.AssertResult
		vendor   string
	}

	var results []scenarioResult
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml")) {
			continue
		}

		scenario, err := loadScenario(filepath.Join(matrixDir, entry.Name()))
		if err != nil {
			continue
		}

		// Extract vendor from first NF in the scenario.
		vendor := "unknown"
		if len(scenario.NFs) > 0 {
			vendor = scenario.NFs[0].Vendor
		}

		result := executeScenario(scenario, timeout)
		result.Scenario = scenario.Name
		results = append(results, scenarioResult{
			scenario: scenario,
			result:   result,
			vendor:   vendor,
		})
	}

	// Group results by assertion rule name.
	type assertionGroup struct {
		rule    string
		entries []struct {
			vendor string
			passed bool
			dur    time.Duration
		}
	}

	groupMap := make(map[string]*assertionGroup)
	var groupOrder []string

	for _, sr := range results {
		if len(sr.scenario.ExpectedEvents) == 0 {
			// Steady state — group as "No false positives"
			key := "No false positives on steady_state"
			if groupMap[key] == nil {
				groupMap[key] = &assertionGroup{rule: key}
				groupOrder = append(groupOrder, key)
			}
			groupMap[key].entries = append(groupMap[key].entries, struct {
				vendor string
				passed bool
				dur    time.Duration
			}{vendor: sr.vendor, passed: sr.result.Passed, dur: sr.result.Duration})
		} else {
			for _, exp := range sr.scenario.ExpectedEvents {
				key := fmt.Sprintf("%s fires on %s", exp.Rule, baseScenarioName(sr.scenario.Name))
				if groupMap[key] == nil {
					groupMap[key] = &assertionGroup{rule: key}
					groupOrder = append(groupOrder, key)
				}
				// Find if this specific assertion passed.
				passed := false
				var dur time.Duration
				for _, d := range sr.result.Details {
					if d.Rule == exp.Rule {
						passed = d.Passed
						dur = d.Elapsed
						break
					}
				}
				groupMap[key].entries = append(groupMap[key].entries, struct {
					vendor string
					passed bool
					dur    time.Duration
				}{vendor: sr.vendor, passed: passed, dur: dur})
			}
		}
	}

	// Print grouped results.
	fmt.Printf("argus-certify matrix --matrix-dir %s\n\n", matrixDir)

	totalAssertions := 0
	passedAssertions := 0

	for _, key := range groupOrder {
		group := groupMap[key]
		fmt.Printf("Assertion: %s\n", group.rule)
		for _, e := range group.entries {
			totalAssertions++
			if e.passed {
				passedAssertions++
				fmt.Printf("  %-10s [PASS] %s\n", e.vendor, e.dur.Truncate(time.Millisecond))
			} else {
				fmt.Printf("  %-10s [FAIL]\n", e.vendor)
			}
		}
		fmt.Println()
	}

	vendorSet := make(map[string]bool)
	for _, sr := range results {
		vendorSet[sr.vendor] = true
	}
	fmt.Printf("Matrix: %d/%d passed across %d vendors\n", passedAssertions, totalAssertions, len(vendorSet))

	if passedAssertions < totalAssertions {
		os.Exit(1)
	}
	return nil
}

// baseScenarioName strips vendor prefix from scenario name for grouping.
// e.g. "free5gc_alarm_storm" → "alarm_storm", "open5gs_alarm_storm" → "alarm_storm"
func baseScenarioName(name string) string {
	prefixes := []string{"free5gc_", "open5gs_", "oai_", "nokia_", "ericsson_"}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return strings.TrimPrefix(name, p)
		}
	}
	return name
}

func loadScenario(path string) (simengine.Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return simengine.Scenario{}, fmt.Errorf("read scenario: %w", err)
	}
	var s simengine.Scenario
	if err := yaml.Unmarshal(data, &s); err != nil {
		return simengine.Scenario{}, fmt.Errorf("parse scenario: %w", err)
	}
	return s, nil
}
