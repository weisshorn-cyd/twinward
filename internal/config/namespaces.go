package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

const AllowedNamespacesEnv = "ALLOWED_NAMESPACES"

type NamespacePolicy struct {
	patterns []string
}

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

func (p NamespacePolicy) Allows(namespace string) bool {
	for _, pattern := range p.patterns {
		matched, err := filepath.Match(pattern, namespace)
		if err == nil && matched {
			return true
		}
	}
	return false
}

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
