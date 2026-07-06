// Package controller reconciles Twinward API resources.
package controller

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	twinwardv1alpha1 "github.com/weisshorn-cyd/twinward/api/v1alpha1"
	"github.com/weisshorn-cyd/twinward/internal/config"
	"github.com/weisshorn-cyd/twinward/internal/logging"
)

const (
	contentHashSaltSize = 32
	managedByLabel      = "app.kubernetes.io/managed-by"
	managedByValue      = "twinward"
	lastSyncHash        = twinwardv1alpha1.GroupName + "/last-sync-hash"
	lastSyncSource      = twinwardv1alpha1.GroupName + "/last-sync-source"
	lastSyncSourceUID   = twinwardv1alpha1.GroupName + "/last-sync-source-uid"

	sourceSecretIndex = "spec.sourceSecret"
	targetSecretIndex = "spec.targetSecret"

	reasonInvalidSpec               = "InvalidSpec"
	reasonSourceNamespaceNotAllowed = "SourceNamespaceNotAllowed"
	reasonTargetNamespaceNotAllowed = "TargetNamespaceNotAllowed"
	reasonSourceNotFound            = "SourceNotFound"
	reasonSourceUIDMismatch         = "SourceUIDMismatch"
	reasonSourceGetFailed           = "SourceGetFailed"
	reasonTargetAlreadyExists       = "TargetAlreadyExists"
	reasonTargetOwnershipMismatch   = "TargetOwnershipMismatch"
	reasonTargetUIDMismatch         = "TargetUIDMismatch"
	reasonTargetImmutable           = "TargetImmutable"
	reasonTargetGetFailed           = "TargetGetFailed"
	reasonTargetCreateFailed        = "TargetCreateFailed"
	reasonTargetUpdateFailed        = "TargetUpdateFailed"
	reasonTargetCreated             = "TargetCreated"
	reasonTargetUpdated             = "TargetUpdated"
	reasonInSync                    = "InSync"
)

var (
	syncAttempts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "twinward_secret_sync_attempts_total",
			Help: "Total number of Secret synchronization attempts by result and reason.",
		},
		[]string{"result", "reason"},
	)
	namespaceSkips = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "twinward_namespace_skips_total",
			Help: "Total number of skipped SecretSync reconciliations because a namespace is not allowed.",
		},
		[]string{"role"},
	)
)

func init() {
	metrics.Registry.MustRegister(syncAttempts, namespaceSkips)
}

// SecretSyncReconciler synchronizes one source Secret into an owned target Secret.
type SecretSyncReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	NamespacePolicy config.NamespacePolicy
	Recorder        events.EventRecorder
	ContentHashSalt []byte
}

// NewContentHashSalt returns a cryptographically random, per-process salt.
func NewContentHashSalt() ([]byte, error) {
	salt := make([]byte, contentHashSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate content hash salt: %w", err)
	}
	return salt, nil
}

// Reconcile brings a SecretSync target into its declared state.
func (r *SecretSyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logging.FromContext(ctx).With("secretSync", req.Name)

	var secretSync twinwardv1alpha1.SecretSync
	if err := r.Get(ctx, req.NamespacedName, &secretSync); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	sourceRef := secretSync.Spec.Source
	targetRef := secretSync.Spec.Target
	if sourceRef.Namespace == "" || sourceRef.Name == "" || sourceRef.UID == "" ||
		targetRef.Namespace == "" || targetRef.Name == "" ||
		(sourceRef.Namespace == targetRef.Namespace && sourceRef.Name == targetRef.Name) {
		return ctrl.Result{}, r.setReady(ctx, &secretSync, metav1.ConditionFalse, reasonInvalidSpec,
			"source and target must be distinct and include namespace and name; source UID is required",
			secretSync.Status.SourceUID, secretSync.Status.TargetUID, false)
	}

	if !r.NamespacePolicy.Allows(sourceRef.Namespace) {
		namespaceSkips.WithLabelValues("source").Inc()
		return ctrl.Result{}, r.setReady(ctx, &secretSync, metav1.ConditionFalse, reasonSourceNamespaceNotAllowed,
			fmt.Sprintf("source namespace %q is not allowed", sourceRef.Namespace),
			secretSync.Status.SourceUID, secretSync.Status.TargetUID, false)
	}
	if !r.NamespacePolicy.Allows(targetRef.Namespace) {
		namespaceSkips.WithLabelValues("target").Inc()
		return ctrl.Result{}, r.setReady(ctx, &secretSync, metav1.ConditionFalse, reasonTargetNamespaceNotAllowed,
			fmt.Sprintf("target namespace %q is not allowed", targetRef.Namespace),
			secretSync.Status.SourceUID, secretSync.Status.TargetUID, false)
	}

	sourceKey := types.NamespacedName{Namespace: sourceRef.Namespace, Name: sourceRef.Name}
	var source corev1.Secret
	if err := r.Get(ctx, sourceKey, &source); err != nil {
		if apierrors.IsNotFound(err) {
			syncAttempts.WithLabelValues("blocked", reasonSourceNotFound).Inc()
			return ctrl.Result{}, r.setReady(ctx, &secretSync, metav1.ConditionFalse, reasonSourceNotFound,
				fmt.Sprintf("source Secret %s was not found", sourceKey.String()), "", secretSync.Status.TargetUID, false)
		}
		syncAttempts.WithLabelValues("error", reasonSourceGetFailed).Inc()
		statusErr := r.setReady(ctx, &secretSync, metav1.ConditionFalse, reasonSourceGetFailed,
			fmt.Sprintf("failed to read source Secret %s", sourceKey.String()), "", secretSync.Status.TargetUID, false)
		return ctrl.Result{}, errors.Join(err, statusErr)
	}

	if source.UID != sourceRef.UID {
		syncAttempts.WithLabelValues("blocked", reasonSourceUIDMismatch).Inc()
		return ctrl.Result{}, r.setReady(ctx, &secretSync, metav1.ConditionFalse, reasonSourceUIDMismatch,
			fmt.Sprintf("source Secret %s has UID %q, expected %q", sourceKey.String(), source.UID, sourceRef.UID),
			source.UID, secretSync.Status.TargetUID, false)
	}

	targetKey := types.NamespacedName{Namespace: targetRef.Namespace, Name: targetRef.Name}
	var target corev1.Secret
	if err := r.Get(ctx, targetKey, &target); err != nil {
		if apierrors.IsNotFound(err) {
			return r.createTarget(ctx, log, &secretSync, &source)
		}
		syncAttempts.WithLabelValues("error", reasonTargetGetFailed).Inc()
		statusErr := r.setReady(ctx, &secretSync, metav1.ConditionFalse, reasonTargetGetFailed,
			fmt.Sprintf("failed to read target Secret %s", targetKey.String()),
			source.UID, secretSync.Status.TargetUID, false)
		return ctrl.Result{}, errors.Join(err, statusErr)
	}

	if !metav1.IsControlledBy(&target, &secretSync) {
		reason := reasonTargetAlreadyExists
		message := fmt.Sprintf("target Secret %s already exists and will not be adopted", targetKey.String())
		if controller := metav1.GetControllerOf(&target); controller != nil {
			reason = reasonTargetOwnershipMismatch
			message = fmt.Sprintf("target Secret %s is controlled by %s %q with UID %q",
				targetKey.String(), controller.Kind, controller.Name, controller.UID)
		}
		syncAttempts.WithLabelValues("blocked", reason).Inc()
		return ctrl.Result{}, r.setReady(ctx, &secretSync, metav1.ConditionFalse, reason, message,
			source.UID, secretSync.Status.TargetUID, false)
	}

	if secretSync.Status.TargetUID == "" {
		syncAttempts.WithLabelValues("blocked", reasonTargetAlreadyExists).Inc()
		return ctrl.Result{}, r.setReady(ctx, &secretSync, metav1.ConditionFalse, reasonTargetAlreadyExists,
			fmt.Sprintf("target Secret %s exists with no UID recorded in SecretSync status and will not be adopted",
				targetKey.String()), source.UID, "", false)
	}

	if target.UID != secretSync.Status.TargetUID {
		syncAttempts.WithLabelValues("blocked", reasonTargetUIDMismatch).Inc()
		return ctrl.Result{}, r.setReady(ctx, &secretSync, metav1.ConditionFalse, reasonTargetUIDMismatch,
			fmt.Sprintf("target Secret %s has UID %q, expected previously recorded UID %q",
				targetKey.String(), target.UID, secretSync.Status.TargetUID),
			source.UID, secretSync.Status.TargetUID, false)
	}

	contentInSync := equalSecretContent(&source, &target)
	metadataInSync := metadataDirectivesMatch(&target, secretSync.Spec.Target)
	if contentInSync && metadataInSync {
		syncAttempts.WithLabelValues("success", reasonInSync).Inc()
		return ctrl.Result{}, r.setReady(ctx, &secretSync, metav1.ConditionTrue, reasonInSync,
			fmt.Sprintf("target Secret %s is in sync", targetKey.String()), source.UID, target.UID, false)
	}

	if !contentInSync && target.Immutable != nil && *target.Immutable {
		syncAttempts.WithLabelValues("blocked", reasonTargetImmutable).Inc()
		return ctrl.Result{}, r.setReady(ctx, &secretSync, metav1.ConditionFalse, reasonTargetImmutable,
			fmt.Sprintf("target Secret %s is immutable and differs from the source", targetKey.String()),
			source.UID, target.UID, false)
	}

	updated := target.DeepCopy()
	applySourceContent(updated, &source, secretSync.Spec.Target, r.ContentHashSalt)
	if err := r.Patch(ctx, updated, client.MergeFromWithOptions(&target, client.MergeFromWithOptimisticLock{})); err != nil {
		syncAttempts.WithLabelValues("error", reasonTargetUpdateFailed).Inc()
		statusErr := r.setReady(ctx, &secretSync, metav1.ConditionFalse, reasonTargetUpdateFailed,
			fmt.Sprintf("failed to update target Secret %s", targetKey.String()), source.UID, target.UID, false)
		return ctrl.Result{}, errors.Join(err, statusErr)
	}

	log.InfoContext(ctx, "synchronized target Secret", "source", sourceKey.String(), "target", targetKey.String())
	syncAttempts.WithLabelValues("success", reasonTargetUpdated).Inc()
	if r.Recorder != nil {
		r.Recorder.Eventf(
			updated,
			nil,
			corev1.EventTypeNormal,
			"SecretSynced",
			"Sync",
			"Copied content from %s",
			sourceKey.String(),
		)
	}
	return ctrl.Result{}, r.setReady(ctx, &secretSync, metav1.ConditionTrue, reasonTargetUpdated,
		fmt.Sprintf("synchronized target Secret %s", targetKey.String()), source.UID, updated.UID, true)
}

func (r *SecretSyncReconciler) createTarget(
	ctx context.Context,
	log *slog.Logger,
	secretSync *twinwardv1alpha1.SecretSync,
	source *corev1.Secret,
) (ctrl.Result, error) {
	targetKey := types.NamespacedName{
		Namespace: secretSync.Spec.Target.Namespace,
		Name:      secretSync.Spec.Target.Name,
	}
	target := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetKey.Namespace,
			Name:      targetKey.Name,
		},
	}
	applySourceContent(target, source, secretSync.Spec.Target, r.ContentHashSalt)
	if err := controllerutil.SetControllerReference(
		secretSync,
		target,
		r.Scheme,
		controllerutil.WithBlockOwnerDeletion(false),
	); err != nil {
		statusErr := r.setReady(ctx, secretSync, metav1.ConditionFalse, reasonTargetCreateFailed,
			fmt.Sprintf("failed to configure ownership for target Secret %s", targetKey.String()),
			source.UID, secretSync.Status.TargetUID, false)
		return ctrl.Result{}, errors.Join(err, statusErr)
	}

	if err := r.Create(ctx, target); err != nil {
		if apierrors.IsAlreadyExists(err) {
			syncAttempts.WithLabelValues("blocked", reasonTargetAlreadyExists).Inc()
			return ctrl.Result{}, r.setReady(ctx, secretSync, metav1.ConditionFalse, reasonTargetAlreadyExists,
				fmt.Sprintf("target Secret %s already exists and will not be adopted", targetKey.String()),
				source.UID, secretSync.Status.TargetUID, false)
		}
		syncAttempts.WithLabelValues("error", reasonTargetCreateFailed).Inc()
		statusErr := r.setReady(ctx, secretSync, metav1.ConditionFalse, reasonTargetCreateFailed,
			fmt.Sprintf("failed to create target Secret %s", targetKey.String()),
			source.UID, secretSync.Status.TargetUID, false)
		return ctrl.Result{}, errors.Join(err, statusErr)
	}

	sourceKey := types.NamespacedName{Namespace: source.Namespace, Name: source.Name}
	log.InfoContext(ctx, "created target Secret", "source", sourceKey.String(), "target", targetKey.String())
	syncAttempts.WithLabelValues("success", reasonTargetCreated).Inc()
	if r.Recorder != nil {
		r.Recorder.Eventf(
			target,
			nil,
			corev1.EventTypeNormal,
			"SecretCopied",
			"Create",
			"Copied content from %s",
			sourceKey.String(),
		)
	}
	return ctrl.Result{}, r.setReady(ctx, secretSync, metav1.ConditionTrue, reasonTargetCreated,
		fmt.Sprintf("created target Secret %s", targetKey.String()), source.UID, target.UID, true)
}

func (r *SecretSyncReconciler) setReady(
	ctx context.Context,
	secretSync *twinwardv1alpha1.SecretSync,
	status metav1.ConditionStatus,
	reason string,
	message string,
	sourceUID types.UID,
	targetUID types.UID,
	markSynced bool,
) error {
	before := secretSync.DeepCopy()
	secretSync.Status.ObservedGeneration = secretSync.Generation
	secretSync.Status.SourceUID = sourceUID
	secretSync.Status.TargetUID = targetUID
	if markSynced {
		now := metav1.Now()
		secretSync.Status.LastSyncTime = &now
	}
	meta.SetStatusCondition(&secretSync.Status.Conditions, metav1.Condition{
		Type:               twinwardv1alpha1.ReadyCondition,
		Status:             status,
		ObservedGeneration: secretSync.Generation,
		Reason:             reason,
		Message:            message,
	})
	if equality.Semantic.DeepEqual(before.Status, secretSync.Status) {
		return nil
	}
	return r.Status().Patch(ctx, secretSync, client.MergeFrom(before))
}

// SetupWithManager registers the SecretSync controller and Secret watches.
func (r *SecretSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if len(r.ContentHashSalt) != contentHashSaltSize {
		return fmt.Errorf("content hash salt must be %d bytes", contentHashSaltSize)
	}

	ctx := context.Background()
	if err := mgr.GetFieldIndexer().IndexField(ctx, &twinwardv1alpha1.SecretSync{}, sourceSecretIndex, func(object client.Object) []string {
		secretSync := object.(*twinwardv1alpha1.SecretSync)
		return []string{secretRefKey(secretSync.Spec.Source.Namespace, secretSync.Spec.Source.Name)}
	}); err != nil {
		return fmt.Errorf("index SecretSync sources: %w", err)
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &twinwardv1alpha1.SecretSync{}, targetSecretIndex, func(object client.Object) []string {
		secretSync := object.(*twinwardv1alpha1.SecretSync)
		return []string{secretRefKey(secretSync.Spec.Target.Namespace, secretSync.Spec.Target.Name)}
	}); err != nil {
		return fmt.Errorf("index SecretSync targets: %w", err)
	}

	secretMetadata := &metav1.PartialObjectMetadata{}
	secretMetadata.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Secret"))
	return ctrl.NewControllerManagedBy(mgr).
		For(&twinwardv1alpha1.SecretSync{}).
		WatchesMetadata(secretMetadata, handler.EnqueueRequestsFromMapFunc(r.secretSyncsForSecret)).
		Complete(r)
}

func (r *SecretSyncReconciler) secretSyncsForSecret(ctx context.Context, object client.Object) []reconcile.Request {
	key := secretRefKey(object.GetNamespace(), object.GetName())
	requests := make([]reconcile.Request, 0, 2)
	seen := make(map[string]struct{})
	for _, index := range []string{sourceSecretIndex, targetSecretIndex} {
		var syncs twinwardv1alpha1.SecretSyncList
		if err := r.List(ctx, &syncs, client.MatchingFields{index: key}); err != nil {
			logging.FromContext(ctx).ErrorContext(ctx, "unable to map Secret to SecretSync resources",
				"error", err, "secret", client.ObjectKeyFromObject(object).String(), "index", index)
			continue
		}
		for i := range syncs.Items {
			name := syncs.Items[i].Name
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
		}
	}
	return requests
}

func secretRefKey(namespace, name string) string {
	return namespace + "/" + name
}

func applySourceContent(
	target, source *corev1.Secret,
	targetRef twinwardv1alpha1.TargetSecretReference,
	salt []byte,
) {
	target.Type = source.Type
	target.Data = cloneData(source.Data)
	target.StringData = nil
	target.Labels = ensureMap(target.Labels)
	target.Annotations = ensureMap(target.Annotations)
	target.Labels[managedByLabel] = managedByValue
	target.Annotations[lastSyncSource] = types.NamespacedName{Namespace: source.Namespace, Name: source.Name}.String()
	target.Annotations[lastSyncSourceUID] = string(source.UID)
	target.Annotations[lastSyncHash] = contentHash(source, salt)
	applyMetadataDirectives(target.Labels, targetRef.Labels)
	applyMetadataDirectives(target.Annotations, targetRef.Annotations)
}

func equalSecretContent(source, target *corev1.Secret) bool {
	return source.Type == target.Type && mapsEqual(source.Data, target.Data)
}

func metadataDirectivesMatch(target *corev1.Secret, targetRef twinwardv1alpha1.TargetSecretReference) bool {
	return metadataMapMatches(target.Labels, targetRef.Labels) &&
		metadataMapMatches(target.Annotations, targetRef.Annotations)
}

func metadataMapMatches(actual map[string]string, directives map[string]*string) bool {
	for key, desired := range directives {
		actualValue, exists := actual[key]
		if desired == nil {
			if exists {
				return false
			}
			continue
		}
		if !exists || actualValue != *desired {
			return false
		}
	}
	return true
}

func applyMetadataDirectives(target map[string]string, directives map[string]*string) {
	for key, value := range directives {
		if value == nil {
			delete(target, key)
			continue
		}
		target[key] = *value
	}
}

func mapsEqual(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for key, leftValue := range left {
		rightValue, ok := right[key]
		if !ok || !bytes.Equal(leftValue, rightValue) {
			return false
		}
	}
	return true
}

func cloneData(data map[string][]byte) map[string][]byte {
	if data == nil {
		return nil
	}
	clone := make(map[string][]byte, len(data))
	for key, value := range data {
		clone[key] = append([]byte(nil), value...)
	}
	return clone
}

func ensureMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

func contentHash(secret *corev1.Secret, salt []byte) string {
	hash := hmac.New(sha256.New, salt)
	hash.Write([]byte(secret.Type))
	for _, key := range sortedKeys(secret.Data) {
		hash.Write([]byte{0})
		hash.Write([]byte(key))
		hash.Write([]byte{0})
		hash.Write(secret.Data[key])
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func sortedKeys(data map[string][]byte) []string {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
