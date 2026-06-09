# Twinward

![Twinward gopher duplicating a secret painting](docs/assets/twinward-gopher-painter-logo.png)

Twinward is a Kubernetes operator that continuously copies Secret content
according to centrally managed `SecretCopy` resources.

## SecretCopy

`SecretCopy` is a cluster-scoped resource intended to be managed by the
platform engineering team. It identifies one source Secret by namespace, name,
and immutable Kubernetes UID, and one target by namespace and name:

```yaml
apiVersion: twinward.io/v1alpha1
kind: SecretCopy
metadata:
  name: team-a-db-password-to-team-b
spec:
  source:
    namespace: team-a
    name: db-password
    uid: 11111111-1111-1111-1111-111111111111
  target:
    namespace: team-b
    name: db-password-copy
```

Obtain the source UID with:

```sh
kubectl get secret db-password -n team-a \
  -o jsonpath='{.metadata.uid}'
```

The target must not exist initially. Twinward never adopts a pre-existing
Secret. After creating the target, Twinward sets the `SecretCopy` as its
controller owner and continuously synchronizes it. Deleting the `SecretCopy`
causes Kubernetes garbage collection to delete the owned target.

The source UID prevents a deleted source from being silently replaced by a
different Secret with the same namespace and name. In-place Secret updates keep
the same UID and continue to synchronize normally.

## Copied Fields

The operator copies `type` and `data` from the source Secret into the target
Secret. It preserves other target metadata and adds:

- `app.kubernetes.io/managed-by=twinward`
- `twinward.io/last-sync-source`
- `twinward.io/last-sync-source-uid`
- `twinward.io/last-sync-hash`

Immutable target Secrets are not modified.

The sync hash is keyed with a cryptographically random, per-process salt. The
salt remains in memory and is not written to Kubernetes. Consequently, hash
values are not stable across controller restarts.

## Status

`SecretCopy` exposes a standard `Ready` condition and records the observed
source UID, target UID, generation, and last successful synchronization time.
Common condition reasons include:

| Ready | Reason | Meaning |
|---|---|---|
| `True` | `TargetCreated` | The target was created and populated. |
| `True` | `TargetUpdated` | Existing owned target content was synchronized. |
| `True` | `InSync` | The owned target already matches the source. |
| `False` | `SourceNotFound` | No source exists with the configured name. |
| `False` | `SourceUIDMismatch` | The source name exists but its UID differs. |
| `False` | `TargetAlreadyExists` | A pre-existing target was found and not adopted. |
| `False` | `TargetOwnershipMismatch` | Another resource controls the target. |
| `False` | `TargetUIDMismatch` | The owned target was deleted and replaced unexpectedly. |
| `False` | `TargetImmutable` | The owned target differs but cannot be updated. |

Inspect status with:

```sh
kubectl get secretcopies
kubectl describe secretcopy team-a-db-password-to-team-b
```

## Namespace Allowlist

Set `ALLOWED_NAMESPACES` to a comma-separated list of namespace patterns.
Patterns support exact names and shell-style wildcards:

```sh
ALLOWED_NAMESPACES=default,team-*,prod-?
```

If the variable is empty or unset, no namespaces are allowed. Use `*`
explicitly to allow all namespaces. Reconciliations and targets outside the
allowlist are skipped and counted in metrics.

## Metrics

Controller-runtime exposes Prometheus metrics on `:8080/metrics`. Custom metrics:

- `twinward_secret_sync_attempts_total{result,reason}`
- `twinward_namespace_skips_total{role}`

The base manifest creates a metrics Service. If your cluster uses the Prometheus
Operator, apply `config/servicemonitor.yaml` as well.

## Run Locally

```sh
go test ./...
go run ./cmd/twinward --metrics-bind-address=:8080
```

## Deploy

Edit the image and `ALLOWED_NAMESPACES` in `config/manifests.yaml`, then install
the CRD and controller:

```sh
kubectl apply -f config/crd.yaml
kubectl apply -f config/manifests.yaml
```

See `config/sample-secretcopy.yaml` for an example source and copy policy.

Optional Prometheus Operator integration:

```sh
kubectl apply -f config/servicemonitor.yaml
```
