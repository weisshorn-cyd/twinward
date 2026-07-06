package config

import "testing"

func Test_namespacePolicy(t *testing.T) {
	t.Run("allows exact and wildcard patterns", testNamespacePolicyAllowsExactAndWildcardPatterns)
	t.Run("defaults to no namespaces", testNamespacePolicyDefaultsToNoNamespaces)
	t.Run("explicit wildcard allows all namespaces", testNamespacePolicyExplicitWildcardAllowsAllNamespaces)
	t.Run("rejects invalid pattern", testNamespacePolicyRejectsInvalidPattern)
}

func testNamespacePolicyAllowsExactAndWildcardPatterns(t *testing.T) {
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
		t.Run(namespace, func(t *testing.T) {
			if got := policy.Allows(namespace); got != want {
				t.Errorf("Allows(%q) = %v, want %v", namespace, got, want)
			}
		})
	}
}

func testNamespacePolicyDefaultsToNoNamespaces(t *testing.T) {
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

func testNamespacePolicyExplicitWildcardAllowsAllNamespaces(t *testing.T) {
	policy, err := NewNamespacePolicy("*")
	if err != nil {
		t.Fatalf("NewNamespacePolicy() error = %v", err)
	}

	if !policy.Allows("anything") {
		t.Fatal("explicit wildcard should allow all namespaces")
	}
}

func testNamespacePolicyRejectsInvalidPattern(t *testing.T) {
	if _, err := NewNamespacePolicy("["); err == nil {
		t.Fatal("expected invalid glob pattern to be rejected")
	}
}
