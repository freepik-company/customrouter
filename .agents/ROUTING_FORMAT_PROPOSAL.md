# Propuesta de Formato para ConfigMap de Rutas

## Principios de Diseño

1. **Un solo fichero** - Sin lookups entre keys (particionado automático si excede 900KB)
2. **Rutas pre-expandidas** - Cada combinación host+lang+path es una entrada
3. **Ordenado por prioridad** - Priority DESC, luego por tipo, luego por longitud de path
4. **Zero lógica en el proxy** - Solo comparar host, iterar rutas ordenadas, primer match gana

---

## Formato del ConfigMap

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
        "www.magnific.com": [
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

**Campos:**
- `path` - Path a matchear (literal, regex expandida, o con prefijo de idioma)
- `type` - Tipo de match: `exact`, `regex`, `prefix`
- `backend` - Destino en formato `service.namespace.svc.cluster.local:port`
- `priority` - Prioridad numérica (mayor = se evalúa primero). Default: 1000

**Ordenamiento:**
1. Priority DESC (mayor primero)
2. Type: exact > regex > prefix
3. Path length DESC (más largo primero)

---

## Algoritmo del Proxy

```go
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

## Particionado Automático

Cuando el ConfigMap excede 900KB:
- Se crean múltiples ConfigMaps: `customrouter-routes-0`, `customrouter-routes-1`, etc.
- Cada uno tiene el label `customrouter.freepik.com/part` con su índice
- El proxy debe cargar todos los ConfigMaps con el label `app.kubernetes.io/managed-by: customrouter-controller`

---

## CRD Correspondiente

```yaml
apiVersion: customrouter.freepik.com/v1alpha1
kind: CustomHTTPRoute
metadata:
  name: magnific-routes
spec:
  hostnames:
    - www.magnific.com

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

**Expansión por el controller:**
- `PathPrefix` con `policy: Optional` → genera N+1 entradas (una por idioma + sin prefijo)
- `PathPrefix` con `policy: Required` → genera N entradas (solo con prefijos)
- `PathPrefix` con `policy: Disabled` → genera 1 entrada (path literal)
- `Regex` → genera 1 entrada con la regex modificada para incluir prefijos opcionales
- `Exact` → genera 1 entrada (sin expansión de prefijos)
