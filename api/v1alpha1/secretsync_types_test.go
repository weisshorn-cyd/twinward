package v1alpha1

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
)

func Test_targetMetadataDirectives(t *testing.T) {
	yamlData := []byte(`
spec:
  source:
    namespace: team-a
    name: source
    uid: source-uid
  target:
    namespace: team-b
    name: target
    labels:
      example.com/add: value
      example.com/remove: ~
    annotations:
      example.com/remove: null
`)
	jsonData, err := yaml.ToJSON(yamlData)
	if err != nil {
		t.Fatalf("convert YAML to JSON: %v", err)
	}

	var secretSync SecretSync
	if err := json.Unmarshal(jsonData, &secretSync); err != nil {
		t.Fatalf("decode SecretSync: %v", err)
	}

	add := secretSync.Spec.Target.Labels["example.com/add"]
	if add == nil || *add != "value" {
		t.Fatalf("add directive = %v, want pointer to %q", add, "value")
	}
	if remove, exists := secretSync.Spec.Target.Labels["example.com/remove"]; !exists || remove != nil {
		t.Fatalf("label removal directive = %v, exists = %t; want nil, true", remove, exists)
	}
	if remove, exists := secretSync.Spec.Target.Annotations["example.com/remove"]; !exists || remove != nil {
		t.Fatalf("annotation removal directive = %v, exists = %t; want nil, true", remove, exists)
	}
}

func Test_secretSyncDeepCopy(t *testing.T) {
	value := "original"
	secretSync := &SecretSync{
		Spec: SecretSyncSpec{
			Target: TargetSecretReference{
				Labels: map[string]*string{"example.com/key": &value},
			},
		},
	}

	copied := secretSync.DeepCopy()
	*copied.Spec.Target.Labels["example.com/key"] = "changed"

	if got := *secretSync.Spec.Target.Labels["example.com/key"]; got != "original" {
		t.Fatalf("original label directive = %q, want %q", got, "original")
	}
}
