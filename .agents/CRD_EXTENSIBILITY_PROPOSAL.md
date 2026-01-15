# CRD Extensibility Proposal

## Principle

Maintain the familiar structure of the original sample, but prepared for future extension.

---

## Current CRD

```yaml
apiVersion: customrouter.freepik.com/v1alpha1
kind: CustomHTTPRoute
metadata:
  name: example-routes
spec:
  hostnames:
    - www.example.com
    - example.com

  # Path prefixes (languages, regions, etc.)
  pathPrefixes:
    values: [es, fr, it, de, pt, ja, ko]
    policy: Optional  # Optional | Required | Disabled

  rules:
    # Matches with PathPrefix and default priority (1000)
    - matches:
        - path: /calendar/halloween
        - path: /collection
      backendRefs:
        - name: web-en
          namespace: web
          port: 80

    # Exact match with high priority
    - matches:
        - path: /health
          type: Exact
          priority: 2000
      backendRefs:
        - name: health
          namespace: infra
          port: 8080
      pathPrefixes:
        policy: Disabled

    # Match with regex and custom priority
    - matches:
        - path: ^/users/[0-9]+/profile$
          type: Regex
          priority: 1500
      backendRefs:
        - name: users-api
          namespace: backend
          port: 8080

    # API without language prefixes
    - matches:
        - path: /api/v1
      backendRefs:
        - name: api
          namespace: backend
          port: 8080
      pathPrefixes:
        policy: Disabled

    # Default with low priority
    - matches:
        - path: /
          priority: 100
      backendRefs:
        - name: default
          namespace: web
          port: 80
```

---

## Match Types

| Type | Syntax | Use | Performance |
|------|--------|-----|-------------|
| `PathPrefix` | `/foo` | Static routes, languages | O(1) - `strings.HasPrefix` |
| `Exact` | `/health` | Health checks, specific endpoints | O(1) - `==` |
| `Regex` | `^/users/[0-9]+$` | Dynamic routes with IDs | O(n) - `regexp.MatchString` |

**Default:** `PathPrefix` (if `type` is not specified)

---

## Priority System

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `priority` | int32 | 1000 | Higher value = evaluated first |

**Usage examples:**
- `priority: 2000` - Critical routes (health checks, auth)
- `priority: 1000` - Normal routes (default)
- `priority: 100` - Catch-all routes

**Sorting in ConfigMap:**
1. Priority DESC (higher first)
2. Type: exact > regex > prefix
3. Path length DESC (longer first)

---

## Regex Expansion with Language Prefixes

Regex routes **are automatically expanded** with language prefixes:

**Input:**
```yaml
- matches:
    - path: ^/users/[0-9]+/profile$
      type: Regex
  backendRefs:
    - name: users-api
```

**Output in ConfigMap (policy: Optional):**
```json
{"path": "^(?:/(es|fr|it))?/users/[0-9]+/profile$", "type": "regex", ...}
```

**Output in ConfigMap (policy: Required):**
```json
{"path": "^/(es|fr|it)/users/[0-9]+/profile$", "type": "regex", ...}
```

**Output in ConfigMap (policy: Disabled):**
```json
{"path": "^/users/[0-9]+/profile$", "type": "regex", ...}
```

---

## Resulting ConfigMap

```json
{
  "version": 1,
  "hosts": {
    "www.example.com": [
      {"path": "/health", "type": "exact", "backend": "health.infra.svc.cluster.local:8080", "priority": 2000},
      {"path": "^(?:/(es|fr|it))?/users/[0-9]+/profile$", "type": "regex", "backend": "users-api.backend.svc.cluster.local:8080", "priority": 1500},
      {"path": "/es/calendar/halloween", "type": "prefix", "backend": "web-en.web.svc.cluster.local:80", "priority": 1000},
      {"path": "/calendar/halloween", "type": "prefix", "backend": "web-en.web.svc.cluster.local:80", "priority": 1000},
      {"path": "/api/v1", "type": "prefix", "backend": "api.backend.svc.cluster.local:8080", "priority": 1000},
      {"path": "/es/", "type": "prefix", "backend": "default.web.svc.cluster.local:80", "priority": 100},
      {"path": "/", "type": "prefix", "backend": "default.web.svc.cluster.local:80", "priority": 100}
    ]
  }
}
```

---

## Proxy Algorithm

```go
type Route struct {
    Path     string
    Type     string  // "prefix" | "exact" | "regex"
    Backend  string
    Priority int32
    regex    *regexp.Regexp  // Pre-compiled if Type == "regex"
}

func match(host, path string) string {
    routes, ok := config.Hosts[host]
    if !ok {
        return ""
    }
    
    // Routes are already sorted by priority from the controller
    for _, route := range routes {
        switch route.Type {
        case "exact":
            if path == route.Path {
                return route.Backend
            }
        case "regex":
            if route.regex.MatchString(path) {
                return route.Backend
            }
        default: // "prefix"
            if strings.HasPrefix(path, route.Path) {
                return route.Backend
            }
        }
    }
    return ""
}
```

---

## Regex Use Cases

### Numeric IDs
```yaml
- matches:
    - path: ^/orders/[0-9]+$
      type: Regex
      priority: 1200
```

### UUIDs
```yaml
- matches:
    - path: ^/sessions/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$
      type: Regex
```

### Slugs
```yaml
- matches:
    - path: ^/blog/[a-z0-9-]+$
      type: Regex
```

### File Extensions
```yaml
- matches:
    - path: \.(jpg|jpeg|png|gif|webp)$
      type: Regex
      priority: 1500
  backendRefs:
    - name: cdn
      namespace: infra
      port: 80
  pathPrefixes:
    policy: Disabled
```

---

## Future Extension: Headers

When A/B testing or feature flags are needed:

```yaml
spec:
  # ... hostnames, pathPrefixes ...

  headers:
    - name: X-Experiment
      values: [control, variant-a]
      policy: Optional

  rules:
    - matches:
        - path: /checkout
          priority: 1000
      backendRefs:
        - name: checkout
          namespace: web
          port: 80
      # Will expand with header combinations
```

---

## Summary

| Type | When to use | Expands with pathPrefixes | Default priority |
|------|-------------|---------------------------|------------------|
| `PathPrefix` | Static routes | Yes (multiple entries) | 1000 |
| `Exact` | Health checks, unique endpoints | No | 1000 |
| `Regex` | Dynamic IDs, complex patterns | Yes (modifies the regex) | 1000 |
