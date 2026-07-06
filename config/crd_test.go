package config_test

import (
	"encoding/json"
	"os"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"

	twinwardv1alpha1 "github.com/weisshorn-cyd/twinward/api/v1alpha1"
)

type schemaProperty struct {
	Properties           map[string]schemaProperty `json:"properties"`
	AdditionalProperties *schemaProperty           `json:"additionalProperties"`
	Nullable             bool                      `json:"nullable"`
	XValidations         []struct {
		Rule string `json:"rule"`
	} `json:"x-kubernetes-validations"`
}

func Test_crd(t *testing.T) {
	yamlData, err := os.ReadFile("crd.yaml")
	if err != nil {
		t.Fatalf("read crd.yaml: %v", err)
	}
	jsonData, err := yaml.ToJSON(yamlData)
	if err != nil {
		t.Fatalf("convert crd.yaml to JSON: %v", err)
	}

	var crd struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			Group string `json:"group"`
			Names struct {
				Kind     string `json:"kind"`
				ListKind string `json:"listKind"`
				Plural   string `json:"plural"`
				Singular string `json:"singular"`
			} `json:"names"`
			Versions []struct {
				Name   string `json:"name"`
				Schema struct {
					OpenAPIV3Schema schemaProperty `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(jsonData, &crd); err != nil {
		t.Fatalf("decode crd.yaml: %v", err)
	}

	assertEqual(t, "spec.group", crd.Spec.Group, twinwardv1alpha1.GroupName)
	assertEqual(t, "metadata.name", crd.Metadata.Name,
		twinwardv1alpha1.SecretSyncResource+"."+twinwardv1alpha1.GroupName)
	assertEqual(t, "spec.names.kind", crd.Spec.Names.Kind, twinwardv1alpha1.SecretSyncKind)
	assertEqual(t, "spec.names.listKind", crd.Spec.Names.ListKind, twinwardv1alpha1.SecretSyncKind+"List")
	assertEqual(t, "spec.names.plural", crd.Spec.Names.Plural, twinwardv1alpha1.SecretSyncResource)
	assertEqual(t, "spec.names.singular", crd.Spec.Names.Singular, "secretsync")
	if len(crd.Spec.Versions) != 1 {
		t.Fatalf("len(spec.versions) = %d, want 1", len(crd.Spec.Versions))
	}
	assertEqual(t, "spec.versions[0].name", crd.Spec.Versions[0].Name, twinwardv1alpha1.Version)

	specSchema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
	if len(specSchema.XValidations) != 1 {
		t.Fatalf("len(spec x-kubernetes-validations) = %d, want 1", len(specSchema.XValidations))
	}
	assertEqual(
		t,
		"target immutability rule",
		specSchema.XValidations[0].Rule,
		"self.target.namespace == oldSelf.target.namespace && self.target.name == oldSelf.target.name",
	)

	targetSchema := specSchema.Properties["target"]
	for _, field := range []string{"labels", "annotations"} {
		t.Run(field, func(t *testing.T) {
			property := targetSchema.Properties[field]
			if property.AdditionalProperties == nil {
				t.Fatalf("target.%s additionalProperties schema is missing", field)
			}
			if !property.AdditionalProperties.Nullable {
				t.Fatalf("target.%s values must be nullable to support YAML ~ removal", field)
			}
		})
	}
}

func assertEqual(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}
