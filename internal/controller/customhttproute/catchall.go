/*
Copyright 2026.

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

package customhttproute

import (
	"context"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

const (
	catchAllFilterSuffix = "-catchall"

	envoyFilterManagedByLabel = "app.kubernetes.io/managed-by"
	envoyFilterManagedByValue = "customrouter-controller"
	envoyFilterOwnerLabel     = "customrouter.freepik.com/attachment"
)

var envoyFilterGVK = schema.GroupVersionKind{
	Group:   "networking.istio.io",
	Version: "v1alpha3",
	Kind:    "EnvoyFilter",
}

// CatchAllEntry represents a hostname with its default backend for catch-all routing
type CatchAllEntry struct {
	Hostname   string
	BackendRef v1alpha1.BackendRef
}

// reconcileCatchAllFromRoutes aggregates catchAllRoute declarations from all CustomHTTPRoutes,
// merges them with the EPA's own catchAllRoute, and generates the catchall EnvoyFilter.
func (r *CustomHTTPRouteReconciler) reconcileCatchAllFromRoutes(
	ctx context.Context,
	routeList *v1alpha1.CustomHTTPRouteList,
) error {
	logger := log.FromContext(ctx)

	entries := collectCatchAllEntries(routeList)

	epaList := &v1alpha1.ExternalProcessorAttachmentList{}
	if err := r.List(ctx, epaList); err != nil {
		return fmt.Errorf("failed to list ExternalProcessorAttachments: %w", err)
	}

	if len(epaList.Items) == 0 {
		if len(entries) > 0 {
			logger.Info("CustomHTTPRoutes declare catchAllRoute but no ExternalProcessorAttachment exists, skipping catchall EnvoyFilter")
		}
		return nil
	}

	for i := range epaList.Items {
		epa := &epaList.Items[i]

		merged := mergeWithEPACatchAll(entries, epa)

		if len(merged) == 0 {
			if err := r.deleteCatchAllEnvoyFilter(ctx, epa); err != nil {
				return err
			}
			continue
		}

		if err := r.reconcileCatchAllEnvoyFilter(ctx, epa, merged); err != nil {
			return fmt.Errorf("failed to reconcile catch-all EnvoyFilter for EPA %s/%s: %w",
				epa.Namespace, epa.Name, err)
		}

		logger.Info("Catch-all EnvoyFilter reconciled from CustomHTTPRoutes",
			"epa", epa.Name,
			"namespace", epa.Namespace,
			"hostnameCount", len(merged))
	}

	return nil
}

// collectCatchAllEntries extracts catch-all entries from all CustomHTTPRoutes that declare catchAllRoute
func collectCatchAllEntries(routeList *v1alpha1.CustomHTTPRouteList) []CatchAllEntry {
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

	entries := make([]CatchAllEntry, 0, len(hostnameMap))
	for hostname, backendRef := range hostnameMap {
		entries = append(entries, CatchAllEntry{
			Hostname:   hostname,
			BackendRef: backendRef,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Hostname < entries[j].Hostname
	})

	return entries
}

// mergeWithEPACatchAll merges entries from CustomHTTPRoutes with the EPA's own catchAllRoute config.
// EPA's entries take precedence (override) for the same hostname.
func mergeWithEPACatchAll(routeEntries []CatchAllEntry, epa *v1alpha1.ExternalProcessorAttachment) []CatchAllEntry {
	merged := make(map[string]v1alpha1.BackendRef)

	for _, entry := range routeEntries {
		merged[entry.Hostname] = entry.BackendRef
	}

	if epa.Spec.CatchAllRoute != nil {
		for _, hostname := range epa.Spec.CatchAllRoute.Hostnames {
			merged[hostname] = epa.Spec.CatchAllRoute.BackendRef
		}
	}

	result := make([]CatchAllEntry, 0, len(merged))
	for hostname, backendRef := range merged {
		result = append(result, CatchAllEntry{
			Hostname:   hostname,
			BackendRef: backendRef,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Hostname < result[j].Hostname
	})

	return result
}

// reconcileCatchAllEnvoyFilter creates or updates the catch-all EnvoyFilter
func (r *CustomHTTPRouteReconciler) reconcileCatchAllEnvoyFilter(
	ctx context.Context,
	epa *v1alpha1.ExternalProcessorAttachment,
	entries []CatchAllEntry,
) error {
	filterName := epa.Name + catchAllFilterSuffix

	ef := &unstructured.Unstructured{}
	ef.SetGroupVersionKind(envoyFilterGVK)
	ef.SetName(filterName)
	ef.SetNamespace(epa.Namespace)
	ef.SetLabels(map[string]string{
		"app.kubernetes.io/name":  "customrouter",
		envoyFilterManagedByLabel: envoyFilterManagedByValue,
		envoyFilterOwnerLabel:     epa.Name,
	})
	ef.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: epa.APIVersion,
			Kind:       epa.Kind,
			Name:       epa.Name,
			UID:        epa.UID,
			Controller: boolPtr(true),
		},
	})

	selectorInterface := make(map[string]interface{})
	for k, v := range epa.Spec.GatewayRef.Selector {
		selectorInterface[k] = v
	}

	configPatches := make([]interface{}, 0, len(entries))
	for _, entry := range entries {
		clusterName := fmt.Sprintf("outbound|%d||%s.%s.svc.cluster.local",
			entry.BackendRef.Port,
			entry.BackendRef.Name,
			entry.BackendRef.Namespace)

		patch := map[string]interface{}{
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
		configPatches = append(configPatches, patch)
	}

	spec := map[string]interface{}{
		"workloadSelector": map[string]interface{}{
			"labels": selectorInterface,
		},
		"configPatches": configPatches,
	}

	if err := unstructured.SetNestedField(ef.Object, spec, "spec"); err != nil {
		return fmt.Errorf("failed to set spec: %w", err)
	}

	return r.upsertUnstructured(ctx, ef)
}

// deleteCatchAllEnvoyFilter deletes the catch-all EnvoyFilter if it exists
func (r *CustomHTTPRouteReconciler) deleteCatchAllEnvoyFilter(
	ctx context.Context,
	epa *v1alpha1.ExternalProcessorAttachment,
) error {
	catchAllFilter := &unstructured.Unstructured{}
	catchAllFilter.SetGroupVersionKind(envoyFilterGVK)
	catchAllKey := types.NamespacedName{
		Name:      epa.Name + catchAllFilterSuffix,
		Namespace: epa.Namespace,
	}

	err := r.Get(ctx, catchAllKey, catchAllFilter)
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get catch-all EnvoyFilter: %w", err)
	}

	if err := r.Delete(ctx, catchAllFilter); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete catch-all EnvoyFilter: %w", err)
	}

	return nil
}

// upsertUnstructured creates or updates an unstructured object
func (r *CustomHTTPRouteReconciler) upsertUnstructured(
	ctx context.Context,
	obj *unstructured.Unstructured,
) error {
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(obj.GroupVersionKind())
	key := types.NamespacedName{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}

	err := r.Get(ctx, key, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, obj)
	}
	if err != nil {
		return err
	}

	obj.SetResourceVersion(existing.GetResourceVersion())
	return r.Update(ctx, obj)
}

func boolPtr(b bool) *bool {
	return &b
}
