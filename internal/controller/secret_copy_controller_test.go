package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	twinwardv1alpha1 "github.com/weisshorn-cyd/twinward/api/v1alpha1"
	"github.com/weisshorn-cyd/twinward/internal/config"
)

const (
	sourceUID types.UID = "11111111-1111-1111-1111-111111111111"
	copyUID   types.UID = "22222222-2222-2222-2222-222222222222"
	targetUID types.UID = "33333333-3333-3333-3333-333333333333"
)

func TestReconcileCreatesOwnedTarget(t *testing.T) {
	reconciler, kubeClient := newTestReconciler(t, testSource(), testSecretCopy())

	if _, err := reconciler.Reconcile(context.Background(), copyRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var target corev1.Secret
	targetKey := types.NamespacedName{Namespace: "team-b", Name: "target"}
	if err := kubeClient.Get(context.Background(), targetKey, &target); err != nil {
		t.Fatalf("Get(target) error = %v", err)
	}
	if string(target.Data["password"]) != "s3cr3t" {
		t.Fatalf("target password = %q, want %q", target.Data["password"], "s3cr3t")
	}
	if target.Labels[managedByLabel] != managedByValue {
		t.Fatalf("managed-by label = %q, want %q", target.Labels[managedByLabel], managedByValue)
	}
	controller := metav1.GetControllerOf(&target)
	if controller == nil || controller.UID != copyUID {
		t.Fatalf("target controller = %#v, want SecretCopy UID %q", controller, copyUID)
	}

	got := getSecretCopy(t, kubeClient)
	assertReadyReason(t, got, metav1.ConditionTrue, reasonTargetCreated)
	if got.Status.SourceUID != sourceUID {
		t.Fatalf("status sourceUID = %q, want %q", got.Status.SourceUID, sourceUID)
	}
	if got.Status.LastSyncTime == nil {
		t.Fatal("status lastSyncTime should be set")
	}
}

func TestReconcileReportsSourceNotFound(t *testing.T) {
	reconciler, kubeClient := newTestReconciler(t, testSecretCopy())

	if _, err := reconciler.Reconcile(context.Background(), copyRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertReadyReason(t, getSecretCopy(t, kubeClient), metav1.ConditionFalse, reasonSourceNotFound)
}

func TestReconcileReportsSourceUIDMismatch(t *testing.T) {
	source := testSource()
	source.UID = "different-source-uid"
	reconciler, kubeClient := newTestReconciler(t, source, testSecretCopy())

	if _, err := reconciler.Reconcile(context.Background(), copyRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	got := getSecretCopy(t, kubeClient)
	assertReadyReason(t, got, metav1.ConditionFalse, reasonSourceUIDMismatch)
	if got.Status.SourceUID != source.UID {
		t.Fatalf("status sourceUID = %q, want observed UID %q", got.Status.SourceUID, source.UID)
	}
}

func TestReconcileRefusesExistingTarget(t *testing.T) {
	target := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "target", Namespace: "team-b", UID: targetUID},
		Data:       map[string][]byte{"password": []byte("do-not-overwrite")},
	}
	reconciler, kubeClient := newTestReconciler(t, testSource(), target, testSecretCopy())

	if _, err := reconciler.Reconcile(context.Background(), copyRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var gotTarget corev1.Secret
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(target), &gotTarget); err != nil {
		t.Fatalf("Get(target) error = %v", err)
	}
	if string(gotTarget.Data["password"]) != "do-not-overwrite" {
		t.Fatalf("existing target was modified: %q", gotTarget.Data["password"])
	}
	assertReadyReason(t, getSecretCopy(t, kubeClient), metav1.ConditionFalse, reasonTargetAlreadyExists)
}

func TestReconcileSynchronizesTargetOwnedBySecretCopy(t *testing.T) {
	secretCopy := testSecretCopy()
	secretCopy.Status.TargetUID = targetUID
	target := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "target",
			Namespace: "team-b",
			UID:       targetUID,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: twinwardv1alpha1.GroupVersion.String(),
				Kind:       "SecretCopy",
				Name:       secretCopy.Name,
				UID:        secretCopy.UID,
				Controller: ptrTo(true),
			}},
		},
		Data: map[string][]byte{"password": []byte("old")},
	}
	reconciler, kubeClient := newTestReconciler(t, testSource(), target, secretCopy)

	if _, err := reconciler.Reconcile(context.Background(), copyRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var gotTarget corev1.Secret
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(target), &gotTarget); err != nil {
		t.Fatalf("Get(target) error = %v", err)
	}
	if string(gotTarget.Data["password"]) != "s3cr3t" {
		t.Fatalf("target password = %q, want %q", gotTarget.Data["password"], "s3cr3t")
	}
	assertReadyReason(t, getSecretCopy(t, kubeClient), metav1.ConditionTrue, reasonTargetUpdated)
}

func TestReconcileRejectsRecreatedTargetWithOldOwnerReference(t *testing.T) {
	secretCopy := testSecretCopy()
	secretCopy.Status.TargetUID = "original-target-uid"
	target := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "target",
			Namespace: "team-b",
			UID:       targetUID,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: twinwardv1alpha1.GroupVersion.String(),
				Kind:       "SecretCopy",
				Name:       secretCopy.Name,
				UID:        secretCopy.UID,
				Controller: ptrTo(true),
			}},
		},
	}
	reconciler, kubeClient := newTestReconciler(t, testSource(), target, secretCopy)

	if _, err := reconciler.Reconcile(context.Background(), copyRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	gotCopy := getSecretCopy(t, kubeClient)
	assertReadyReason(t, gotCopy, metav1.ConditionFalse, reasonTargetUIDMismatch)
	if gotCopy.Status.TargetUID != "original-target-uid" {
		t.Fatalf("status targetUID = %q, want original UID to remain pinned", gotCopy.Status.TargetUID)
	}
}

func TestReconcileDoesNotAdoptForgedOwnerReference(t *testing.T) {
	secretCopy := testSecretCopy()
	target := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "target",
			Namespace: "team-b",
			UID:       targetUID,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: twinwardv1alpha1.GroupVersion.String(),
				Kind:       "SecretCopy",
				Name:       secretCopy.Name,
				UID:        secretCopy.UID,
				Controller: ptrTo(true),
			}},
		},
		Data: map[string][]byte{"password": []byte("s3cr3t")},
	}
	reconciler, kubeClient := newTestReconciler(t, testSource(), target, secretCopy)

	for range 2 {
		if _, err := reconciler.Reconcile(context.Background(), copyRequest()); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
	}

	var gotTarget corev1.Secret
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(target), &gotTarget); err != nil {
		t.Fatalf("Get(target) error = %v", err)
	}
	if string(gotTarget.Data["password"]) != "s3cr3t" {
		t.Fatalf("forged target was modified: %q", gotTarget.Data["password"])
	}
	gotCopy := getSecretCopy(t, kubeClient)
	assertReadyReason(t, gotCopy, metav1.ConditionFalse, reasonTargetAlreadyExists)
	if gotCopy.Status.TargetUID != "" {
		t.Fatalf("status targetUID = %q, want empty for untrusted target", gotCopy.Status.TargetUID)
	}
}

func TestReconcileReportsTargetOwnershipMismatch(t *testing.T) {
	target := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "target",
			Namespace: "team-b",
			UID:       targetUID,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "someone-else",
				UID:        "different-owner",
				Controller: ptrTo(true),
			}},
		},
	}
	reconciler, kubeClient := newTestReconciler(t, testSource(), target, testSecretCopy())

	if _, err := reconciler.Reconcile(context.Background(), copyRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertReadyReason(t, getSecretCopy(t, kubeClient), metav1.ConditionFalse, reasonTargetOwnershipMismatch)
}

func TestReconcileReportsNamespaceNotAllowed(t *testing.T) {
	policy, err := config.NewNamespacePolicy("team-a")
	if err != nil {
		t.Fatalf("NewNamespacePolicy() error = %v", err)
	}
	reconciler, kubeClient := newTestReconcilerWithPolicy(t, policy, testSource(), testSecretCopy())

	if _, err := reconciler.Reconcile(context.Background(), copyRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertReadyReason(t, getSecretCopy(t, kubeClient), metav1.ConditionFalse, reasonTargetNamespaceNotAllowed)
}

func TestContentHashUsesSalt(t *testing.T) {
	secret := testSource()
	firstSalt := make([]byte, contentHashSaltSize)
	secondSalt := make([]byte, contentHashSaltSize)
	secondSalt[0] = 1

	first := contentHash(secret, firstSalt)
	if second := contentHash(secret, secondSalt); first == second {
		t.Fatal("contentHash() should differ when the salt differs")
	}
	if repeated := contentHash(secret, firstSalt); first != repeated {
		t.Fatal("contentHash() should be stable for the same content and salt")
	}
}

func TestNewContentHashSaltReturnsExpectedLength(t *testing.T) {
	salt, err := NewContentHashSalt()
	if err != nil {
		t.Fatalf("NewContentHashSalt() error = %v", err)
	}
	if len(salt) != contentHashSaltSize {
		t.Fatalf("len(NewContentHashSalt()) = %d, want %d", len(salt), contentHashSaltSize)
	}
}

func newTestReconciler(t *testing.T, objects ...client.Object) (*SecretCopyReconciler, client.Client) {
	t.Helper()
	policy, err := config.NewNamespacePolicy("*")
	if err != nil {
		t.Fatalf("NewNamespacePolicy() error = %v", err)
	}
	return newTestReconcilerWithPolicy(t, policy, objects...)
}

func newTestReconcilerWithPolicy(
	t *testing.T,
	policy config.NamespacePolicy,
	objects ...client.Object,
) (*SecretCopyReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := twinwardv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add Twinward scheme: %v", err)
	}
	kubeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&twinwardv1alpha1.SecretCopy{}).
		WithObjects(objects...).
		Build()
	return &SecretCopyReconciler{
		Client:          kubeClient,
		Scheme:          scheme,
		NamespacePolicy: policy,
		ContentHashSalt: make([]byte, contentHashSaltSize),
	}, kubeClient
}

func testSource() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "source",
			Namespace: "team-a",
			UID:       sourceUID,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"password": []byte("s3cr3t")},
	}
}

func testSecretCopy() *twinwardv1alpha1.SecretCopy {
	return &twinwardv1alpha1.SecretCopy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: twinwardv1alpha1.GroupVersion.String(),
			Kind:       "SecretCopy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-copy",
			UID:  copyUID,
		},
		Spec: twinwardv1alpha1.SecretCopySpec{
			Source: twinwardv1alpha1.SourceSecretReference{
				Namespace: "team-a",
				Name:      "source",
				UID:       sourceUID,
			},
			Target: twinwardv1alpha1.TargetSecretReference{
				Namespace: "team-b",
				Name:      "target",
			},
		},
	}
}

func copyRequest() ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-copy"}}
}

func getSecretCopy(t *testing.T, kubeClient client.Client) *twinwardv1alpha1.SecretCopy {
	t.Helper()
	var got twinwardv1alpha1.SecretCopy
	if err := kubeClient.Get(context.Background(), types.NamespacedName{Name: "test-copy"}, &got); err != nil {
		t.Fatalf("Get(SecretCopy) error = %v", err)
	}
	return &got
}

func assertReadyReason(
	t *testing.T,
	secretCopy *twinwardv1alpha1.SecretCopy,
	status metav1.ConditionStatus,
	reason string,
) {
	t.Helper()
	condition := findCondition(secretCopy.Status.Conditions, twinwardv1alpha1.ReadyCondition)
	if condition == nil {
		t.Fatal("Ready condition was not set")
	}
	if condition.Status != status || condition.Reason != reason {
		t.Fatalf("Ready condition = (%s, %s), want (%s, %s)",
			condition.Status, condition.Reason, status, reason)
	}
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func ptrTo[T any](value T) *T {
	return &value
}
