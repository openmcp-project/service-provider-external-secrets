# Refactoring: Generalizing the Manager/Helper Pattern

**Purpose:** This document identifies every coupling point in `pkg/externalsecrets` that prevents the manager/helper pattern from being reused by other service providers, explains _why_ each is a problem, and proposes _how_ to fix it.

---

## Current Architecture

```
pkg/externalsecrets/
  manager.go          ← generic reconcile engine
  managedobject.go    ← generic object wrapper  ← imports v1alpha1 ✗
  managedcluster.go   ← generic cluster wrapper
  orphancleaner.go    ← generic orphan cleanup  ← imports v1alpha1 ✗
                                                 ← uses hardcoded label ✗
  labels.go           ← label helpers           ← hardcoded sp name ✗
  objectutils.go      ← debug utility
  flux.go             ← ESO-specific
  helm.go             ← ESO-specific
  secret.go           ← ESO-specific            ← hardcoded "sp-eso-" prefix ✗
  permissions.go      ← ESO-specific

internal/controller/
  externalsecretsoperator_controller.go         ← ClusterType ↔ ResourceLocation cast ✗
```

The generic parts (`manager`, `managedobject`, `managedcluster`, `orphancleaner`) are conceptually reusable but are blocked by a handful of concrete coupling points described below.

---

## Refactoring Items

### 1. `Status` type is coupled to `v1alpha1.InstancePhase` and `v1alpha1.ResourceLocation`

**File:** `pkg/externalsecrets/managedobject.go:56-60`

**What the code does:**
```go
type Status struct {
    Phase    v1alpha1.InstancePhase    // ← ESO API type
    Message  string
    Location v1alpha1.ResourceLocation // ← ESO API type
}

type StatusFunc func(o client.Object, rl v1alpha1.ResourceLocation) Status

type ManagedObject interface {
    GetStatus(v1alpha1.ResourceLocation) Status // ← ESO API type in interface
    ...
}
```

**Why it's a problem:**
Any other service provider wanting to reuse `ManagedObject` or `StatusFunc` must import the ESO service provider's own API types (`service-provider-external-secrets/api/v1alpha1`). That is an absurd and circular dependency. The generic framework must not know about any specific service provider's domain types.

**How to fix:**
Define `InstancePhase` and `ResourceLocation` as `string`-based types in the generic package itself. Since `v1alpha1.InstancePhase` and `v1alpha1.ResourceLocation` are already `type InstancePhase string` in the ESO API, all existing callsites that assign ESO phase constants (`v1alpha1.Ready`, `v1alpha1.Terminating`, etc.) remain valid — they are all untyped string constants or string-typed values that convert automatically.

```go
// In the generic package (e.g., pkg/servicemanager)
type InstancePhase = string  // type alias, or its own type with constants
type ResourceLocation = string

// Provide generic phase constants identical to what v1alpha1 defines:
const (
    PhasePending     InstancePhase = "Pending"
    PhaseProgressing InstancePhase = "Progressing"
    PhaseReady       InstancePhase = "Ready"
    PhaseFailed      InstancePhase = "Failed"
    PhaseTerminating InstancePhase = "Terminating"
    PhaseUnknown     InstancePhase = "Unknown"
)

type Status struct {
    Phase    InstancePhase
    Message  string
    Location ResourceLocation
}

type StatusFunc func(o client.Object, rl ResourceLocation) Status

type ManagedObject interface {
    GetStatus(ResourceLocation) Status
    ...
}
```

The ESO-specific `v1alpha1.InstancePhase` can remain as-is in the API package; it just is no longer imported by the generic framework. The controller layer translates between them with a trivial string cast if needed.

---

### 2. `SimpleStatus` references `v1alpha1` phase constants

**File:** `pkg/externalsecrets/managedobject.go:33-53`

**What the code does:**
```go
func SimpleStatus(o client.Object, rl v1alpha1.ResourceLocation) Status {
    if !o.GetDeletionTimestamp().IsZero() {
        return Status{Phase: v1alpha1.Terminating, ...}
    }
    if o.GetUID() == "" {
        return Status{Phase: v1alpha1.Pending, ...}
    }
    return Status{Phase: v1alpha1.Ready, ...}
}
```

**Why it's a problem:**
`SimpleStatus` is a utility that should be available to every service provider. Right now importing it drags in `v1alpha1`. It also uses `v1alpha1.ResourceLocation` in its signature.

**How to fix:**
Once `InstancePhase`, `ResourceLocation`, and `Status` are defined in the generic package (see item 1), `SimpleStatus` should use those. The function body only needs `v1alpha1.Terminating` → `PhaseTerminating`, etc. — trivial renaming with no behavioral change.

---

### 3. `orphancleaner.go` imports `v1alpha1` for inline status functions

**File:** `pkg/externalsecrets/orphancleaner.go:108-134`

**What the code does:**
```go
func cleanupPreparedStatus(_ client.Object, rl apiv1alpha1.ResourceLocation) Status {
    return Status{Phase: apiv1alpha1.Terminating, Location: rl, ...}
}
func cleanupErrorStatus(_ client.Object, rl apiv1alpha1.ResourceLocation) Status {
    return Status{Phase: apiv1alpha1.Terminating, Location: rl, ...}
}
```

**Why it's a problem:**
Same as item 1 — `orphancleaner.go` imports the ESO API package solely for phase constants that should belong to the generic framework. This is the tail wagging the dog.

**How to fix:**
Replace `apiv1alpha1.Terminating` with `PhaseTerminating` from the generic package. No logic change needed. These two functions become zero-import from the ESO API.

---

### 4. `LabelManagedByValue` is hardcoded to the ESO service provider name

**File:** `pkg/externalsecrets/labels.go:9`

```go
LabelManagedByValue = "service-provider-external-secrets"
```

**Why it's a problem:**
This is the most dangerous coupling for correctness. The `app.kubernetes.io/managed-by` label is written to **every object** the manager creates, and it is also the **selector used by `OrphanCleaner`** to find objects to delete.

If two service providers share this code unchanged:
- SP-A stamps all its objects with `managed-by: service-provider-external-secrets`
- SP-B stamps all its objects with `managed-by: service-provider-external-secrets`
- SP-A's orphan cleaner lists objects with that label → accidentally finds and deletes SP-B's objects

Even in a copy-paste scenario: a developer creates `service-provider-vault` and copies `pkg/externalsecrets`. If they forget to update `LabelManagedByValue`, their cleaner will silently delete ESO's objects on any cluster running both.

**How to fix:**
Make `NewManager` accept the managed-by value as a required parameter (or as a functional option). The manager then passes it into `SetManagedBy` and into the `OrphanCleaner`:

```go
// Generic package
func NewManager(managedByValue string) Manager

// Each SP defines its own constant:
// In service-provider-external-secrets:
const ManagedByValue = "service-provider-external-secrets"

// In service-provider-vault (hypothetical):
const ManagedByValue = "service-provider-vault"

mgr := servicemanager.NewManager(ManagedByValue)
```

The `LabelManagedBy` key (`app.kubernetes.io/managed-by`) remains a constant — it is a well-known Kubernetes label. Only the _value_ needs to be configurable.

---

### 5. `SetManagedBy` and `ManagedBy()` use the hardcoded constant

**File:** `pkg/externalsecrets/labels.go:13-26`

```go
func SetManagedBy(o client.Object) {
    labels[LabelManagedBy] = LabelManagedByValue // ← hardcoded
}
func ManagedBy() client.ListOption {
    return client.MatchingLabels{LabelManagedBy: LabelManagedByValue} // ← hardcoded
}
```

**Why it's a problem:**
`SetManagedBy` is called inside `manager.go`'s reconcile loop (line 141), and `LabelManagedByValue` (the string `"service-provider-external-secrets"`) is baked in at the call site. Sharing `manager.go` without changing this would stamp every object with the wrong owner.

`ManagedBy()` is used in `orphancleaner.go` to list objects — same problem as item 4.

**How to fix:**
Convert `SetManagedBy` and `ManagedBy()` to functions that accept the value as a parameter, or encapsulate both into a `LabelConfig` struct that is injected into `Manager` at construction:

```go
// Option A: Parameter-based
func SetManagedBy(o client.Object, value string)
func ManagedBySelector(value string) client.ListOption

// Option B: Struct (cleaner for multiple helpers sharing the config)
type LabelConfig struct {
    ManagedByValue string
}
func (l LabelConfig) Set(o client.Object)
func (l LabelConfig) Selector() client.ListOption
```

The manager stores the `LabelConfig` and calls `cfg.Set(obj)` in the reconcile mutator, passing `cfg` to `OrphanCleaner` at construction time.

---

### 6. `OrphanCleaner` has the label hardcoded in its list query

**File:** `pkg/externalsecrets/orphancleaner.go:68`

```go
if err := cl.List(ctx, objList,
    client.InNamespace(c.namespace),
    client.MatchingLabels{LabelManagedBy: LabelManagedByValue}, // ← hardcoded
); err != nil {
```

**Why it's a problem:**
This is a direct consequence of items 4 and 5. The orphan cleaner's correctness depends entirely on using the right managed-by value for the _current_ service provider. With a hardcoded value, the cleaner is silently scoped to ESO's objects — making it dangerous to share.

**How to fix:**
Pass the managed-by value (or a `LabelConfig`) into `NewOrphanCleaner`:

```go
func NewOrphanCleaner[T client.ObjectList](
    cluster ManagedCluster,
    namespace string,
    managedByValue string,   // ← new parameter
    clType cleanerType[T],
) OrphanCleaner
```

The cleaner stores this and uses it in its `List` call.

---

### 7. All generic code lives in the same package as ESO-specific code

**Directory:** `pkg/externalsecrets/`

**Why it's a problem:**
The Go import model means any consumer of the generic framework (`manager.go`, `managedobject.go`, `managedcluster.go`) must import the entire `pkg/externalsecrets` package — which transitively pulls in:
- `github.com/fluxcd/helm-controller/api/v2` (Flux HelmRelease types)
- `github.com/fluxcd/source-controller/api/v1` (Flux OCIRepository types)
- `github.com/fluxcd/pkg/runtime/conditions` (Flux conditions interface)
- `opencontrolplane-runtime/pkg/serviceprovider/clusteraccess`
- ESO's own `api/v1alpha1`

A `service-provider-vault` would have to vendor all of ESO's Flux dependencies just to use the manager framework. This is an import hygiene problem and can cause version conflicts in large dependency trees.

**How to fix:**
Split the package:

```
pkg/
  servicemanager/          ← new: the generic framework
    manager.go
    managedobject.go
    managedcluster.go
    orphancleaner.go
    labels.go
    objectutils.go

  externalsecrets/         ← existing: ESO-specific code only
    flux.go
    helm.go
    secret.go
    permissions.go
```

The `pkg/servicemanager` package would depend only on:
- `sigs.k8s.io/controller-runtime` (client, controllerutil)
- `github.com/openmcp-project/controller-utils/pkg/clusters`

No Flux, no ESO API, no service-provider-specific imports.

---

### 8. `ClusterType` in the generic package implicitly mirrors `v1alpha1.ResourceLocation`

**File:** `pkg/externalsecrets/managedcluster.go:11-18` + `internal/controller/...:210`

**What the code does:**
```go
// In managedcluster.go (generic):
type ClusterType string
const (
    ManagedControlPlane ClusterType = "ManagedControlPlane"
    PlatformCluster     ClusterType = "PlatformCluster"
    WorkloadCluster     ClusterType = "WorkloadCluster"
)

// In controller.go (ESO-specific), line 210:
status := res.Object.GetStatus(apiv1alpha1.ResourceLocation(res.Cluster.GetClusterType()))
```

**Why it's a problem:**
The cast `apiv1alpha1.ResourceLocation(res.Cluster.GetClusterType())` works only because the string values happen to match between `ClusterType` and `v1alpha1.ResourceLocation`. This is an invisible invariant — there is nothing in the type system enforcing it. If someone adds a new `ClusterType` constant (`WorkloadCluster` is already defined but has no `v1alpha1.ResourceLocation` counterpart), the cast silently produces an invalid enum value.

Additionally, if the generic framework uses its own `ResourceLocation`-equivalent type (see item 1), this cast becomes straightforward and explicit.

**How to fix:**
Once `ResourceLocation` is defined in the generic package as a `string` type (item 1), `ClusterType` and `ResourceLocation` can either be:

- **Merged**: `ClusterType` _is_ `ResourceLocation` — they represent the same concept (where does this object live?)
- **Kept separate with explicit conversion**: The controller explicitly maps `ClusterType → ResourceLocation` with a switch or a lookup, making the correspondence visible and compiler-checked

Option A (merge) is simpler and removes the duplication. The generic `ResourceLocation` would have the same constants as today's `ClusterType`.

---

### 9. `PrefixSecretName` hardcodes the `sp-eso-` service-provider prefix

**File:** `pkg/externalsecrets/secret.go` (function `PrefixSecretName`)

**What the code does:**
```go
func PrefixSecretName(secretName string) (string, error) {
    // internally uses "sp-eso-" as the prefix
}
```

**Why it's a problem:**
The prefix `sp-eso-` is specific to the External Secrets service provider naming convention. Another service provider (e.g., Vault) would produce names like `sp-eso-my-vault-secret`, which is confusing and incorrect.

The function has reasonable generic logic (prefix + hash-truncate to 63 chars with collision safety), but the prefix itself is domain-specific.

**How to fix:**
Move the generic truncation/hashing logic to `pkg/servicemanager` with a configurable prefix parameter. Keep the ESO-specific prefix as a constant in `pkg/externalsecrets`:

```go
// In pkg/servicemanager (generic)
func PrefixSecretName(prefix, secretName string) (string, error)

// In pkg/externalsecrets (ESO-specific)
const ChartPullSecretPrefix = "sp-eso-"

func PrefixSecretName(secretName string) (string, error) {
    return servicemanager.PrefixSecretName(ChartPullSecretPrefix, secretName)
}
```

---

### 10. Apply ordering is implicit (registration order) but only deletion is dependency-driven

**File:** `pkg/externalsecrets/manager.go:76-98`

**What the code does:**
```go
func (m *managerImpl) reconcileObjects(ctx context.Context, isDeletion bool) ([]Result, error) {
    dependents := m.getDependents()           // builds reverse dep graph
    for _, mc := range m.clusters {
        for _, mo := range mc.GetObjects() {
            result := m.reconcileObject(ctx, mc, mo, dependents, isDeletion)
            // ...
        }
    }
}
```

**Why it's a problem:**
The dependency graph (`DependsOn` in `ManagedObjectContext`) is **only used for deletion ordering**. During `Apply`, objects are reconciled in the order they were added via `AddObject`. This is a hidden contract that a new service provider might violate, leading to reconcile failures (e.g., creating a `HelmRelease` before its `OCIRepository` is ready, though `CreateOrUpdate` would be idempotent, Flux would just error out on the first cycle).

This is not strictly a _coupling_ problem but it IS a footgun for framework consumers.

**How to fix:**
Two options:
- **Document it**: Explicitly state in the `ManagedObjectContext` or `Manager` godoc that `DependsOn` only affects deletion ordering, and `Apply` ordering is the caller's responsibility.
- **Enforce it for Apply too** (optional, stronger): Sort `GetObjects()` topologically before applying, using the `DependsOn` graph. This makes the framework safe regardless of registration order.

If implementing topological sorting, use Kahn's algorithm on the `DependsOn` DAG. Cycle detection should return an error.

---

### 11. `NewSecretCleaner` is a thin wrapper that is ESO-specific in its scope

**File:** `pkg/externalsecrets/secret.go`

**What the code does:**
```go
func NewSecretCleaner(cluster ManagedCluster, namespace string, secretsToKeep []corev1.LocalObjectReference) OrphanCleaner {
    return NewOrphanCleaner[*corev1.SecretList](cluster, namespace, cleanerType[*corev1.SecretList]{
        ObjectsToKeep: secretsToKeep,
        EmptyList:     func() *corev1.SecretList { return &corev1.SecretList{} },
    })
}
```

**Why it's a problem:**
`NewSecretCleaner` is actually a useful generic helper (not ESO-specific — any SP would copy secrets and need a cleaner). But because it lives in `pkg/externalsecrets` alongside Flux code, it inherits the package-level coupling.

**How to fix:**
Once `pkg/servicemanager` exists, move `NewSecretCleaner` there. It has no ESO-specific logic — it is entirely generic. Only the usage with specific namespaces and prefixed names is ESO-specific (that stays in `internal/controller`).

---

### 12. The `ManagePullSecret` function's secret copy logic belongs in the generic layer

**File:** `pkg/externalsecrets/secret.go`

**What the code does:**
```go
type SecretCopyConfig struct {
    SourceClient    client.Client
    SourceNamespace string
    TargetNamespace string
    TargetName      string
}

func ManagePullSecret(targetCluster ManagedCluster, pullSecret corev1.LocalObjectReference, config SecretCopyConfig)
```

**Why it's a problem:**
The `ManagePullSecret` function implements a cross-cluster/cross-namespace secret copy pattern. This is a common need for any service provider that uses Helm charts from private registries or needs to synchronize credentials. It has zero ESO-specific logic.

**How to fix:**
Move `SecretCopyConfig` and `ManagePullSecret` to `pkg/servicemanager`. The function name could be more generic: `ManageSecretCopy`. Only the _usage_ (which secrets, which namespaces) stays in the ESO controller.

---

## Summary Table

| # | File | Issue | Severity | Fix |
|---|------|-------|----------|-----|
| 1 | `managedobject.go` | `Status` struct uses `v1alpha1.InstancePhase` / `ResourceLocation` | Critical | Define generic types in `pkg/servicemanager` |
| 2 | `managedobject.go` | `SimpleStatus` references `v1alpha1` phase constants | High | Use generic constants from `pkg/servicemanager` |
| 3 | `orphancleaner.go` | Inline status funcs import `apiv1alpha1.Terminating` | High | Use generic `PhaseTerminating` constant |
| 4 | `labels.go` | `LabelManagedByValue` hardcoded to ESO SP name | Critical | Make configurable in `NewManager(value string)` |
| 5 | `labels.go` | `SetManagedBy` / `ManagedBy()` use hardcoded constant | High | Accept value as parameter or via `LabelConfig` struct |
| 6 | `orphancleaner.go` | Label value hardcoded in `List` query | Critical | Pass managed-by value into `NewOrphanCleaner` |
| 7 | `pkg/externalsecrets/` (package) | Generic + ESO-specific code colocated | High | Split into `pkg/servicemanager` + `pkg/externalsecrets` |
| 8 | `managedcluster.go` + controller | `ClusterType` implicitly mirrors `v1alpha1.ResourceLocation` | Medium | Merge types or add explicit conversion |
| 9 | `secret.go` | `PrefixSecretName` hardcodes `"sp-eso-"` | Medium | Accept prefix as parameter; move generic logic to shared pkg |
| 10 | `manager.go` | Apply ordering is implicit; dependency graph only used for deletion | Low | Document clearly; optionally add topological sort for Apply |
| 11 | `secret.go` | `NewSecretCleaner` is generic but lives in ESO-specific package | Low | Move to `pkg/servicemanager` |
| 12 | `secret.go` | `ManagePullSecret` / `SecretCopyConfig` are generic but misplaced | Low | Move to `pkg/servicemanager` |

---

## Proposed Package Structure

```
pkg/
  servicemanager/                    ← NEW: generic, zero ESO imports
    manager.go                       (Manager, OrphanCleaner, Result, AllDeleted)
    managedobject.go                 (ManagedObject, Status, InstancePhase,
                                      ResourceLocation, StatusFunc, SimpleStatus,
                                      ReconcileFunc, NoOp, DeletionPolicy)
    managedcluster.go                (ManagedCluster, ClusterType / ResourceLocation)
    orphancleaner.go                 (NewOrphanCleaner[T], ErrOrphanCleanup)
    labels.go                        (LabelManagedBy constant, SetManagedBy(o, value),
                                      ManagedBySelector(value))
    objectutils.go                   (ObjectID)
    secret.go                        (ManageSecretCopy, SecretCopyConfig,
                                      NewSecretCleaner, PrefixSecretName(prefix, name))

  externalsecrets/                   ← EXISTING: ESO-specific only, imports servicemanager
    flux.go                          (ManageFluxResources, FluxStatus)
    helm.go                          (HelmValues, ExtractHelmValues)
    secret.go                        (PrefixSecretName wrapper, ChartPullSecretPrefix)
    permissions.go                   (ResolveEsoNamespace, TokenAccesGenerator)
    labels.go                        (ManagedByValue = "service-provider-external-secrets")
```

### Dependency graph after refactoring

```
  internal/controller/
    externalsecretsoperator_controller.go
          │
          ├── imports pkg/servicemanager   (Manager, NewManager, ManagedCluster, ...)
          └── imports pkg/externalsecrets  (ManageFluxResources, ExtractHelmValues, ...)

  pkg/externalsecrets/
          │
          └── imports pkg/servicemanager   (ManagedObject, ManagedCluster, ...)
              imports api/v1alpha1         (ExternalSecretsOperator, ProviderConfig)
              imports fluxcd/*             (HelmRelease, OCIRepository)

  pkg/servicemanager/
          │
          └── imports controller-runtime  (client, controllerutil)
              imports controller-utils    (clusters.Cluster)
              NO v1alpha1, NO fluxcd, NO ESO imports
```

---

## Migration Strategy

Migration can be done in stages without breaking the existing operator:

**Stage 1 — Decouple types (no package split yet)**
- Define `InstancePhase`, `ResourceLocation` as string types in the existing `pkg/externalsecrets` package
- Update `Status`, `StatusFunc`, `ManagedObject.GetStatus`, `SimpleStatus` to use the new types
- Replace `apiv1alpha1.Terminating / Pending / Ready / Unknown` in `managedobject.go` and `orphancleaner.go` with the local constants
- The ESO API's `v1alpha1.InstancePhase` remains, but it is no longer imported by the framework layer

**Stage 2 — Make LabelManagedByValue configurable**
- Add managed-by value to `NewManager` signature
- Add it to `NewOrphanCleaner` signature
- Update `SetManagedBy`, `ManagedBy()` to use the passed value
- The single call site in the controller passes the constant (`"service-provider-external-secrets"`)

**Stage 3 — Extract `pkg/servicemanager`**
- Move the decoupled generic files to the new package
- Update `pkg/externalsecrets` imports accordingly
- No behavior change; only package movement

**Stage 4 — Relocate generic helpers**
- Move `NewSecretCleaner`, `ManagePullSecret`/`SecretCopyConfig`, and the generic `PrefixSecretName` to `pkg/servicemanager`
- Keep ESO-specific wrappers in `pkg/externalsecrets` (e.g., `PrefixSecretName` with `"sp-eso-"`)

**Stage 5 — Resolve ClusterType / ResourceLocation duality**
- Consolidate `ClusterType` and `ResourceLocation` in `pkg/servicemanager`
- Remove the implicit string cast in the controller
