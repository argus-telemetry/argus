// Package dsl provides template-based metric path matching and expansion
// for vendor mapping DSL. Supports variable substitution for vendor-specific
// path translation (e.g. Nokia ENM, Ericsson PM).
package dsl

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// Vars holds template variables available in source_template expressions.
type Vars struct {
	NF       string
	Vendor   string
	Metric   string
	Instance string
	PLMN     string
	Slice    string
}

// ExpandTemplate renders a source_template with the given variables.
// Template syntax follows Go text/template: {{.NF}}, {{.Vendor}}, etc.
func ExpandTemplate(tmpl string, vars Vars) (string, error) {
	t, err := template.New("").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", tmpl, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("execute template %q: %w", tmpl, err)
	}
	return buf.String(), nil
}

// MatchTemplate attempts to match a concrete path against a template pattern.
// Returns extracted variable bindings and true if the path matches the template
// structure, or (nil, false) if it does not match.
//
// Matching is segment-based: the template is expanded with placeholder markers,
// then each segment is compared. Template segments (those containing "{{") become
// wildcards that capture the corresponding path segment value.
func MatchTemplate(tmpl string, path string) (map[string]string, bool) {
	// Extract variable names from the template.
	varNames := extractVarNames(tmpl)
	if len(varNames) == 0 {
		// No variables — exact match.
		return nil, tmpl == path
	}

	// Split both template and path into segments.
	tmplParts := splitPath(tmpl)
	pathParts := splitPath(path)

	if len(tmplParts) != len(pathParts) {
		return nil, false
	}

	vars := make(map[string]string)
	for i, tp := range tmplParts {
		if isTemplateSegment(tp) {
			// This segment is a variable — extract its value.
			name := extractSingleVarName(tp)
			if name != "" {
				vars[name] = pathParts[i]
			}
		} else {
			// Literal segment — must match exactly.
			if tp != pathParts[i] {
				return nil, false
			}
		}
	}

	return vars, true
}

// splitPath splits a path by its primary delimiter and removes empty segments.
// Supports "/" (gNMI/YANG paths) and ":" (Prometheus-safe encoding of hierarchical
// vendor paths like Ericsson ENM). "/" takes precedence; ":" is the fallback for
// paths that have no "/" but contain ":".
func splitPath(p string) []string {
	delim := "/"
	if !strings.Contains(p, "/") && strings.Contains(p, ":") {
		delim = ":"
	}
	parts := strings.Split(p, delim)
	var result []string
	for _, s := range parts {
		if s != "" {
			result = append(result, s)
		}
	}
	return result
}

// isTemplateSegment returns true if a segment contains Go template syntax.
func isTemplateSegment(s string) bool {
	return strings.Contains(s, "{{")
}

// extractVarNames returns all variable names referenced in a template string.
func extractVarNames(tmpl string) []string {
	var names []string
	for {
		start := strings.Index(tmpl, "{{.")
		if start == -1 {
			break
		}
		tmpl = tmpl[start+3:]
		end := strings.Index(tmpl, "}}")
		if end == -1 {
			break
		}
		name := strings.TrimSpace(tmpl[:end])
		names = append(names, name)
		tmpl = tmpl[end+2:]
	}
	return names
}

// extractSingleVarName extracts the variable name from a single template segment
// like "{{.NF}}" or "{{.Instance}}". Returns "" if the segment contains
// multiple expressions or no recognized pattern.
func extractSingleVarName(seg string) string {
	start := strings.Index(seg, "{{.")
	if start == -1 {
		return ""
	}
	rest := seg[start+3:]
	end := strings.Index(rest, "}}")
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}
