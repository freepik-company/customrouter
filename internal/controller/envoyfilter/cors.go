/*
Copyright 2024-2026 Freepik Company S.L.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package envoyfilter

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/pkg/routes"
)

const (
	// CORSFilterSuffix is the EnvoyFilter name suffix for CORS policies.
	CORSFilterSuffix = "-cors"

	// corsFilterName is the canonical HTTP filter name that Istio installs in
	// the Gateway listener chain. The typed_per_filter_config on our injected
	// routes must use this exact key for Envoy to apply the policy.
	corsFilterName = "envoy.filters.http.cors"

	// corsPolicyTypeURL is the v3 CORS policy message. Istio's Gateway listener
	// ships with envoy.filters.http.cors pre-installed, so we only need to
	// attach per-route policies — no filter-chain patch required.
	corsPolicyTypeURL = "type.googleapis.com/envoy.extensions.filters.http.cors.v3.CorsPolicy"

	// corsPatchPriority keeps CORS patches aligned with mirror patches so
	// INSERT_BEFORE customrouter-dynamic-route is processed after the base
	// routes filter. See mirrorPatchPriority for the same rationale.
	corsPatchPriority int64 = 10
)

// CORSEntry is a (hostname, expanded route, policy) tuple ready to be rendered.
type CORSEntry struct {
	Hostname string
	Route    routes.Route
	Policy   routes.RouteCORS
}

// CollectCORSEntries iterates every CustomHTTPRoute, expands its rules, and
// emits one entry per (hostname, route) with a CORS policy attached. The
// output is sorted deterministically so repeated reconciles produce identical
// EnvoyFilters.
func CollectCORSEntries(routeList *v1alpha1.CustomHTTPRouteList) []CORSEntry {
	entries := make([]CORSEntry, 0, len(routeList.Items))

	for i := range routeList.Items {
		cr := &routeList.Items[i]
		if cr.DeletionTimestamp != nil && !cr.DeletionTimestamp.IsZero() {
			continue
		}
		if !hasCORSAction(cr) {
			continue
		}

		hostMap, err := routes.ExpandRoutes(cr, nil)
		if err != nil {
			continue
		}
		for host, rs := range hostMap {
			for j := range rs {
				route := rs[j]
				if route.CORS == nil {
					continue
				}
				entries = append(entries, CORSEntry{
					Hostname: host,
					Route:    route,
					Policy:   *route.CORS,
				})
			}
		}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Hostname != entries[j].Hostname {
			return entries[i].Hostname < entries[j].Hostname
		}
		if entries[i].Route.Priority != entries[j].Route.Priority {
			return entries[i].Route.Priority > entries[j].Route.Priority
		}
		if entries[i].Route.Type != entries[j].Route.Type {
			return typePriority[entries[i].Route.Type] < typePriority[entries[j].Route.Type]
		}
		if len(entries[i].Route.Path) != len(entries[j].Route.Path) {
			return len(entries[i].Route.Path) > len(entries[j].Route.Path)
		}
		return entries[i].Route.Path < entries[j].Route.Path
	})

	return entries
}

// hasCORSAction is a cheap pre-filter that skips ExpandRoutes when no CORS
// action is declared anywhere in the resource.
func hasCORSAction(cr *v1alpha1.CustomHTTPRoute) bool {
	for _, rule := range cr.Spec.Rules {
		for _, a := range rule.Actions {
			if a.Type == v1alpha1.ActionTypeCORS {
				return true
			}
		}
	}
	return false
}

// BuildCORSEnvoyFilter builds the {epa}-cors EnvoyFilter. For each CORSEntry
// it emits an HTTP_ROUTE patch that inserts a route carrying both the normal
// ExtProc-backed primary action AND a typed_per_filter_config entry binding
// the Envoy CORS filter to the rendered policy. Preflight responses and
// response-header injection run inside Envoy; the ExtProc hot path is not
// touched.
func BuildCORSEnvoyFilter(
	epa *v1alpha1.ExternalProcessorAttachment,
	entries []CORSEntry,
) (*unstructured.Unstructured, error) {
	filterName := epa.Name + CORSFilterSuffix

	ef := &unstructured.Unstructured{}
	ef.SetGroupVersionKind(GVK)
	ef.SetName(filterName)
	ef.SetNamespace(epa.Namespace)
	ef.SetLabels(StandardLabels(epa.Name))
	ef.SetOwnerReferences([]metav1.OwnerReference{NewOwnerReference(epa)})

	selectorInterface := SelectorToInterface(epa.Spec.GatewayRef.Selector)

	configPatches := make([]interface{}, 0, len(entries))
	numRetries := GetNumRetries(epa)
	for i := range entries {
		configPatches = append(configPatches, buildCORSPatch(&entries[i], numRetries))
	}

	spec := map[string]interface{}{
		"workloadSelector": map[string]interface{}{
			"labels": selectorInterface,
		},
		"priority":      corsPatchPriority,
		"configPatches": configPatches,
	}

	if err := unstructured.SetNestedField(ef.Object, spec, "spec"); err != nil {
		return nil, fmt.Errorf("failed to set spec: %w", err)
	}

	return ef, nil
}

func buildCORSPatch(entry *CORSEntry, numRetries int64) map[string]interface{} {
	match := BuildRouteMatch(&entry.Route)

	headers, _ := match["headers"].([]interface{})
	if headers == nil {
		headers = []interface{}{}
	}
	if matcher := authorityMatcher(entry.Hostname); matcher != nil {
		headers = append(headers, matcher)
	}
	headers = append(headers, map[string]interface{}{
		"name":          "x-customrouter-cluster",
		"present_match": true,
	})
	match["headers"] = headers

	routeAction := map[string]interface{}{
		"cluster_header": "x-customrouter-cluster",
		"timeout":        "30s",
		"retry_policy": map[string]interface{}{
			"retry_on":               "connect-failure,refused-stream,unavailable,cancelled,retriable-status-codes",
			"num_retries":            numRetries,
			"retriable_status_codes": []interface{}{int64(503)},
		},
	}

	return map[string]interface{}{
		"applyTo": "HTTP_ROUTE",
		"match": map[string]interface{}{
			"context": "GATEWAY",
			"routeConfiguration": map[string]interface{}{
				"vhost": map[string]interface{}{
					"route": map[string]interface{}{
						"name": dynamicRouteName,
					},
				},
			},
		},
		"patch": map[string]interface{}{
			"operation": "INSERT_BEFORE",
			"value": map[string]interface{}{
				"name":  corsRouteName(entry),
				"match": match,
				"route": routeAction,
				"typed_per_filter_config": map[string]interface{}{
					corsFilterName: buildCORSPolicyTyped(&entry.Policy),
				},
			},
		},
	}
}

// buildCORSPolicyTyped renders the Envoy CORS policy as a typed_per_filter_config
// "Any" message. Each AllowOrigins entry becomes a StringMatcher; "*" is
// expressed as safe_regex ".*" so it interoperates with the allow_credentials
// rejection path in browsers (validation forbids that combination anyway).
// AllowMethods/Headers/ExposeHeaders are comma-joined strings as Envoy expects.
func buildCORSPolicyTyped(p *routes.RouteCORS) map[string]interface{} {
	origins := make([]interface{}, 0, len(p.AllowOrigins))
	for _, o := range p.AllowOrigins {
		origins = append(origins, originStringMatcher(o))
	}

	policy := map[string]interface{}{
		"@type":                     corsPolicyTypeURL,
		"allow_origin_string_match": origins,
	}
	if len(p.AllowMethods) > 0 {
		policy["allow_methods"] = strings.Join(p.AllowMethods, ",")
	}
	if len(p.AllowHeaders) > 0 {
		policy["allow_headers"] = strings.Join(p.AllowHeaders, ",")
	}
	if len(p.ExposeHeaders) > 0 {
		policy["expose_headers"] = strings.Join(p.ExposeHeaders, ",")
	}
	if p.AllowCredentials {
		policy["allow_credentials"] = true
	}
	if p.MaxAge > 0 {
		policy["max_age"] = strconv.Itoa(int(p.MaxAge))
	}
	return policy
}

// originStringMatcher renders a single AllowOrigins entry as Envoy's
// StringMatcher. "*" becomes a regex wildcard; everything else is an exact
// match (case-sensitive, as browsers send the Origin header lowercased-scheme
// plus host verbatim).
func originStringMatcher(origin string) map[string]interface{} {
	if origin == "*" {
		return map[string]interface{}{
			"safe_regex": map[string]interface{}{
				"regex": ".*",
			},
		}
	}
	return map[string]interface{}{
		"exact": origin,
	}
}

// corsRouteName derives a stable route name from the entry so re-renders
// produce byte-identical EnvoyFilters when the inputs are unchanged.
func corsRouteName(entry *CORSEntry) string {
	h := sha1.New()
	_, _ = h.Write([]byte(entry.Hostname + "|" + entry.Route.Path + "|" +
		entry.Route.Type + "|" + entry.Route.Method + "|" +
		corsPolicyFingerprint(&entry.Policy)))
	return "customrouter-cors-" + hex.EncodeToString(h.Sum(nil))[:12]
}

// corsPolicyFingerprint flattens the policy into a stable string so the route
// name changes when (and only when) the policy content changes.
func corsPolicyFingerprint(p *routes.RouteCORS) string {
	return strings.Join(p.AllowOrigins, ",") + "|" +
		strings.Join(p.AllowMethods, ",") + "|" +
		strings.Join(p.AllowHeaders, ",") + "|" +
		strings.Join(p.ExposeHeaders, ",") + "|" +
		strconv.FormatBool(p.AllowCredentials) + "|" +
		strconv.Itoa(int(p.MaxAge))
}
