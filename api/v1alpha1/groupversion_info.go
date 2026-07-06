// Package v1alpha1 contains the Twinward API types.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// GroupName is the Kubernetes API group used by Twinward resources.
	GroupName = "twinward.weisshorn.cyd"
	// Version is the served Twinward API version.
	Version = "v1alpha1"
	// SecretSyncKind is the Kubernetes kind for a SecretSync.
	SecretSyncKind = "SecretSync"
	// SecretSyncResource is the plural Kubernetes resource name for SecretSync.
	SecretSyncResource = "secretsyncs"
)

var (
	// GroupVersion identifies the served Twinward API group and version.
	GroupVersion = schema.GroupVersion{Group: GroupName, Version: Version}
	// SchemeBuilder registers Twinward API types.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	// AddToScheme adds all Twinward API types to a runtime scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion, &SecretSync{}, &SecretSyncList{})
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
