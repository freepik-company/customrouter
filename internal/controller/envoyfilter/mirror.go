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
	"regexp"
	"sort"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/pkg/routes"
)

const (
	// MirrorFilterSuffix is the EnvoyFilter name suffix for mirror routes.
	MirrorFilterSuffix = "-mirror"

	// mirrorPatchPriority ensures mirror patches are applied after the generic
	// routes patch (default priority 0). Combined with INSERT_BEFORE targeting
	// customrouter-dynamic-route, this places mirror routes immediately ahead
	// of the catch-all ExtProc route so they match first.
	mirrorPatchPriority int64 = 10

	// dynamicRouteName is the name of the generic ExtProc-driven route created
	// by reconcileRoutesEnvoyFilter. Mirror patches INSERT_BEFORE it.
	dynamicRouteName = "customrouter-dynamic-route"
)

// MirrorEntry represents a single (hostname, expanded route, mirror target)
// tuple ready to be rendered into an Envoy route with request_mirror_policies.
type MirrorEntry struct {
	Hostname string
	Route    routes.Route
	Mirror   routes.RouteMirror
}

// CollectMirrorEntries iterates every CustomHTTPRoute, expands its rules into
// concrete per-hostname routes, and emits one MirrorEntry per mirror target.
// The resulting slice is sorted deterministically so the generated EnvoyFilter
// is stable across reconciles (no spurious updates).
func CollectMirrorEntries(routeList *v1alpha1.CustomHTTPRouteList) []MirrorEntry {
	entries := make([]MirrorEntry, 0, len(routeList.Items))

	for i := range routeList.Items {
		cr := &routeList.Items[i]
		if cr.DeletionTimestamp != nil && !cr.DeletionTimestamp.IsZero() {
			continue
		}
		if !hasMirrorAction(cr) {
			continue
		}

		hostMap, err := routes.ExpandRoutes(cr, nil)
		if err != nil {
			continue
		}
		for host, rs := range hostMap {
			for j := range rs {
				route := rs[j]
				for _, m := range route.Mirrors {
					entries = append(entries, MirrorEntry{
						Hostname: host,
						Route:    route,
						Mirror:   m,
					})
				}
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
		if entries[i].Route.Path != entries[j].Route.Path {
			return entries[i].Route.Path < entries[j].Route.Path
		}
		return BuildClusterName(entries[i].Mirror.BackendRef) < BuildClusterName(entries[j].Mirror.BackendRef)
	})

	return entries
}

// typePriority mirrors the ordering used by pkg/routes.SortRoutes so the
// EnvoyFilter emits mirror routes in specificity order (exact > regex > prefix).
var typePriority = map[string]int{
	routes.RouteTypeExact:  0,
	routes.RouteTypeRegex:  1,
	routes.RouteTypePrefix: 2,
}

// hasMirrorAction is a cheap pre-filter that skips ExpandRoutes for routes
// that clearly have no mirror actions. Avoids unnecessary expansion work.
func hasMirrorAction(cr *v1alpha1.CustomHTTPRoute) bool {
	for _, rule := range cr.Spec.Rules {
		for _, a := range rule.Actions {
			if a.Type == v1alpha1.ActionTypeRequestMirror {
				return true
			}
		}
	}
	return false
}

// BuildMirrorEnvoyFilter builds the {epa}-mirror EnvoyFilter unstructured object
// that installs Envoy-native request_mirror_policies for every collected entry.
// The ExtProc data plane is not involved in mirror dispatch — it remains on the
// primary-request hot path only.
func BuildMirrorEnvoyFilter(
	epa *v1alpha1.ExternalProcessorAttachment,
	entries []MirrorEntry,
) (*unstructured.Unstructured, error) {
	filterName := epa.Name + MirrorFilterSuffix

	ef := &unstructured.Unstructured{}
	ef.SetGroupVersionKind(GVK)
	ef.SetName(filterName)
	ef.SetNamespace(epa.Namespace)
	ef.SetLabels(StandardLabels(epa.Name))
	ef.SetOwnerReferences([]metav1.OwnerReference{NewOwnerReference(epa)})

	selectorInterface := SelectorToInterface(epa.Spec.GatewayRef.Selector)

	configPatches := make([]interface{}, 0, len(entries))
	for i := range entries {
		configPatches = append(configPatches, buildMirrorPatch(epa, &entries[i]))
	}

	spec := map[string]interface{}{
		"workloadSelector": map[string]interface{}{
			"labels": selectorInterface,
		},
		"priority":      mirrorPatchPriority,
		"configPatches": configPatches,
	}

	if err := unstructured.SetNestedField(ef.Object, spec, "spec"); err != nil {
		return nil, fmt.Errorf("failed to set spec: %w", err)
	}

	return ef, nil
}

// buildMirrorPatch builds a single HTTP_ROUTE configPatch that inserts a new
// mirror-enabled route immediately before the generic ExtProc route. The route
// re-uses cluster_header so the primary backend is still ExtProc-selected;
// Envoy attaches a request_mirror_policy pointing at the mirror cluster.
func buildMirrorPatch(epa *v1alpha1.ExternalProcessorAttachment, entry *MirrorEntry) map[string]interface{} {
	match := BuildRouteMatch(&entry.Route)

	// Hostname-scope the mirror via :authority so the mirror route does not
	// leak into virtual hosts owned by other CustomHTTPRoutes that happen to
	// share a path shape. The x-customrouter-cluster presence check ensures
	// we only run on requests ExtProc has already accepted.
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
		"timeout":        GetRouteTimeout(epa),
		"request_mirror_policies": []interface{}{
			buildMirrorPolicy(&entry.Mirror),
		},
	}
	ApplyRetryPolicy(routeAction, epa)

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
				"name":  mirrorRouteName(entry),
				"match": match,
				"route": routeAction,
			},
		},
	}
}

// buildMirrorPolicy assembles the request_mirror_policies entry. When Percent
// is unset the mirror fires for every matched request; when set, runtime_fraction
// gates it via Envoy's native fractional-percent sampler (denominator HUNDRED).
func buildMirrorPolicy(m *routes.RouteMirror) map[string]interface{} {
	policy := map[string]interface{}{
		"cluster": BuildClusterName(m.BackendRef),
	}
	if m.Percent != nil && *m.Percent < 100 {
		policy["runtime_fraction"] = map[string]interface{}{
			"default_value": map[string]interface{}{
				"numerator":   int64(*m.Percent),
				"denominator": "HUNDRED",
			},
		}
	}
	return policy
}

// mirrorRouteName derives a deterministic, Envoy-safe route name from the
// entry so re-renders produce byte-identical EnvoyFilters when the inputs
// are unchanged (no spurious controller updates).
func mirrorRouteName(entry *MirrorEntry) string {
	h := sha1.New()
	_, _ = h.Write([]byte(entry.Hostname + "|" + entry.Route.Path + "|" +
		entry.Route.Type + "|" + entry.Route.Method + "|" +
		BuildClusterName(entry.Mirror.BackendRef) + "|" +
		percentString(entry.Mirror.Percent)))
	return "customrouter-mirror-" + hex.EncodeToString(h.Sum(nil))[:12]
}

func percentString(p *int32) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(int(*p))
}

// authorityMatcher builds an :authority header matcher for the given hostname.
// Wildcard hostnames ("*.example.com") produce a safe_regex; literals use a
// regex too so the optional ":<port>" suffix of the authority header is
// tolerated without requiring callers to think about it. A bare "*" disables
// the restriction (applies to any hostname).
func authorityMatcher(hostname string) map[string]interface{} {
	if hostname == "" || hostname == "*" {
		return nil
	}
	pattern := hostnameToAuthorityRegex(hostname)
	return map[string]interface{}{
		"name": ":authority",
		"safe_regex_match": map[string]interface{}{
			"regex": pattern,
		},
	}
}

// hostnameToAuthorityRegex converts a Gateway-API-style hostname (which may
// begin with a "*." wildcard label) into an anchored regex that matches the
// HTTP/2 :authority pseudo-header. The port suffix is optional.
func hostnameToAuthorityRegex(hostname string) string {
	if strings.HasPrefix(hostname, "*.") {
		rest := regexp.QuoteMeta(strings.TrimPrefix(hostname, "*."))
		return `^[^.]+\.` + rest + `(:[0-9]+)?$`
	}
	return `^` + regexp.QuoteMeta(hostname) + `(:[0-9]+)?$`
}
