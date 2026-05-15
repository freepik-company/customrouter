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

// Package envoyfilter provides shared helpers for building, upserting, and deleting
// Istio EnvoyFilter resources used by both the CustomHTTPRoute and
// ExternalProcessorAttachment controllers.
package envoyfilter

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/internal/controller"
)

// DefaultCatchAllPorts are the listener ports against which HTTP_ROUTE INSERT_FIRST
// patches are emitted when a hostname is already covered by an HTTPRoute. Patches
// targeting a non-existing (hostname, port) virtual host are silently ignored by
// Istio, so emitting both is safe.
var DefaultCatchAllPorts = []int{80, 443}

const (
	// EnvoyFilter name suffixes
	ExtProcFilterSuffix  = "-extproc"
	RoutesFilterSuffix   = "-routes"
	CatchAllFilterSuffix = "-catchall"

	// Labels for managed EnvoyFilters
	ManagedByLabel = "app.kubernetes.io/managed-by"
	ManagedByValue = "customrouter-controller"
	OwnerLabel     = "customrouter.freepik.com/attachment"
	AppNameLabel   = "app.kubernetes.io/name"
	AppNameValue   = "customrouter"
)

// GVK is the GroupVersionKind for Istio EnvoyFilter resources.
var GVK = schema.GroupVersionKind{
	Group:   "networking.istio.io",
	Version: "v1alpha3",
	Kind:    "EnvoyFilter",
}

// DefaultRouteTimeout is the per-request timeout applied to customrouter-managed
// routes when the EPA does not override it via spec.routeTimeout.
const DefaultRouteTimeout = "30s"

// defaultRetryOn is the retry_on policy list applied when the user enables retries
// (numRetries > 0) without specifying retryOn explicitly. This matches the legacy
// (pre-0.7.0) hardcoded value to keep upgrade behaviour predictable for users who
// did rely on retries.
const defaultRetryOn = "connect-failure,refused-stream,unavailable,cancelled,retriable-status-codes"

// defaultRetriableStatusCodes is the retriable_status_codes list applied when the
// user enables retries without specifying the codes explicitly. Matches the legacy
// hardcoded behaviour.
var defaultRetriableStatusCodes = []int32{503}

// GetRouteTimeout returns the configured per-request route timeout, defaulting
// to DefaultRouteTimeout when not specified.
func GetRouteTimeout(epa *v1alpha1.ExternalProcessorAttachment) string {
	if epa.Spec.RouteTimeout != "" {
		return epa.Spec.RouteTimeout
	}
	return DefaultRouteTimeout
}

// BuildRetryPolicy returns the retry_policy map to embed in an Envoy route's
// "route" section, or nil when no retry_policy block should be emitted.
//
// Returning nil when the user has not configured retries (RetryPolicy == nil)
// keeps the generated EnvoyFilters clean — no leftover retry_on/retriable
// fields in config dumps that would otherwise mislead operators auditing
// retry behaviour. Envoy treats an absent retry_policy and one with
// num_retries: 0 identically (no retries), so this is semantics-preserving.
//
// When RetryPolicy is non-nil but every field is zero-valued, an explicit
// "num_retries: 0" policy is still emitted: the user has expressed an intent
// to pin the value to zero, and surfacing it in the generated EnvoyFilter is
// useful for auditing. Callers can omit the whole RetryPolicy field to get
// the "no retry_policy block at all" behaviour.
//
// retryOn defaults to the legacy value
// "connect-failure,refused-stream,unavailable,cancelled,retriable-status-codes"
// when the field is empty; retriableStatusCodes defaults to [503]. These
// defaults match the pre-0.7.0 hardcoded behaviour so that a user only needs
// to set numRetries to opt back in to the historical retry profile.
func BuildRetryPolicy(rp *v1alpha1.RetryPolicyConfig) map[string]interface{} {
	if rp == nil {
		return nil
	}

	retryOn := rp.RetryOn
	if retryOn == "" {
		retryOn = defaultRetryOn
	}

	codes := rp.RetriableStatusCodes
	if len(codes) == 0 {
		codes = defaultRetriableStatusCodes
	}
	codesInterface := make([]interface{}, 0, len(codes))
	for _, c := range codes {
		codesInterface = append(codesInterface, int64(c))
	}

	out := map[string]interface{}{
		"retry_on":               retryOn,
		"num_retries":            rp.NumRetries,
		"retriable_status_codes": codesInterface,
	}
	if rp.PerTryTimeout != "" {
		out["per_try_timeout"] = rp.PerTryTimeout
	}
	return out
}

// ApplyRetryPolicy sets the retry_policy key on routeAction in place when a
// policy should be emitted, and leaves it absent otherwise. Centralised here
// so all four call sites (routes, catch-all, mirror, CORS) cannot drift apart
// on the "when to emit" decision.
func ApplyRetryPolicy(routeAction map[string]interface{}, epa *v1alpha1.ExternalProcessorAttachment) {
	if policy := BuildRetryPolicy(epa.Spec.RetryPolicy); policy != nil {
		routeAction["retry_policy"] = policy
	}
}

// CatchAllEntry represents a hostname with its default backend for catch-all routing.
type CatchAllEntry struct {
	Hostname   string
	BackendRef v1alpha1.BackendRef
}

// BoolPtr returns a pointer to the given bool value.
func BoolPtr(b bool) *bool {
	return &b
}

// BuildClusterName builds the Istio cluster name for a BackendRef.
func BuildClusterName(ref v1alpha1.BackendRef) string {
	if strings.Contains(ref.Name, ".") {
		return fmt.Sprintf("outbound|%d||%s", ref.Port, ref.Name)
	}
	return fmt.Sprintf("outbound|%d||%s.%s.svc.cluster.local", ref.Port, ref.Name, ref.Namespace)
}

// NewOwnerReference builds an owner reference for the given EPA.
func NewOwnerReference(epa *v1alpha1.ExternalProcessorAttachment) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: epa.APIVersion,
		Kind:       epa.Kind,
		Name:       epa.Name,
		UID:        epa.UID,
		Controller: BoolPtr(true),
	}
}

// StandardLabels returns the common labels for an EnvoyFilter owned by the given EPA.
func StandardLabels(epaName string) map[string]string {
	return map[string]string{
		AppNameLabel:   AppNameValue,
		ManagedByLabel: ManagedByValue,
		OwnerLabel:     epaName,
	}
}

// SelectorToInterface converts a string map to interface map for unstructured objects.
func SelectorToInterface(selector map[string]string) map[string]interface{} {
	out := make(map[string]interface{}, len(selector))
	for k, v := range selector {
		out[k] = v
	}
	return out
}

// UpsertUnstructured creates or updates an unstructured object with retry on conflict.
func UpsertUnstructured(ctx context.Context, cl client.Client, obj *unstructured.Unstructured) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(obj.GroupVersionKind())
		key := types.NamespacedName{
			Name:      obj.GetName(),
			Namespace: obj.GetNamespace(),
		}

		err := cl.Get(ctx, key, existing)
		if errors.IsNotFound(err) {
			return cl.Create(ctx, obj)
		}
		if err != nil {
			return err
		}

		obj.SetResourceVersion(existing.GetResourceVersion())
		return cl.Update(ctx, obj)
	})
}

// DeleteEnvoyFilter deletes an EnvoyFilter by namespaced name. Returns nil if not found.
func DeleteEnvoyFilter(ctx context.Context, cl client.Client, key types.NamespacedName) error {
	ef := &unstructured.Unstructured{}
	ef.SetGroupVersionKind(GVK)

	err := cl.Get(ctx, key, ef)
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get EnvoyFilter %s: %w", key.Name, err)
	}

	if err := cl.Delete(ctx, ef); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete EnvoyFilter %s: %w", key.Name, err)
	}
	return nil
}

// CollectCatchAllEntries extracts catch-all entries from all CustomHTTPRoutes that declare catchAllRoute.
// When multiple routes declare the same hostname, the first one in lexicographic order of
// namespace/name wins, ensuring a deterministic result across reconciliations.
func CollectCatchAllEntries(routeList *v1alpha1.CustomHTTPRouteList) []CatchAllEntry {
	hostnameMap := make(map[string]v1alpha1.BackendRef)

	ordered := orderedRoutesWithCatchAll(routeList)
	for _, route := range ordered {
		for _, hostname := range route.Spec.Hostnames {
			if _, exists := hostnameMap[hostname]; exists {
				continue
			}
			hostnameMap[hostname] = route.Spec.CatchAllRoute.BackendRef
		}
	}

	return sortedEntries(hostnameMap)
}

// orderedRoutesWithCatchAll returns non-deleting routes with a non-nil catchAllRoute,
// sorted by "namespace/name" to provide a stable iteration order for dedup decisions.
func orderedRoutesWithCatchAll(routeList *v1alpha1.CustomHTTPRouteList) []*v1alpha1.CustomHTTPRoute {
	out := make([]*v1alpha1.CustomHTTPRoute, 0, len(routeList.Items))
	for i := range routeList.Items {
		route := &routeList.Items[i]
		if route.DeletionTimestamp != nil && !route.DeletionTimestamp.IsZero() {
			continue
		}
		if route.Spec.CatchAllRoute == nil {
			continue
		}
		out = append(out, route)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return routeKey(out[i]) < routeKey(out[j])
	})
	return out
}

func routeKey(route *v1alpha1.CustomHTTPRoute) string {
	return route.Namespace + "/" + route.Name
}

// MergeCatchAllEntries merges entries from CustomHTTPRoutes with the EPA's own catchAllRoute config.
// EPA entries take precedence (override) for the same hostname.
func MergeCatchAllEntries(routeEntries []CatchAllEntry, epa *v1alpha1.ExternalProcessorAttachment) []CatchAllEntry {
	merged := make(map[string]v1alpha1.BackendRef, len(routeEntries))

	for _, entry := range routeEntries {
		merged[entry.Hostname] = entry.BackendRef
	}

	if epa.Spec.CatchAllRoute != nil {
		for _, hostname := range epa.Spec.CatchAllRoute.Hostnames {
			merged[hostname] = epa.Spec.CatchAllRoute.BackendRef
		}
	}

	return sortedEntries(merged)
}

// CollectHostnamesWithHTTPRoute returns the subset of the given hostnames that are
// also declared in any existing HTTPRoute. This drives per-hostname selection between
// "ADD a virtual host" (no HTTPRoute exists) and "inject into the existing virtual host"
// (HTTPRoute exists and already owns the domain).
func CollectHostnamesWithHTTPRoute(httpRouteList *gatewayv1.HTTPRouteList, hostnames []string) map[string]bool {
	out := map[string]bool{}
	if httpRouteList == nil || len(hostnames) == 0 {
		return out
	}
	target := make(map[string]struct{}, len(hostnames))
	for _, h := range hostnames {
		target[h] = struct{}{}
	}
	for i := range httpRouteList.Items {
		hr := &httpRouteList.Items[i]
		for _, h := range hr.Spec.Hostnames {
			if _, ok := target[string(h)]; ok {
				out[string(h)] = true
			}
		}
	}
	return out
}

// BuildCatchAllEnvoyFilter builds the catch-all EnvoyFilter unstructured object.
// For each hostname the emitted patch depends on whether an HTTPRoute already owns
// the domain (see hostnamesWithHTTPRoute): if yes, HTTP_ROUTE INSERT_FIRST patches
// inject the fallback into the existing virtual host (avoids Envoy's "Duplicate entry
// of domain" error); otherwise the legacy VIRTUAL_HOST ADD creates a new virtual host.
func BuildCatchAllEnvoyFilter(
	epa *v1alpha1.ExternalProcessorAttachment,
	entries []CatchAllEntry,
	hostnamesWithHTTPRoute map[string]bool,
) (*unstructured.Unstructured, error) {
	filterName := epa.Name + CatchAllFilterSuffix

	ef := &unstructured.Unstructured{}
	ef.SetGroupVersionKind(GVK)
	ef.SetName(filterName)
	ef.SetNamespace(epa.Namespace)
	ef.SetLabels(StandardLabels(epa.Name))
	ef.SetOwnerReferences([]metav1.OwnerReference{NewOwnerReference(epa)})

	selectorInterface := SelectorToInterface(epa.Spec.GatewayRef.Selector)

	configPatches := make([]interface{}, 0, len(entries))
	for _, entry := range entries {
		for _, patch := range buildCatchAllPatches(epa, entry, hostnamesWithHTTPRoute[entry.Hostname]) {
			configPatches = append(configPatches, patch)
		}
	}

	spec := map[string]interface{}{
		"workloadSelector": map[string]interface{}{
			"labels": selectorInterface,
		},
		"configPatches": configPatches,
	}

	if err := unstructured.SetNestedField(ef.Object, spec, "spec"); err != nil {
		return nil, fmt.Errorf("failed to set spec: %w", err)
	}

	return ef, nil
}

// buildCatchAllPatches returns the config patches for one hostname. When no HTTPRoute
// owns the domain, one VIRTUAL_HOST ADD is returned. When an HTTPRoute already owns
// the domain, one HTTP_ROUTE INSERT_FIRST per port in DefaultCatchAllPorts is returned
// — Envoy would reject a second virtual host with the same domain, so the fallback is
// injected into the existing one instead.
func buildCatchAllPatches(epa *v1alpha1.ExternalProcessorAttachment, entry CatchAllEntry, hostnameHasHTTPRoute bool) []map[string]interface{} {
	if !hostnameHasHTTPRoute {
		return []map[string]interface{}{buildCatchAllVirtualHostPatch(epa, entry)}
	}
	patches := make([]map[string]interface{}, 0, len(DefaultCatchAllPorts))
	for _, port := range DefaultCatchAllPorts {
		patches = append(patches, buildCatchAllHTTPRoutePatch(epa, entry, port))
	}
	return patches
}

// buildCatchAllVirtualHostPatch builds the legacy VIRTUAL_HOST ADD patch, creating a
// new virtual host with both the header-gated dynamic route and the default fallback.
func buildCatchAllVirtualHostPatch(epa *v1alpha1.ExternalProcessorAttachment, entry CatchAllEntry) map[string]interface{} {
	clusterName := BuildClusterName(entry.BackendRef)
	timeout := GetRouteTimeout(epa)

	dynamicRoute := map[string]interface{}{
		"cluster_header": "x-customrouter-cluster",
		"timeout":        timeout,
	}
	ApplyRetryPolicy(dynamicRoute, epa)

	return map[string]interface{}{
		"applyTo": "VIRTUAL_HOST",
		"match": map[string]interface{}{
			"context": "GATEWAY",
		},
		"patch": map[string]interface{}{
			"operation": "ADD",
			"value": map[string]interface{}{
				"name":    fmt.Sprintf("customrouter-catchall-%s", entry.Hostname),
				"domains": []interface{}{entry.Hostname},
				"routes": []interface{}{
					map[string]interface{}{
						"name": "customrouter-dynamic-route",
						"match": map[string]interface{}{
							"prefix": "/",
							"headers": []interface{}{
								map[string]interface{}{
									"name":          "x-customrouter-cluster",
									"present_match": true,
								},
							},
						},
						"route": dynamicRoute,
					},
					map[string]interface{}{
						"name": "default",
						"match": map[string]interface{}{
							"prefix": "/",
						},
						"route": map[string]interface{}{
							"cluster": clusterName,
							"timeout": timeout,
						},
					},
				},
			},
		},
	}
}

// buildCatchAllHTTPRoutePatch injects only the default fallback route at the top of
// the virtual host "<hostname>:<port>" (owned by the HTTPRoute). The header-gated
// dynamic route is already injected into every virtual host by the <epa>-routes
// EnvoyFilter, so duplicating it here would be redundant.
func buildCatchAllHTTPRoutePatch(epa *v1alpha1.ExternalProcessorAttachment, entry CatchAllEntry, port int) map[string]interface{} {
	clusterName := BuildClusterName(entry.BackendRef)

	return map[string]interface{}{
		"applyTo": "HTTP_ROUTE",
		"match": map[string]interface{}{
			"context": "GATEWAY",
			"routeConfiguration": map[string]interface{}{
				"vhost": map[string]interface{}{
					"name": fmt.Sprintf("%s:%d", entry.Hostname, port),
				},
			},
		},
		"patch": map[string]interface{}{
			"operation": "INSERT_FIRST",
			"value": map[string]interface{}{
				"name": fmt.Sprintf("customrouter-catchall-%s", entry.Hostname),
				"match": map[string]interface{}{
					"prefix": "/",
				},
				"route": map[string]interface{}{
					"cluster": clusterName,
					"timeout": GetRouteTimeout(epa),
				},
			},
		},
	}
}

// CatchAllProgrammedStatus describes whether a CustomHTTPRoute's catchAllRoute ends up
// applied on at least one EPA's catch-all EnvoyFilter, and if not, which reason prevails.
type CatchAllProgrammedStatus struct {
	Programmed bool
	Reason     string
	Hostnames  []string // hostnames of the route that won both dedup and EPA override (when Programmed=true)
}

// EvaluateCatchAllProgrammed determines the programming state of a route's catchAllRoute.
// The result's Reason is one of the ConditionReasonCatchAll* constants from the controller package.
func EvaluateCatchAllProgrammed(
	route *v1alpha1.CustomHTTPRoute,
	routeList *v1alpha1.CustomHTTPRouteList,
	epaList *v1alpha1.ExternalProcessorAttachmentList,
) CatchAllProgrammedStatus {
	if route == nil || route.Spec.CatchAllRoute == nil {
		return CatchAllProgrammedStatus{Reason: controller.ConditionReasonCatchAllNotConfigured}
	}

	if epaList == nil || len(epaList.Items) == 0 {
		return CatchAllProgrammedStatus{Reason: controller.ConditionReasonCatchAllNoEPA}
	}

	selfKey := routeKey(route)
	var wonHostnames []string
	for _, hostname := range route.Spec.Hostnames {
		if winnerHostnameRoute(hostname, routeList) == selfKey {
			wonHostnames = append(wonHostnames, hostname)
		}
	}

	var programmed, lostByEPA []string
	for _, hostname := range wonHostnames {
		// A hostname is lost only if every EPA overrides it, because each EPA produces
		// its own catch-all EnvoyFilter: the route's catch-all still reaches the dataplane
		// through any EPA that does not declare the hostname in its own catchAllRoute.
		if hostnameOverriddenByEveryEPA(hostname, epaList) {
			lostByEPA = append(lostByEPA, hostname)
		} else {
			programmed = append(programmed, hostname)
		}
	}

	if len(programmed) > 0 {
		return CatchAllProgrammedStatus{
			Programmed: true,
			Reason:     controller.ConditionReasonCatchAllProgrammed,
			Hostnames:  programmed,
		}
	}

	if len(lostByEPA) > 0 {
		return CatchAllProgrammedStatus{Reason: controller.ConditionReasonCatchAllOverriddenByEPA}
	}
	return CatchAllProgrammedStatus{Reason: controller.ConditionReasonCatchAllOverriddenByRoute}
}

// winnerHostnameRoute returns the namespace/name of the route that wins the dedup for hostname,
// or "" if no route declares it. The winner is the first in lexicographic order of namespace/name.
func winnerHostnameRoute(hostname string, routeList *v1alpha1.CustomHTTPRouteList) string {
	if routeList == nil {
		return ""
	}
	ordered := orderedRoutesWithCatchAll(routeList)
	for _, r := range ordered {
		for _, h := range r.Spec.Hostnames {
			if h == hostname {
				return routeKey(r)
			}
		}
	}
	return ""
}

// hostnameOverriddenByEveryEPA reports whether every EPA declares hostname in its own
// catchAllRoute.Hostnames. Returns false if any EPA has no catchAllRoute or does not declare
// the hostname, because that EPA will carry the route's catch-all through to the dataplane.
func hostnameOverriddenByEveryEPA(hostname string, epaList *v1alpha1.ExternalProcessorAttachmentList) bool {
	if epaList == nil || len(epaList.Items) == 0 {
		return false
	}
	for i := range epaList.Items {
		epa := &epaList.Items[i]
		if epa.Spec.CatchAllRoute == nil {
			return false
		}
		found := false
		for _, h := range epa.Spec.CatchAllRoute.Hostnames {
			if h == hostname {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// sortedEntries converts a hostname→BackendRef map to a sorted slice of CatchAllEntry.
func sortedEntries(m map[string]v1alpha1.BackendRef) []CatchAllEntry {
	entries := make([]CatchAllEntry, 0, len(m))
	for hostname, ref := range m {
		entries = append(entries, CatchAllEntry{Hostname: hostname, BackendRef: ref})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Hostname < entries[j].Hostname
	})
	return entries
}
