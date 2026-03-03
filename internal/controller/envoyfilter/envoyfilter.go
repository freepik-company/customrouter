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

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

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
func CollectCatchAllEntries(routeList *v1alpha1.CustomHTTPRouteList) []CatchAllEntry {
	hostnameMap := make(map[string]v1alpha1.BackendRef)

	for i := range routeList.Items {
		route := &routeList.Items[i]
		if route.DeletionTimestamp != nil && !route.DeletionTimestamp.IsZero() {
			continue
		}
		if route.Spec.CatchAllRoute == nil {
			continue
		}
		for _, hostname := range route.Spec.Hostnames {
			hostnameMap[hostname] = route.Spec.CatchAllRoute.BackendRef
		}
	}

	return sortedEntries(hostnameMap)
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

// BuildCatchAllEnvoyFilter builds the catch-all EnvoyFilter unstructured object.
func BuildCatchAllEnvoyFilter(
	epa *v1alpha1.ExternalProcessorAttachment,
	entries []CatchAllEntry,
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
		configPatches = append(configPatches, buildCatchAllPatch(entry))
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

// buildCatchAllPatch builds a single VIRTUAL_HOST config patch for a catch-all entry.
func buildCatchAllPatch(entry CatchAllEntry) map[string]interface{} {
	clusterName := BuildClusterName(entry.BackendRef)

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
						"route": map[string]interface{}{
							"cluster_header": "x-customrouter-cluster",
							"timeout":        "30s",
							"retry_policy": map[string]interface{}{
								"retry_on":               "connect-failure,refused-stream,unavailable,cancelled,retriable-status-codes",
								"num_retries":            int64(2),
								"retriable_status_codes": []interface{}{int64(503)},
							},
						},
					},
					map[string]interface{}{
						"name": "default",
						"match": map[string]interface{}{
							"prefix": "/",
						},
						"route": map[string]interface{}{
							"cluster": clusterName,
							"timeout": "30s",
						},
					},
				},
			},
		},
	}
}

// sortedEntries converts a hostname→BackendRef map to a sorted slice of CatchAllEntry.
func sortedEntries(m map[string]v1alpha1.BackendRef) []CatchAllEntry {
	entries := make([]CatchAllEntry, 0, len(m))
	for hostname, ref := range m {
		entries = append(entries, CatchAllEntry{Hostname: hostname, BackendRef: ref})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Hostname < entries[j].Hostname
	})
	return entries
}
