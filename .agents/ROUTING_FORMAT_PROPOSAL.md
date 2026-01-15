# Route ConfigMap Format Proposal

## Design Principles

1. **Single file** - No lookups between keys (automatic partitioning if exceeds 900KB)
2. **Pre-expanded routes** - Each host+lang+path combination is an entry
3. **Sorted by priority** - Priority DESC, then by type, then by path length
4. **Zero logic in proxy** - Just compare host, iterate sorted routes, first match wins

---

## ConfigMap Format

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: customrouter-routes-0
  namespace: istio-system
  labels:
    app.kubernetes.io/name: customrouter
    app.kubernetes.io/managed-by: customrouter-controller
    customrouter.freepik.com/part: "0"
data:
  routes.json: |
    {
      "version": 1,
      "hosts": {
        "www.example.com": [
          {"path": "/health", "type": "exact", "backend": "health.infra.svc.cluster.local:8080", "priority": 2000},
          {"path": "^(?:/(es|fr|it))?/users/[0-9]+/profile$", "type": "regex", "backend": "users-api.backend.svc.cluster.local:8080", "priority": 1500},
          {"path": "/es/calendar/halloween", "type": "prefix", "backend": "web-en.web.svc.cluster.local:80", "priority": 1000},
          {"path": "/fr/calendar/halloween", "type": "prefix", "backend": "web-en.web.svc.cluster.local:80", "priority": 1000},
          {"path": "/calendar/halloween", "type": "prefix", "backend": "web-en.web.svc.cluster.local:80", "priority": 1000},
          {"path": "/api/v1", "type": "prefix", "backend": "api.backend.svc.cluster.local:8080", "priority": 1000},
          {"path": "/es/", "type": "prefix", "backend": "default.web.svc.cluster.local:80", "priority": 100},
          {"path": "/", "type": "prefix", "backend": "default.web.svc.cluster.local:80", "priority": 100}
        ]
      }
    }
```

**Fields:**
- `path` - Path to match (literal, expanded regex, or with language prefix)
- `type` - Match type: `exact`, `regex`, `prefix`
- `backend` - Destination in format `service.namespace.svc.cluster.local:port`
- `priority` - Numeric priority (higher = evaluated first). Default: 1000

**Sorting:**
1. Priority DESC (higher first)
2. Type: exact > regex > prefix
3. Path length DESC (longer first)

---

## Proxy Algorithm

```go
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
            if route.compiledRegex.MatchString(path) {
                return route.Backend
            }
        default: // prefix
            if strings.HasPrefix(path, route.Path) {
                return route.Backend
            }
        }
    }
    return ""
}
```

---

## Automatic Partitioning

When the ConfigMap exceeds 900KB:
- Multiple ConfigMaps are created: `customrouter-routes-0`, `customrouter-routes-1`, etc.
- Each one has the label `customrouter.freepik.com/part` with its index
- The proxy must load all ConfigMaps with the label `app.kubernetes.io/managed-by: customrouter-controller`

---

## Corresponding CRD

```yaml
apiVersion: customrouter.freepik.com/v1alpha1
kind: CustomHTTPRoute
metadata:
  name: example-routes
spec:
  hostnames:
    - www.example.com

  pathPrefixes:
    values: [es, fr, it]
    policy: Optional

  rules:
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

    - matches:
        - path: ^/users/[0-9]+/profile$
          type: Regex
          priority: 1500
      backendRefs:
        - name: users-api
          namespace: backend
          port: 8080

    - matches:
        - path: /calendar/halloween
        - path: /collection
      backendRefs:
        - name: web-en
          namespace: web
          port: 80

    - matches:
        - path: /
          priority: 100
      backendRefs:
        - name: default
          namespace: web
          port: 80
```

**Expansion by controller:**
- `PathPrefix` with `policy: Optional` → generates N+1 entries (one per language + without prefix)
- `PathPrefix` with `policy: Required` → generates N entries (only with prefixes)
- `PathPrefix` with `policy: Disabled` → generates 1 entry (literal path)
- `Regex` → generates 1 entry with the regex modified to include optional prefixes
- `Exact` → generates 1 entry (no prefix expansion)
