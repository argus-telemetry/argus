package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/argus-5g/argus/internal/schema"
)

func addVendorCmd(args []string) error {
	var vendor, nfList, outputDir string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--vendor":
			if i+1 < len(args) {
				vendor = args[i+1]
				i++
			}
		case "--nfs":
			if i+1 < len(args) {
				nfList = args[i+1]
				i++
			}
		case "--output":
			if i+1 < len(args) {
				outputDir = args[i+1]
				i++
			}
		}
	}

	if vendor == "" {
		return fmt.Errorf("--vendor is required")
	}
	if nfList == "" {
		nfList = "amf,smf,upf,gnb,slice"
	}
	if outputDir == "" {
		outputDir = "schema/v1"
	}

	nfs := strings.Split(nfList, ",")
	var modified []string

	for _, nf := range nfs {
		nf = strings.TrimSpace(nf)
		schemaFile := filepath.Join(outputDir, nf+".yaml")

		updated, err := addVendorToSchema(schemaFile, vendor)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: %v\n", nf, err)
			continue
		}
		if updated {
			modified = append(modified, nf)
		} else {
			fmt.Fprintf(os.Stderr, "  %s: vendor %q already exists, skipping\n", nf, vendor)
		}
	}

	// Generate skeleton scenario file.
	scenarioPath, err := generateScenario(vendor, outputDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to generate scenario: %v\n", err)
	}

	// Print summary.
	fmt.Printf("\nadd-vendor: %s\n", vendor)
	fmt.Printf("  Modified schemas: %s\n", strings.Join(modified, ", "))
	if scenarioPath != "" {
		fmt.Printf("  Generated scenario: %s\n", scenarioPath)
	}

	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Fill in source_template for each KPI in the modified schema files\n")
	fmt.Printf("  2. Update the generated scenario with correct metric names\n")
	fmt.Printf("  3. Run: argus-certify run --scenario %s\n", scenarioPath)
	fmt.Printf("  4. Add documentation to docs/vendor-connectors/%s/\n", vendor)
	fmt.Printf("  5. Submit PR with your connector\n")

	return nil
}

// addVendorToSchema reads a schema YAML, adds placeholder vendor mappings if the
// vendor does not already exist, and appends a YAML snippet to the file.
// This avoids yaml.Marshal round-tripping which destroys comments and formatting.
func addVendorToSchema(path, vendor string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}

	var s schema.NFSchema
	if err := yaml.Unmarshal(data, &s); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}

	// Check if vendor already exists.
	if _, exists := s.Mappings[vendor]; exists {
		return false, nil
	}

	// Collect non-derived KPIs for placeholder generation.
	var kpis []schema.KPIDefinition
	for _, kpi := range s.KPIs {
		if !kpi.Derived {
			kpis = append(kpis, kpi)
		}
	}

	// Build YAML text snippet and append to file.
	snippet := buildVendorSnippet(vendor, kpis)

	// Ensure file ends with a newline before appending.
	content := string(data)
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	content += snippet

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}

	return true, nil
}

// buildVendorSnippet generates a YAML text block for a new vendor mapping.
func buildVendorSnippet(vendor string, kpis []schema.KPIDefinition) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n  %s:\n", vendor)
	sb.WriteString("    source_protocol: prometheus\n")
	sb.WriteString("    metrics:\n")

	for _, kpi := range kpis {
		metricType := "gauge"
		resetAware := "false"
		if strings.Contains(kpi.Unit, "count") || kpi.Unit == "count" {
			metricType = "counter"
			resetAware = "true"
		}
		fmt.Fprintf(&sb, "      %s:\n", kpi.Name)
		fmt.Fprintf(&sb, "        source_template: \"%s:{{.Job}}:{{.Reader}}:REPLACE_WITH_COUNTER_NAME:{{.Instance}}\"\n", vendor)
		fmt.Fprintf(&sb, "        type: %s\n", metricType)
		fmt.Fprintf(&sb, "        reset_aware: %s\n", resetAware)
		sb.WriteString("        label_match_strategy: exact\n")
		sb.WriteString("        label_extract:\n")
		sb.WriteString("          - segment: 4\n")
		sb.WriteString("            label: instance_id\n")
	}

	return sb.String()
}

// generateScenario creates a skeleton alarm_storm scenario for the new vendor.
func generateScenario(vendor, schemaDir string) (string, error) {
	scenarioDir := "test/scenarios/matrix"
	scenarioPath := filepath.Join(scenarioDir, vendor+"_amf_alarm_storm.yaml")

	// Don't overwrite an existing scenario.
	if _, err := os.Stat(scenarioPath); err == nil {
		return scenarioPath, nil
	}

	// Read AMF schema to get the base KPI names for the scenario.
	amfPath := filepath.Join(schemaDir, "amf.yaml")
	data, err := os.ReadFile(amfPath)
	if err != nil {
		return "", fmt.Errorf("read amf schema: %w", err)
	}

	var s schema.NFSchema
	if err := yaml.Unmarshal(data, &s); err != nil {
		return "", fmt.Errorf("parse amf schema: %w", err)
	}

	// Build scenario content.
	var sb strings.Builder
	fmt.Fprintf(&sb, "name: %s_amf_alarm_storm\n", vendor)
	fmt.Fprintf(&sb, "description: Registration storm scenario — %s vendor metrics\n", vendor)
	sb.WriteString("duration: 300\n\n")
	sb.WriteString("nfs:\n")
	sb.WriteString("  - type: AMF\n")
	fmt.Fprintf(&sb, "    vendor: %s\n", vendor)
	sb.WriteString("    instance_id: amf-001\n")
	sb.WriteString("    protocol: prometheus\n")
	sb.WriteString("    port: 9090\n")
	sb.WriteString("    metrics:\n")
	fmt.Fprintf(&sb, "      - name: \"%s:job_1:reader_1:REPLACE_REG_ATTEMPTS:amf_001\"\n", vendor)
	sb.WriteString("        labels: {}\n")
	sb.WriteString("        type: counter\n")
	sb.WriteString("        baseline: 10000\n")
	sb.WriteString("        rate_per_second: 15\n")
	fmt.Fprintf(&sb, "      - name: \"%s:job_1:reader_1:REPLACE_REG_FAILURES:amf_001\"\n", vendor)
	sb.WriteString("        labels: {}\n")
	sb.WriteString("        type: counter\n")
	sb.WriteString("        baseline: 30\n")
	sb.WriteString("        rate_per_second: 0.05\n")
	fmt.Fprintf(&sb, "      - name: \"%s:job_1:reader_1:REPLACE_UE_CONNECTED:amf_001\"\n", vendor)
	sb.WriteString("        labels: {}\n")
	sb.WriteString("        type: gauge\n")
	sb.WriteString("        baseline: 950\n")
	sb.WriteString("        jitter: 50\n")
	sb.WriteString("    events:\n")
	sb.WriteString("      - name: registration_storm\n")
	sb.WriteString("        start_sec: 60\n")
	sb.WriteString("        duration_sec: 120\n")
	fmt.Fprintf(&sb, "        metric: \"%s:job_1:reader_1:REPLACE_REG_ATTEMPTS:amf_001\"\n", vendor)
	sb.WriteString("        rate_scale: 50\n")
	sb.WriteString("      - name: rejection_spike\n")
	sb.WriteString("        start_sec: 60\n")
	sb.WriteString("        duration_sec: 120\n")
	fmt.Fprintf(&sb, "        metric: \"%s:job_1:reader_1:REPLACE_REG_FAILURES:amf_001\"\n", vendor)
	sb.WriteString("        rate_scale: 10000\n")
	sb.WriteString("      - name: ue_drop\n")
	sb.WriteString("        start_sec: 60\n")
	sb.WriteString("        duration_sec: 120\n")
	fmt.Fprintf(&sb, "        metric: \"%s:job_1:reader_1:REPLACE_UE_CONNECTED:amf_001\"\n", vendor)
	sb.WriteString("        override: 200\n")
	sb.WriteString("\nexpected_events:\n")
	sb.WriteString("  - rule: RegistrationStorm\n")
	sb.WriteString("    severity: critical\n")
	sb.WriteString("    within_seconds: 15\n")
	sb.WriteString("    affected_nfs: [AMF]\n")

	if err := os.WriteFile(scenarioPath, []byte(sb.String()), 0644); err != nil {
		return "", fmt.Errorf("write scenario: %w", err)
	}

	return scenarioPath, nil
}
