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
	syncUID   types.UID = "22222222-2222-2222-2222-222222222222"
	targetUID types.UID = "33333333-3333-3333-3333-333333333333"
)

func Test_reconcile(t *testing.T) {
	t.Run("creates owned target", testReconcileCreatesOwnedTarget)
	t.Run("applies target metadata when creating target", testReconcileAppliesTargetMetadataWhenCreatingTarget)
	t.Run("reports source not found", testReconcileReportsSourceNotFound)
	t.Run("reports source UID mismatch", testReconcileReportsSourceUIDMismatch)
	t.Run("refuses existing target", testReconcileRefusesExistingTarget)
	t.Run("synchronizes target owned by SecretSync", testReconcileSynchronizesTargetOwnedBySecretSync)
	t.Run("updates target metadata without changing content", testReconcileUpdatesTargetMetadataWithoutChangingContent)
	t.Run("rejects recreated target with old owner reference", testReconcileRejectsRecreatedTargetWithOldOwnerReference)
	t.Run("does not adopt forged owner reference", testReconcileDoesNotAdoptForgedOwnerReference)
	t.Run("reports target ownership mismatch", testReconcileReportsTargetOwnershipMismatch)
	t.Run("reports namespace not allowed", testReconcileReportsNamespaceNotAllowed)
}

func testReconcileCreatesOwnedTarget(t *testing.T) {
	reconciler, kubeClient := newTestReconciler(t, testSource(), testSecretSync())

	if _, err := reconciler.Reconcile(context.Background(), syncRequest()); err != nil {
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
	if controller == nil || controller.UID != syncUID {
		t.Fatalf("target controller = %#v, want SecretSync UID %q", controller, syncUID)
	}

	got := getSecretSync(t, kubeClient)
	assertReadyReason(t, got, metav1.ConditionTrue, reasonTargetCreated)
	if got.Status.SourceUID != sourceUID {
		t.Fatalf("status sourceUID = %q, want %q", got.Status.SourceUID, sourceUID)
	}
	if got.Status.LastSyncTime == nil {
		t.Fatal("status lastSyncTime should be set")
	}
}

func testReconcileAppliesTargetMetadataWhenCreatingTarget(t *testing.T) {
	secretSync := testSecretSync()
	secretSync.Spec.Target.Labels = map[string]*string{
		"example.com/environment": ptrTo("production"),
		managedByLabel:            nil,
	}
	secretSync.Spec.Target.Annotations = map[string]*string{
		"example.com/owner": ptrTo("platform"),
		lastSyncHash:        nil,
	}
	reconciler, kubeClient := newTestReconciler(t, testSource(), secretSync)

	if _, err := reconciler.Reconcile(context.Background(), syncRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var target corev1.Secret
	targetKey := types.NamespacedName{Namespace: "team-b", Name: "target"}
	if err := kubeClient.Get(context.Background(), targetKey, &target); err != nil {
		t.Fatalf("Get(target) error = %v", err)
	}
	if got := target.Labels["example.com/environment"]; got != "production" {
		t.Fatalf("environment label = %q, want %q", got, "production")
	}
	if _, exists := target.Labels[managedByLabel]; exists {
		t.Fatalf("managed-by label should have been removed")
	}
	if got := target.Annotations["example.com/owner"]; got != "platform" {
		t.Fatalf("owner annotation = %q, want %q", got, "platform")
	}
	if _, exists := target.Annotations[lastSyncHash]; exists {
		t.Fatalf("last-sync-hash annotation should have been removed")
	}
}

func testReconcileReportsSourceNotFound(t *testing.T) {
	reconciler, kubeClient := newTestReconciler(t, testSecretSync())

	if _, err := reconciler.Reconcile(context.Background(), syncRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertReadyReason(t, getSecretSync(t, kubeClient), metav1.ConditionFalse, reasonSourceNotFound)
}

func testReconcileReportsSourceUIDMismatch(t *testing.T) {
	source := testSource()
	source.UID = "different-source-uid"
	reconciler, kubeClient := newTestReconciler(t, source, testSecretSync())

	if _, err := reconciler.Reconcile(context.Background(), syncRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	got := getSecretSync(t, kubeClient)
	assertReadyReason(t, got, metav1.ConditionFalse, reasonSourceUIDMismatch)
	if got.Status.SourceUID != source.UID {
		t.Fatalf("status sourceUID = %q, want observed UID %q", got.Status.SourceUID, source.UID)
	}
}

func testReconcileRefusesExistingTarget(t *testing.T) {
	target := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "target", Namespace: "team-b", UID: targetUID},
		Data:       map[string][]byte{"password": []byte("do-not-overwrite")},
	}
	reconciler, kubeClient := newTestReconciler(t, testSource(), target, testSecretSync())

	if _, err := reconciler.Reconcile(context.Background(), syncRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var gotTarget corev1.Secret
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(target), &gotTarget); err != nil {
		t.Fatalf("Get(target) error = %v", err)
	}
	if string(gotTarget.Data["password"]) != "do-not-overwrite" {
		t.Fatalf("existing target was modified: %q", gotTarget.Data["password"])
	}
	assertReadyReason(t, getSecretSync(t, kubeClient), metav1.ConditionFalse, reasonTargetAlreadyExists)
}

func testReconcileSynchronizesTargetOwnedBySecretSync(t *testing.T) {
	secretSync := testSecretSync()
	secretSync.Status.TargetUID = targetUID
	target := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "target",
			Namespace: "team-b",
			UID:       targetUID,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: twinwardv1alpha1.GroupVersion.String(),
				Kind:       twinwardv1alpha1.SecretSyncKind,
				Name:       secretSync.Name,
				UID:        secretSync.UID,
				Controller: ptrTo(true),
			}},
		},
		Data: map[string][]byte{"password": []byte("old")},
	}
	reconciler, kubeClient := newTestReconciler(t, testSource(), target, secretSync)

	if _, err := reconciler.Reconcile(context.Background(), syncRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var gotTarget corev1.Secret
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(target), &gotTarget); err != nil {
		t.Fatalf("Get(target) error = %v", err)
	}
	if string(gotTarget.Data["password"]) != "s3cr3t" {
		t.Fatalf("target password = %q, want %q", gotTarget.Data["password"], "s3cr3t")
	}
	assertReadyReason(t, getSecretSync(t, kubeClient), metav1.ConditionTrue, reasonTargetUpdated)
}

func testReconcileUpdatesTargetMetadataWithoutChangingContent(t *testing.T) {
	secretSync := testSecretSync()
	secretSync.Status.TargetUID = targetUID
	secretSync.Spec.Target.Labels = map[string]*string{
		"example.com/environment": ptrTo("production"),
		"example.com/obsolete":    nil,
	}
	secretSync.Spec.Target.Annotations = map[string]*string{
		"example.com/owner":    ptrTo("platform"),
		"example.com/obsolete": nil,
	}
	target := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "target",
			Namespace: "team-b",
			UID:       targetUID,
			Labels: map[string]string{
				"example.com/keep":     "unchanged",
				"example.com/obsolete": "remove-me",
			},
			Annotations: map[string]string{
				"example.com/keep":     "unchanged",
				"example.com/obsolete": "remove-me",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: twinwardv1alpha1.GroupVersion.String(),
				Kind:       twinwardv1alpha1.SecretSyncKind,
				Name:       secretSync.Name,
				UID:        secretSync.UID,
				Controller: ptrTo(true),
			}},
		},
		Immutable: ptrTo(true),
		Type:      corev1.SecretTypeOpaque,
		Data:      map[string][]byte{"password": []byte("s3cr3t")},
	}
	reconciler, kubeClient := newTestReconciler(t, testSource(), target, secretSync)

	if _, err := reconciler.Reconcile(context.Background(), syncRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var gotTarget corev1.Secret
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(target), &gotTarget); err != nil {
		t.Fatalf("Get(target) error = %v", err)
	}
	if got := gotTarget.Labels["example.com/environment"]; got != "production" {
		t.Fatalf("environment label = %q, want %q", got, "production")
	}
	if got := gotTarget.Annotations["example.com/owner"]; got != "platform" {
		t.Fatalf("owner annotation = %q, want %q", got, "platform")
	}
	for kind, values := range map[string]map[string]string{
		"labels":      gotTarget.Labels,
		"annotations": gotTarget.Annotations,
	} {
		t.Run(kind, func(t *testing.T) {
			if _, exists := values["example.com/obsolete"]; exists {
				t.Errorf("%s still contain obsolete key", kind)
			}
			if got := values["example.com/keep"]; got != "unchanged" {
				t.Errorf("%s preserved value = %q, want %q", kind, got, "unchanged")
			}
		})
	}
	assertReadyReason(t, getSecretSync(t, kubeClient), metav1.ConditionTrue, reasonTargetUpdated)
}

func testReconcileRejectsRecreatedTargetWithOldOwnerReference(t *testing.T) {
	secretSync := testSecretSync()
	secretSync.Status.TargetUID = "original-target-uid"
	target := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "target",
			Namespace: "team-b",
			UID:       targetUID,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: twinwardv1alpha1.GroupVersion.String(),
				Kind:       twinwardv1alpha1.SecretSyncKind,
				Name:       secretSync.Name,
				UID:        secretSync.UID,
				Controller: ptrTo(true),
			}},
		},
	}
	reconciler, kubeClient := newTestReconciler(t, testSource(), target, secretSync)

	if _, err := reconciler.Reconcile(context.Background(), syncRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	gotSync := getSecretSync(t, kubeClient)
	assertReadyReason(t, gotSync, metav1.ConditionFalse, reasonTargetUIDMismatch)
	if gotSync.Status.TargetUID != "original-target-uid" {
		t.Fatalf("status targetUID = %q, want original UID to remain pinned", gotSync.Status.TargetUID)
	}
}

func testReconcileDoesNotAdoptForgedOwnerReference(t *testing.T) {
	secretSync := testSecretSync()
	target := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "target",
			Namespace: "team-b",
			UID:       targetUID,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: twinwardv1alpha1.GroupVersion.String(),
				Kind:       twinwardv1alpha1.SecretSyncKind,
				Name:       secretSync.Name,
				UID:        secretSync.UID,
				Controller: ptrTo(true),
			}},
		},
		Data: map[string][]byte{"password": []byte("s3cr3t")},
	}
	reconciler, kubeClient := newTestReconciler(t, testSource(), target, secretSync)

	for range 2 {
		if _, err := reconciler.Reconcile(context.Background(), syncRequest()); err != nil {
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
	gotSync := getSecretSync(t, kubeClient)
	assertReadyReason(t, gotSync, metav1.ConditionFalse, reasonTargetAlreadyExists)
	if gotSync.Status.TargetUID != "" {
		t.Fatalf("status targetUID = %q, want empty for untrusted target", gotSync.Status.TargetUID)
	}
}

func testReconcileReportsTargetOwnershipMismatch(t *testing.T) {
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
	reconciler, kubeClient := newTestReconciler(t, testSource(), target, testSecretSync())

	if _, err := reconciler.Reconcile(context.Background(), syncRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertReadyReason(t, getSecretSync(t, kubeClient), metav1.ConditionFalse, reasonTargetOwnershipMismatch)
}

func testReconcileReportsNamespaceNotAllowed(t *testing.T) {
	policy, err := config.NewNamespacePolicy("team-a")
	if err != nil {
		t.Fatalf("NewNamespacePolicy() error = %v", err)
	}
	reconciler, kubeClient := newTestReconcilerWithPolicy(t, policy, testSource(), testSecretSync())

	if _, err := reconciler.Reconcile(context.Background(), syncRequest()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	assertReadyReason(t, getSecretSync(t, kubeClient), metav1.ConditionFalse, reasonTargetNamespaceNotAllowed)
}

func Test_contentHash(t *testing.T) {
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

func Test_newContentHashSalt(t *testing.T) {
	salt, err := NewContentHashSalt()
	if err != nil {
		t.Fatalf("NewContentHashSalt() error = %v", err)
	}
	if len(salt) != contentHashSaltSize {
		t.Fatalf("len(NewContentHashSalt()) = %d, want %d", len(salt), contentHashSaltSize)
	}
}

func newTestReconciler(t *testing.T, objects ...client.Object) (*SecretSyncReconciler, client.Client) {
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
) (*SecretSyncReconciler, client.Client) {
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
		WithStatusSubresource(&twinwardv1alpha1.SecretSync{}).
		WithObjects(objects...).
		Build()
	return &SecretSyncReconciler{
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

func testSecretSync() *twinwardv1alpha1.SecretSync {
	return &twinwardv1alpha1.SecretSync{
		TypeMeta: metav1.TypeMeta{
			APIVersion: twinwardv1alpha1.GroupVersion.String(),
			Kind:       twinwardv1alpha1.SecretSyncKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-copy",
			UID:  syncUID,
		},
		Spec: twinwardv1alpha1.SecretSyncSpec{
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

func syncRequest() ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-copy"}}
}

func getSecretSync(t *testing.T, kubeClient client.Client) *twinwardv1alpha1.SecretSync {
	t.Helper()
	var got twinwardv1alpha1.SecretSync
	if err := kubeClient.Get(context.Background(), types.NamespacedName{Name: "test-copy"}, &got); err != nil {
		t.Fatalf("Get(SecretSync) error = %v", err)
	}
	return &got
}

func assertReadyReason(
	t *testing.T,
	secretSync *twinwardv1alpha1.SecretSync,
	status metav1.ConditionStatus,
	reason string,
) {
	t.Helper()
	condition := findCondition(secretSync.Status.Conditions, twinwardv1alpha1.ReadyCondition)
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
