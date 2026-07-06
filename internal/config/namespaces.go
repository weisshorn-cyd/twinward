// Package config provides runtime configuration for Twinward.
package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// AllowedNamespacesEnv names the environment variable containing allowed namespace patterns.
const AllowedNamespacesEnv = "ALLOWED_NAMESPACES"

// NamespacePolicy determines whether namespaces match configured glob patterns.
type NamespacePolicy struct {
	patterns []string
}

// NewNamespacePolicy parses a comma-separated list of namespace glob patterns.
func NewNamespacePolicy(raw string) (NamespacePolicy, error) {
	patterns := splitCSV(raw)

	for _, pattern := range patterns {
		if strings.Contains(pattern, "/") {
			return NamespacePolicy{}, fmt.Errorf("namespace pattern %q must not contain /", pattern)
		}
		if _, err := filepath.Match(pattern, "example"); err != nil {
			return NamespacePolicy{}, fmt.Errorf("namespace pattern %q is invalid: %w", pattern, err)
		}
	}

	return NamespacePolicy{patterns: patterns}, nil
}

// Allows reports whether a namespace matches at least one configured pattern.
func (p NamespacePolicy) Allows(namespace string) bool {
	for _, pattern := range p.patterns {
		matched, err := filepath.Match(pattern, namespace)
		if err == nil && matched {
			return true
		}
	}
	return false
}

// Patterns returns a copy of the configured namespace glob patterns.
func (p NamespacePolicy) Patterns() []string {
	return append([]string(nil), p.patterns...)
}

func splitCSV(raw string) []string {
	var values []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}
