# AGENTS.md

This document helps AI agents work effectively in the **customrouter** codebase.

---

## Project Overview

**customrouter** is a Kubernetes operator built with [Kubebuilder](https://book.kubebuilder.io/) (v4.10.1) that provides dynamic HTTP routing for Kubernetes with Envoy and Istio. It consists of two main components:

1. **Operator**: Watches `CustomHTTPRoute` and `ExternalProcessorAttachment` CRDs, compiles routing rules into optimized ConfigMaps and generates EnvoyFilters
2. **External Processor** (extproc): An Envoy ext_proc gRPC service that receives requests from gateways and routes them based on compiled rules

### Key Specifications

| Property | Value |
|----------|-------|
| Domain | `customrouter.freepik.com` |
| API Group | `customrouter.freepik.com/v1alpha1` |
| Go Version | 1.24.6 |
| Controller Runtime | v0.22.4 |
| Test Framework | Ginkgo/Gomega |
| Linter | golangci-lint v2.5.0 |

### Custom Resources

| CRD | Scope | Description |
|-----|-------|-------------|
| `CustomHTTPRoute` | Namespaced | Defines routing rules for hostnames |
| `ExternalProcessorAttachment` | Namespaced | Attaches extproc to Istio gateway pods via EnvoyFilter |

---

## Essential Commands

### Build & Run

| Command | Description |
|---------|-------------|
| `make build` | Build operator binary to `bin/manager` |
| `make build-extproc` | Build external processor binary to `bin/extproc` |
| `make build-all` | Build both binaries |
| `make run` | Run the operator locally (requires kubeconfig) |
| `make docker-build IMG=<image>` | Build operator Docker image |
| `make docker-build-extproc EXTPROC_IMG=<image>` | Build extproc Docker image |
| `make docker-build-all` | Build all Docker images |

### Testing

| Command | Description |
|---------|-------------|
| `make test` | Run unit tests (excludes e2e) with coverage |
| `make test-e2e` | Run e2e tests using Kind cluster (creates/deletes cluster) |
| `make setup-test-e2e` | Create Kind cluster for e2e tests |
| `make cleanup-test-e2e` | Delete the Kind e2e test cluster |

**Run specific tests:**
```bash
# Unit tests for route expansion
go test ./pkg/routes/... -v

# All unit tests with verbose output
go test $(go list ./... | grep -v /e2e) -v
```

### Code Quality

| Command | Description |
|---------|-------------|
| `make lint` | Run golangci-lint |
| `make lint-fix` | Run golangci-lint with auto-fix |
| `make lint-config` | Verify golangci-lint configuration |
| `make fmt` | Run go fmt |
| `make vet` | Run go vet |

### Code Generation

| Command | Description |
|---------|-------------|
| `make generate` | Generate DeepCopy methods |
| `make manifests` | Generate CRD, RBAC, webhook manifests |

**Important**: After modifying `api/v1alpha1/*_types.go`, always run:
```bash
make generate manifests
```

### Deployment

| Command | Description |
|---------|-------------|
| `make install` | Install CRDs to cluster |
| `make uninstall` | Uninstall CRDs from cluster |
| `make deploy IMG=<image>` | Deploy controller to cluster |
| `make undeploy` | Remove controller from cluster |
| `make build-installer` | Generate consolidated install YAML |

---

## Code Organization

```
.
├── api/v1alpha1/                           # API type definitions
│   ├── customhttproute_types.go            # CustomHTTPRoute spec/status
│   ├── externalprocessorattachment_types.go # ExternalProcessorAttachment spec/status
│   ├── groupversion_info.go                # GroupVersion registration
│   └── zz_generated.deepcopy.go            # Generated (DO NOT EDIT)
│
├── cmd/
│   ├── main.go                             # Operator entrypoint
│   └── extproc/main.go                     # External processor entrypoint
│
├── internal/
│   ├── controller/                         # Controller logic
│   │   ├── commons.go                      # Shared constants, UpdateWithRetry helper
│   │   ├── conditions.go                   # Condition helpers
│   │   ├── customhttproute/
│   │   │   ├── controller.go               # Main reconciliation loop
│   │   │   ├── status.go                   # Status condition updaters
│   │   │   └── sync.go                     # ConfigMap generation & partitioning
│   │   └── externalprocessorattachment/
│   │       ├── controller.go               # Main reconciliation loop
│   │       ├── status.go                   # Status condition updaters
│   │       └── sync.go                     # EnvoyFilter generation
│   └── extproc/                            # External processor implementation
│       ├── config.go                       # Server configuration
│       ├── processor.go                    # gRPC processor service
│       ├── router.go                       # Request header processing
│       └── server.go                       # gRPC server setup
│
├── pkg/routes/                             # Shared routes package
│   ├── types.go                            # Route, RoutesConfig types
│   ├── expand.go                           # Route expansion logic
│   ├── expand_test.go                      # Route expansion tests
│   ├── loader.go                           # Route loading from ConfigMaps
│   └── k8s_loader.go                       # Kubernetes ConfigMap watcher
│
├── config/
│   ├── operator/deploy/                    # Operator deployment manifests
│   │   ├── crd/bases/                      # Generated CRD YAML
│   │   ├── rbac/                           # RBAC manifests
│   │   ├── manager/                        # Manager deployment
│   │   └── default/                        # Kustomize overlays
│   └── external-processor/                 # Extproc deployment samples
│       ├── deploy/                         # Kubernetes manifests
│       └── samples/                        # Envoy/Istio config examples
│
├── chart/                                  # Helm chart
│   ├── Chart.yaml
│   ├── values.yaml
│   ├── crds/                               # CRD files for Helm
│   └── templates/
│
├── test/
│   ├── e2e/                                # End-to-end tests (Ginkgo)
│   └── utils/                              # Test utilities
│
└── .agents/                                # Agent documentation
    ├── AGENTS.md                           # This file
    ├── ROUTING_FORMAT_PROPOSAL.md          # ConfigMap format spec
    └── CRD_EXTENSIBILITY_PROPOSAL.md       # CRD design decisions
```

---

## CRD Structure

### CustomHTTPRoute

```yaml
apiVersion: customrouter.freepik.com/v1alpha1
kind: CustomHTTPRoute
metadata:
  name: my-routes
  namespace: default
spec:
  # Required: identifies which external processor handles these routes
  targetRef:
    name: default  # Must match --target-name flag on extproc

  # Required: hostnames this route applies to
  hostnames:
    - www.example.com
    - example.com

  # Optional: path prefixes (e.g., for i18n)
  pathPrefixes:
    values: [es, fr, it]
    policy: Optional  # Optional | Required | Disabled

  # Required: routing rules
  rules:
    - matches:
        - path: /api
          type: PathPrefix   # PathPrefix (default) | Exact | Regex
          priority: 1000     # Higher = evaluated first (default: 1000)
      backendRefs:
        - name: api-service
          namespace: backend
          port: 8080
      pathPrefixes:          # Optional: override spec-level policy
        policy: Disabled
```

### ExternalProcessorAttachment

```yaml
apiVersion: customrouter.freepik.com/v1alpha1
kind: ExternalProcessorAttachment
metadata:
  name: production-gateway
  namespace: istio-system
spec:
  # Required: select gateway pods by labels
  gatewayRef:
    selector:
      istio: gateway-production

  # Required: external processor service reference
  externalProcessorRef:
    service:
      name: customrouter-extproc
      namespace: customrouter
      port: 9001
    timeout: 5s          # gRPC connection timeout (default: "5s")
    messageTimeout: 5s   # Message exchange timeout (default: "5s")

  # Optional: generate catch-all routes for hostnames without HTTPRoute
  catchAllRoute:
    hostnames:
      - example.com
      - www.example.com
    backendRef:
      name: default-backend
      namespace: web
      port: 80
```

#### Catch-All Routes

By default, CustomHTTPRoute requires a base HTTPRoute to be configured at the Istio Gateway level. Without it, Envoy rejects requests with 404 before the ext_proc filter can process them.

The `catchAllRoute` field solves this by generating an additional EnvoyFilter that creates virtual hosts for the specified hostnames. When configured, the operator generates three EnvoyFilters:

1. `<name>-extproc`: Inserts the ext_proc filter into the HTTP filter chain
2. `<name>-routes`: Adds dynamic routing based on `x-customrouter-cluster` header
3. `<name>-catchall`: Creates catch-all virtual hosts for specified hostnames

---

## Key Types

### PathPrefixPolicy

```go
type PathPrefixPolicy string

const (
    PathPrefixPolicyOptional PathPrefixPolicy = "Optional"  // Routes with and without prefix
    PathPrefixPolicyRequired PathPrefixPolicy = "Required"  // Routes only with prefix
    PathPrefixPolicyDisabled PathPrefixPolicy = "Disabled"  // Routes without any prefix
)
```

### MatchType

```go
type MatchType string

const (
    MatchTypePathPrefix MatchType = "PathPrefix"  // Default - prefix matching
    MatchTypeExact      MatchType = "Exact"       // Exact path match
    MatchTypeRegex      MatchType = "Regex"       // Go regexp syntax
)
```

### Route (pkg/routes/types.go)

```go
type Route struct {
    Path     string `json:"path"`
    Type     string `json:"type"`     // "exact", "prefix", "regex"
    Backend  string `json:"backend"`  // "service.namespace.svc.cluster.local:port"
    Priority int32  `json:"priority"`
}

type RoutesConfig struct {
    Version int                `json:"version"`
    Hosts   map[string][]Route `json:"hosts"`
}
```

---

## Route Expansion Logic

Located in `pkg/routes/expand.go`:

### PathPrefix Expansion

| Policy | Prefixes | Input Path | Output Routes |
|--------|----------|------------|---------------|
| Optional | [es, fr] | /api | /es/api, /fr/api, /api |
| Required | [es, fr] | /api | /es/api, /fr/api |
| Disabled | [es, fr] | /api | /api |

### Regex Expansion

Regex patterns are modified to include optional/required prefix matching:

```
Input:  ^/users/[0-9]+$
Output (Optional): ^(?:/(es|fr|it))?/users/[0-9]+$
Output (Required): ^/(es|fr|it)/users/[0-9]+$
Output (Disabled): ^/users/[0-9]+$ (unchanged)
```

### Exact Match

Exact matches are never expanded - they match the literal path only.

### Sorting Order

Routes are sorted by:
1. **Priority DESC** (higher values first)
2. **Type**: exact > regex > prefix
3. **Path length DESC** (longer paths first)

---

## ConfigMap Generation

Located in `internal/controller/customhttproute/sync.go`:

### Naming Convention

ConfigMaps are named: `customrouter-routes-<target>-<index>`

Example: `customrouter-routes-default-0`, `customrouter-routes-default-1`

### Labels

```yaml
labels:
  app.kubernetes.io/name: customrouter
  app.kubernetes.io/managed-by: customrouter-controller
  customrouter.freepik.com/target: <target-name>
  customrouter.freepik.com/part: <index>
```

### Automatic Partitioning

- ConfigMaps are partitioned when data exceeds 900KB
- Routes are split by hostname first
- If a single hostname exceeds the limit, its routes are split across partitions

### ConfigMap Data Format

```json
{
  "version": 1,
  "hosts": {
    "www.example.com": [
      {"path": "/health", "type": "exact", "backend": "health.svc:8080", "priority": 2000},
      {"path": "/api", "type": "prefix", "backend": "api.svc:8080", "priority": 1000}
    ]
  }
}
```

---

## External Processor

### CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `:9001` | gRPC listen address |
| `--target-name` | `default` | Target name to filter ConfigMaps |
| `--debug` | `false` | Enable debug logging |
| `--access-log` | `true` | Enable access logging |
| `--kubeconfig` | `` | Path to kubeconfig (uses in-cluster if not set) |
| `--grpc-max-recv-msg-size` | 4MB | Max receive message size |
| `--grpc-max-send-msg-size` | 4MB | Max send message size |
| `--grpc-max-concurrent-streams` | 1000 | Max concurrent streams per connection |
| `--grpc-keepalive-time` | 30s | Keepalive ping interval |
| `--grpc-keepalive-timeout` | 10s | Keepalive timeout |
| `--grpc-max-connection-idle` | 5m | Max idle connection time |
| `--grpc-max-connection-age` | 30m | Max connection age |
| `--grpc-max-connection-age-grace` | 10s | Grace period after max age |

### Headers Set by Extproc

When a route matches, extproc sets these headers:

| Header | Description |
|--------|-------------|
| `x-customrouter-cluster` | Istio cluster name for routing |
| `:authority` | New authority (backend host:port) |
| `Host` | New host header |
| `x-original-authority` | Original authority for debugging |
| `x-customrouter-matched-path` | Pattern that matched |
| `x-customrouter-matched-type` | Match type (exact/prefix/regex) |

---

## Operator Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--metrics-bind-address` | `0` | Metrics endpoint address |
| `--health-probe-bind-address` | `:8081` | Health probe address |
| `--leader-elect` | `false` | Enable leader election |
| `--metrics-secure` | `true` | Serve metrics via HTTPS |
| `--routes-configmap-namespace` | `default` | Namespace for route ConfigMaps |

---

## Testing

### Test Framework

- **Unit Tests**: Standard Go testing + testify assertions
- **E2E Tests**: Ginkgo/Gomega with Kind cluster

### Running Tests

```bash
# All unit tests
make test

# Specific package
go test ./pkg/routes/... -v

# E2E tests (creates/cleans up Kind cluster)
make test-e2e

# E2E with existing cluster
KIND_CLUSTER=my-cluster go test -tags=e2e ./test/e2e/ -v -ginkgo.v
```

### Test File Locations

| Path | Description |
|------|-------------|
| `pkg/routes/expand_test.go` | Route expansion unit tests |
| `test/e2e/e2e_test.go` | End-to-end integration tests |
| `test/utils/utils.go` | Test helper utilities |

### E2E Test Requirements

- Kind installed and available in PATH
- Docker running
- `kubectl` configured

---

## Code Patterns

### Controller Update Helpers

Use `UpdateWithRetry` and `UpdateStatusWithRetry` for safe concurrent updates:

```go
// internal/controller/commons.go
err = controller.UpdateWithRetry(ctx, r.Client, object, func(obj client.Object) error {
    // Apply mutations to obj
    return nil
})

err = controller.UpdateStatusWithRetry(ctx, r.Client, object, func(obj client.Object) error {
    route := obj.(*v1alpha1.CustomHTTPRoute)
    route.Status.Conditions = conditions
    return nil
})
```

### Finalizer Pattern

Resources use finalizers for cleanup:

```go
const ResourceFinalizer = "customrouter.freepik.com/finalizer"

// Check and remove finalizer on deletion
if !object.DeletionTimestamp.IsZero() {
    if controllerutil.ContainsFinalizer(object, controller.ResourceFinalizer) {
        // Cleanup logic here
        controllerutil.RemoveFinalizer(object, controller.ResourceFinalizer)
    }
}
```

### Status Conditions

CustomHTTPRoute uses these condition types:

| Condition | Description |
|-----------|-------------|
| `Reconciled` | Whether the manifest was processed |
| `ConfigMapSynced` | Whether the ConfigMap was successfully generated |

---

## Linting Configuration

Located in `.golangci.yml`. Key enabled linters:

- `errcheck`, `govet`, `staticcheck` - Core error checking
- `gocyclo` - Cyclomatic complexity
- `dupl` - Duplicate code detection
- `ginkgolinter` - Ginkgo test conventions
- `revive` - General Go style
- `lll` - Line length (disabled for api/* and internal/*)

---

## Helm Chart

### Installation

```bash
helm install customrouter oci://ghcr.io/freepik-company/customrouter/helm-chart/customrouter \
  --namespace customrouter \
  --create-namespace
```

### Key Values

```yaml
operator:
  enabled: true
  replicaCount: 1
  args:
    - --leader-elect
    - --health-probe-bind-address=:8081
  pdb:
    enabled: false
  hpa:
    enabled: false

externalProcessors:
  default:
    enabled: true
    replicaCount: 1
    args:
      - --addr=:9001
      - --target-name=default
      - --access-log=true
    service:
      type: ClusterIP
      port: 9001
    pdb:
      enabled: false
    hpa:
      enabled: false

crds:
  install: true

extraObjects: []  # Deploy additional resources
```

### Multiple External Processors

You can define multiple processors for different targets:

```yaml
externalProcessors:
  default:
    enabled: true
    args:
      - --target-name=default
  internal:
    enabled: true
    args:
      - --target-name=internal
```

---

## CI/CD Workflows

Located in `.github/workflows/`:

| Workflow | Trigger | Description |
|----------|---------|-------------|
| `lint.yml` | push, PR | Run golangci-lint |
| `test.yml` | push, PR | Run unit tests |
| `test-e2e.yml` | push, PR | Run e2e tests with Kind |
| `release.yml` | tag | Build and push Docker images |
| `release-chart.yml` | tag | Publish Helm chart |

---

## Quick Reference

### After Modifying API Types

```bash
make generate manifests
```

### Before Committing

```bash
make fmt vet lint
make test
```

### Run Operator Locally

```bash
make install  # Install CRDs
make run -- --routes-configmap-namespace=default
```

### Run External Processor Locally

```bash
go run ./cmd/extproc/main.go --target-name=default --kubeconfig=$HOME/.kube/config
```

### Deploy to Cluster

```bash
make install
make deploy IMG=myregistry/operator:tag
kubectl apply -f config/operator/samples/
```

---

## Constants Reference

```go
// Default priority for routes (api/v1alpha1/customhttproute_types.go)
const DefaultPriority int32 = 1000

// Max ConfigMap size before partitioning (internal/controller/customhttproute/sync.go)
const maxConfigMapSize = 900 * 1024  // 900KB

// Finalizer (internal/controller/commons.go)
const ResourceFinalizer = "customrouter.freepik.com/finalizer"

// ConfigMap labels (internal/controller/customhttproute/sync.go)
const configMapManagedByLabel = "app.kubernetes.io/managed-by"
const configMapManagedByValue = "customrouter-controller"
const configMapTargetLabel = "customrouter.freepik.com/target"
const configMapPartLabel = "customrouter.freepik.com/part"
```

---

## Gotchas and Non-Obvious Patterns

1. **Route Aggregation by Target**: Routes from multiple `CustomHTTPRoute` resources with the same `targetRef.name` are merged into the same ConfigMaps.

2. **ConfigMap Namespace**: The operator stores ConfigMaps in the namespace specified by `--routes-configmap-namespace` (default: `default`), not the CR's namespace.

3. **Extproc Target Filtering**: Each extproc instance only loads ConfigMaps matching its `--target-name` flag via label selector.

4. **Regex Path Requirement**: Regex patterns must start with `/` to be eligible for prefix expansion. Patterns not starting with `/` are left unchanged.

5. **Rule-level Override**: `rule.pathPrefixes.policy` overrides `spec.pathPrefixes.policy` for that specific rule only.

6. **E2E Cluster Cleanup**: The `make test-e2e` target automatically deletes the Kind cluster after tests complete.

7. **EnvoyFilter Generation**: `ExternalProcessorAttachment` creates EnvoyFilters in the same namespace as the attachment resource, not the gateway's namespace.

8. **Backend Format**: Backends are always formatted as `service.namespace.svc.cluster.local:port` for Kubernetes DNS resolution.

9. **Catch-All Route Requirement**: Without `catchAllRoute` or a base HTTPRoute, requests to hostnames handled only by CustomHTTPRoute will receive 404 from Envoy before reaching the ext_proc filter.

---

## Additional Documentation

- [ROUTING_FORMAT_PROPOSAL.md](.agents/ROUTING_FORMAT_PROPOSAL.md) - Detailed ConfigMap format specification
- [CRD_EXTENSIBILITY_PROPOSAL.md](.agents/CRD_EXTENSIBILITY_PROPOSAL.md) - CRD design decisions and rationale
