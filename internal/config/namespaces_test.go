package config

import "testing"

func TestNamespacePolicyAllowsExactAndWildcardPatterns(t *testing.T) {
	policy, err := NewNamespacePolicy("default,team-*,prod-?")
	if err != nil {
		t.Fatalf("NewNamespacePolicy() error = %v", err)
	}

	tests := map[string]bool{
		"default": true,
		"team-a":  true,
		"team-ab": true,
		"prod-a":  true,
		"prod-ab": false,
		"other":   false,
	}

	for namespace, want := range tests {
		if got := policy.Allows(namespace); got != want {
			t.Errorf("Allows(%q) = %v, want %v", namespace, got, want)
		}
	}
}

func TestNamespacePolicyDefaultsToNoNamespaces(t *testing.T) {
	policy, err := NewNamespacePolicy("")
	if err != nil {
		t.Fatalf("NewNamespacePolicy() error = %v", err)
	}

	if policy.Allows("anything") {
		t.Fatal("empty policy should not allow any namespace")
	}
	if len(policy.Patterns()) != 0 {
		t.Fatalf("Patterns() = %v, want no patterns", policy.Patterns())
	}
}

func TestNamespacePolicyExplicitWildcardAllowsAllNamespaces(t *testing.T) {
	policy, err := NewNamespacePolicy("*")
	if err != nil {
		t.Fatalf("NewNamespacePolicy() error = %v", err)
	}

	if !policy.Allows("anything") {
		t.Fatal("explicit wildcard should allow all namespaces")
	}
}

func TestNamespacePolicyRejectsInvalidPattern(t *testing.T) {
	if _, err := NewNamespacePolicy("["); err == nil {
		t.Fatal("expected invalid glob pattern to be rejected")
	}
}
