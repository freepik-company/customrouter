# Propuesta de Extensibilidad del CRD

## Principio

Mantener la estructura familiar del sample original, pero preparada para extensión futura.

---

## CRD Actual

```yaml
apiVersion: customrouter.freepik.com/v1alpha1
kind: CustomHTTPRoute
metadata:
  name: magnific-routes
spec:
  hostnames:
    - www.magnific.com
    - magnific.com

  # Prefijos de path (idiomas, regiones, etc.)
  pathPrefixes:
    values: [es, fr, it, de, pt, ja, ko]
    policy: Optional  # Optional | Required | Disabled

  rules:
    # Matches con PathPrefix y prioridad por defecto (1000)
    - matches:
        - path: /calendar/halloween
        - path: /collection
      backendRefs:
        - name: web-en
          namespace: web
          port: 80

    # Match exacto con alta prioridad
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

    # Match con regex y prioridad personalizada
    - matches:
        - path: ^/users/[0-9]+/profile$
          type: Regex
          priority: 1500
      backendRefs:
        - name: users-api
          namespace: backend
          port: 8080

    # API sin prefijos de idioma
    - matches:
        - path: /api/v1
      backendRefs:
        - name: api
          namespace: backend
          port: 8080
      pathPrefixes:
        policy: Disabled

    # Default con baja prioridad
    - matches:
        - path: /
          priority: 100
      backendRefs:
        - name: default
          namespace: web
          port: 80
```

---

## Tipos de Match

| Type | Sintaxis | Uso | Performance |
|------|----------|-----|-------------|
| `PathPrefix` | `/foo` | Rutas estáticas, idiomas | O(1) - `strings.HasPrefix` |
| `Exact` | `/health` | Health checks, endpoints específicos | O(1) - `==` |
| `Regex` | `^/users/[0-9]+$` | Rutas dinámicas con IDs | O(n) - `regexp.MatchString` |

**Default:** `PathPrefix` (si no se especifica `type`)

---

## Sistema de Prioridad

| Campo | Tipo | Default | Descripción |
|-------|------|---------|-------------|
| `priority` | int32 | 1000 | Mayor valor = se evalúa primero |

**Ejemplos de uso:**
- `priority: 2000` - Rutas críticas (health checks, auth)
- `priority: 1000` - Rutas normales (default)
- `priority: 100` - Rutas catch-all

**Ordenamiento en ConfigMap:**
1. Priority DESC (mayor primero)
2. Type: exact > regex > prefix
3. Path length DESC (más largo primero)

---

## Expansión de Regex con Prefijos de Idioma

Las rutas regex **se expanden automáticamente** con los prefijos de idioma:

**Input:**
```yaml
- matches:
    - path: ^/users/[0-9]+/profile$
      type: Regex
  backendRefs:
    - name: users-api
```

**Output en ConfigMap (policy: Optional):**
```json
{"path": "^(?:/(es|fr|it))?/users/[0-9]+/profile$", "type": "regex", ...}
```

**Output en ConfigMap (policy: Required):**
```json
{"path": "^/(es|fr|it)/users/[0-9]+/profile$", "type": "regex", ...}
```

**Output en ConfigMap (policy: Disabled):**
```json
{"path": "^/users/[0-9]+/profile$", "type": "regex", ...}
```

---

## ConfigMap Resultante

```json
{
  "version": 1,
  "hosts": {
    "www.magnific.com": [
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

## Algoritmo del Proxy

```go
type Route struct {
    Path     string
    Type     string  // "prefix" | "exact" | "regex"
    Backend  string
    Priority int32
    regex    *regexp.Regexp  // Pre-compilado si Type == "regex"
}

func match(host, path string) string {
    routes, ok := config.Hosts[host]
    if !ok {
        return ""
    }
    
    // Las rutas ya vienen ordenadas por prioridad del controller
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

## Casos de Uso Regex

### IDs numéricos
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

### Extensiones de archivo
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

## Extensión Futura: Headers

Cuando se necesite A/B testing o feature flags:

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
      # Expandirá con combinaciones de headers
```

---

## Resumen

| Tipo | Cuándo usar | Expande con pathPrefixes | Priority default |
|------|-------------|--------------------------|------------------|
| `PathPrefix` | Rutas estáticas | ✅ Sí (múltiples entradas) | 1000 |
| `Exact` | Health checks, endpoints únicos | ❌ No | 1000 |
| `Regex` | IDs dinámicos, patrones complejos | ✅ Sí (modifica la regex) | 1000 |
