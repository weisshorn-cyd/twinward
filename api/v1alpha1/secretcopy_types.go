package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const ReadyCondition = "Ready"

// SourceSecretReference identifies the exact source Secret approved for copying.
type SourceSecretReference struct {
	// Namespace is the namespace containing the source Secret.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`
	// Name is the name of the source Secret.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// UID pins the policy to one immutable Kubernetes object identity. If the
	// Secret is deleted and recreated with the same name, its new UID must be
	// explicitly approved by updating this field.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	UID types.UID `json:"uid"`
}

// TargetSecretReference identifies the Secret Twinward must create and manage.
type TargetSecretReference struct {
	// Namespace is the namespace in which Twinward creates the target Secret.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`
	// Name is the name Twinward gives the target Secret. A Secret with this name
	// must not already exist.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// SecretCopySpec defines one centrally approved source-to-target relationship.
// +kubebuilder:validation:XValidation:rule="self.target == oldSelf.target",message="target is immutable; create a new SecretCopy instead"
type SecretCopySpec struct {
	// Source identifies the exact Secret whose type and data are copied.
	Source SourceSecretReference `json:"source"`
	// Target identifies the Secret created and continuously synchronized by
	// Twinward. The target reference is immutable.
	Target TargetSecretReference `json:"target"`
}

// SecretCopyStatus reports the observed source, managed target, and latest
// synchronization result.
type SecretCopyStatus struct {
	// ObservedGeneration is the SecretCopy generation reflected by this status.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// SourceUID is the UID observed on the source name. It can differ from
	// spec.source.uid when the Ready reason is SourceUIDMismatch.
	SourceUID types.UID `json:"sourceUID,omitempty"`
	// TargetUID pins synchronization to the target object created by Twinward.
	// Twinward refuses to adopt a target whose UID is not recorded here.
	TargetUID types.UID `json:"targetUID,omitempty"`
	// LastSyncTime is the time Twinward most recently created or updated the
	// target successfully.
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
	// Conditions contains the current Ready condition and its reason.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// SecretCopy continuously copies one UID-pinned source Secret into one
// controller-owned target Secret. SecretCopy is cluster-scoped and intended to
// be managed by platform engineering.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=scopy
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"
// +kubebuilder:printcolumn:name="Source",type="string",JSONPath=".spec.source.name"
// +kubebuilder:printcolumn:name="Target",type="string",JSONPath=".spec.target.name"
type SecretCopy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SecretCopySpec   `json:"spec"`
	Status SecretCopyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type SecretCopyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SecretCopy `json:"items"`
}
