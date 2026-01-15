# AGENTS.md

This document helps AI agents work effectively in the customrouter codebase.

## Project Overview

**customrouter** is a Kubernetes operator built with [Kubebuilder](https://book.kubebuilder.io/) (v4.10.1) that manages `CustomHTTPRoute` custom resources. The operator generates ConfigMaps with pre-expanded routing rules for use by an Envoy proxy.

- **Domain**: `customrouter.freepik.com`
- **API Group**: `customrouter.freepik.com/v1alpha1`
- **Primary Resource**: `CustomHTTPRoute` (namespaced)
- **Go Version**: 1.24.6
- **Controller Runtime**: v0.22.4

## Essential Commands

### Build & Run

| Command | Description |
|---------|-------------|
| `make build` | Build the manager binary to `bin/manager` |
| `make run` | Run the controller locally (requires kubeconfig) |
| `make docker-build IMG=<image>` | Build Docker image |
| `make docker-push IMG=<image>` | Push Docker image to registry |

### Testing

| Command | Description |
|---------|-------------|
| `make test` | Run unit tests (excludes e2e) |
| `make test-e2e` | Run e2e tests using Kind cluster |
| `make setup-test-e2e` | Create Kind cluster for e2e tests |
| `make cleanup-test-e2e` | Delete the Kind e2e test cluster |

### Linting

| Command | Description |
|---------|-------------|
| `make lint` | Run golangci-lint |
| `make lint-fix` | Run golangci-lint with auto-fix |

### Code Generation

| Command | Description |
|---------|-------------|
| `make generate` | Generate DeepCopy methods (run after changing types) |
| `make manifests` | Generate CRD, RBAC, webhook manifests |

**Important**: After modifying `api/v1alpha1/*_types.go`, always run:
```bash
make generate manifests
```

### Deployment

| Command | Description |
|---------|-------------|
| `make install` | Install CRDs to cluster |
| `make deploy IMG=<image>` | Deploy controller to cluster |
| `make undeploy` | Remove controller from cluster |

## Code Organization

```
.
├── api/v1alpha1/                    # API type definitions
│   ├── customhttproute_types.go     # CustomHTTPRoute spec/status
│   ├── groupversion_info.go         # GroupVersion registration
│   └── zz_generated.deepcopy.go     # Generated (DO NOT EDIT)
├── cmd/main.go                      # Entrypoint, manager setup, flags
├── internal/controller/             # Controller logic
│   ├── commons.go                   # Shared constants, UpdateWithRetry helper
│   ├── conditions.go                # Condition helpers
│   └── customhttproute/
│       ├── controller.go            # Main reconciliation loop (Kubebuilder)
│       ├── status.go                # Status condition updaters
│       ├── sync.go                  # ReconcileObject, ConfigMap generation
│       ├── routes.go                # Route expansion logic
│       ├── regex.go                 # Regex expansion with lang prefixes
│       └── regex_test.go            # Regex tests
├── config/
│   ├── crd/bases/                   # Generated CRD YAML
│   ├── samples/                     # Example CR manifests
│   └── ...                          # RBAC, manager, etc.
├── .agents/                         # Agent documentation
│   ├── AGENTS.md                    # This file
│   ├── ROUTING_FORMAT_PROPOSAL.md   # ConfigMap format spec
│   └── CRD_EXTENSIBILITY_PROPOSAL.md # CRD design decisions
└── test/                            # Tests
```

## CRD Structure

```yaml
apiVersion: customrouter.freepik.com/v1alpha1
kind: CustomHTTPRoute
spec:
  hostnames: [www.example.com]
  
  pathPrefixes:
    values: [es, fr, it]
    policy: Optional  # Optional | Required | Disabled
  
  rules:
    - matches:
        - path: /foo
          type: PathPrefix  # PathPrefix | Exact | Regex
          priority: 1000    # Higher = evaluated first (default: 1000)
      backendRefs:
        - name: service
          namespace: ns
          port: 80
      pathPrefixes:
        policy: Disabled  # Override spec-level policy
```

## Key Types

### PathMatch
```go
type PathMatch struct {
    Path     string    `json:"path"`
    Type     MatchType `json:"type,omitempty"`     // Default: PathPrefix
    Priority int32     `json:"priority,omitempty"` // Default: 1000
}
```

### Route (internal, for ConfigMap)
```go
type Route struct {
    Path     string `json:"path"`
    Type     string `json:"type"`     // "prefix", "exact", "regex"
    Backend  string `json:"backend"`
    Priority int32  `json:"priority"`
}
```

## Route Expansion Logic

Located in `internal/controller/customhttproute/routes.go`:

1. **PathPrefix** with `Optional` → N+1 routes (one per lang + one without)
2. **PathPrefix** with `Required` → N routes (only with lang prefixes)
3. **PathPrefix** with `Disabled` → 1 route (literal path)
4. **Exact** → 1 route (no prefix expansion)
5. **Regex** → 1 route with modified pattern (prefixes embedded in regex)

### Regex Expansion

Located in `internal/controller/customhttproute/regex.go`:

```
Input:  ^/users/[0-9]+$
Output: ^(?:/(es|fr|it))?/users/[0-9]+$  (policy: Optional)
Output: ^/(es|fr|it)/users/[0-9]+$       (policy: Required)
```

## ConfigMap Generation

Located in `internal/controller/customhttproute/sync.go`:

- Creates ConfigMaps named `{base}-0`, `{base}-1`, etc.
- Partitions automatically if data exceeds 900KB
- Labels: `app.kubernetes.io/managed-by: customrouter-controller`
- Deletes stale ConfigMaps when routes are removed

### Flags (cmd/main.go)

```
--routes-configmap-name       (default: "customrouter-routes")
--routes-configmap-namespace  (default: "istio-system")
```

## Sorting Logic

Routes are sorted by:
1. **Priority DESC** (higher first)
2. **Type**: exact > regex > prefix
3. **Path length DESC** (longer first)

## Constants

```go
const DefaultPriority int32 = 1000
```

## Testing

### Regex Tests
```bash
go test ./internal/controller/customhttproute/... -run TestExpandRegex -v
```

### All Tests
```bash
make test
```

## Quick Reference

```bash
# After modifying types
make generate manifests

# Before committing
make fmt vet lint
make test

# Run locally
make run -- --routes-configmap-namespace=default

# Deploy
make install
make deploy IMG=myimage
kubectl apply -f config/samples/v1alpha1_customhttproute.yaml
```
