# Twinward

![Twinward gopher duplicating a secret painting](docs/assets/twinward-gopher-painter-logo.png)

Twinward is a Kubernetes operator that continuously copies Secret content
according to centrally managed `SecretSync` resources.

## SecretSync

`SecretSync` is a cluster-scoped resource intended to be managed by the
platform engineering team. It identifies one source Secret by namespace, name,
and immutable Kubernetes UID, and one target by namespace and name:

```yaml
apiVersion: twinward.weisshorn.cyd/v1alpha1
kind: SecretSync
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
    labels:
      app.kubernetes.io/part-of: example-application
      obsolete.example.com/label: ~
    annotations:
      example.com/owner: platform-team
      obsolete.example.com/annotation: ~
```

Obtain the source UID with:

```sh
kubectl get secret db-password -n team-a \
  -o jsonpath='{.metadata.uid}'
```

The target must not exist initially. Twinward never adopts a pre-existing
Secret. After creating the target, Twinward sets the `SecretSync` as its
controller owner and continuously synchronizes it. Deleting the `SecretSync`
causes Kubernetes garbage collection to delete the owned target.

The source UID prevents a deleted source from being silently replaced by a
different Secret with the same namespace and name. In-place Secret updates keep
the same UID and continue to synchronize normally.

## Copied Fields

The operator copies `type` and `data` from the source Secret into the target
Secret. It preserves other target metadata and adds:

- `app.kubernetes.io/managed-by=twinward`
- `twinward.weisshorn.cyd/last-sync-source`
- `twinward.weisshorn.cyd/last-sync-source-uid`
- `twinward.weisshorn.cyd/last-sync-hash`

`spec.target.labels` and `spec.target.annotations` customize target metadata.
A string value adds or replaces a key, while YAML `~` (null) removes it. Keys
not listed in these maps are preserved. These directives remain editable after
creation; the target namespace and name remain immutable. Explicit directives
also take precedence over Twinward's metadata above, so those keys can be
replaced or removed when necessary.

Immutable target Secrets do not have their type or data modified. Their metadata
directives can still be reconciled.

The sync hash is keyed with a cryptographically random, per-process salt. The
salt remains in memory and is not written to Kubernetes. Consequently, hash
values are not stable across controller restarts.

## Status

`SecretSync` exposes a standard `Ready` condition and records the observed
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
kubectl get secretsyncs
kubectl describe secretsync team-a-db-password-to-team-b
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

See `config/sample-secretsync.yaml` for an example source and synchronization
policy.

Optional Prometheus Operator integration:

```sh
kubectl apply -f config/servicemonitor.yaml
```

## Container Releases

Pushing a Git tag publishes a multi-platform image to:

```text
ghcr.io/OWNER/REPOSITORY:GIT_TAG
```

The workflow publishes no `latest` tag and refuses to replace an image tag that
already exists. Git tags must also be valid OCI tags, for example `v1.2.3`.
BuildKit publishes an OCI SBOM and provenance alongside the image.

OCI tags are references and can be changed outside the workflow. For deployment,
use the digest reported in the workflow summary:

```text
ghcr.io/OWNER/REPOSITORY@sha256:...
```

Enable immutable releases in the GitHub repository settings to prevent release
tags from being moved or reused.
