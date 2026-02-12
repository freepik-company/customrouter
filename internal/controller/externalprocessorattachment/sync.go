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

package externalprocessorattachment

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
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

const (
	// Labels for managed EnvoyFilters
	envoyFilterManagedByLabel = "app.kubernetes.io/managed-by"
	envoyFilterManagedByValue = "customrouter-controller"
	envoyFilterOwnerLabel     = "customrouter.freepik.com/attachment"

	// EnvoyFilter name suffixes
	extProcFilterSuffix  = "-extproc"
	routesFilterSuffix   = "-routes"
	catchAllFilterSuffix = "-catchall"
)

var envoyFilterGVK = schema.GroupVersionKind{
	Group:   "networking.istio.io",
	Version: "v1alpha3",
	Kind:    "EnvoyFilter",
}

// reconcileEnvoyFilters creates or updates the EnvoyFilters for this attachment
func (r *ExternalProcessorAttachmentReconciler) reconcileEnvoyFilters(
	ctx context.Context,
	attachment *v1alpha1.ExternalProcessorAttachment,
) error {
	logger := log.FromContext(ctx)

	// Create ext_proc EnvoyFilter
	if err := r.reconcileExtProcEnvoyFilter(ctx, attachment); err != nil {
		return fmt.Errorf("failed to reconcile ext_proc EnvoyFilter: %w", err)
	}

	// Create routes EnvoyFilter
	if err := r.reconcileRoutesEnvoyFilter(ctx, attachment); err != nil {
		return fmt.Errorf("failed to reconcile routes EnvoyFilter: %w", err)
	}

	// Collect catch-all entries from CustomHTTPRoutes and merge with EPA config
	mergedEntries := r.collectMergedCatchAllEntries(ctx, attachment)

	if len(mergedEntries) > 0 {
		if err := r.reconcileCatchAllEnvoyFilter(ctx, attachment, mergedEntries); err != nil {
			return fmt.Errorf("failed to reconcile catch-all EnvoyFilter: %w", err)
		}
		logger.Info("EnvoyFilters reconciled successfully",
			"extproc", attachment.Name+extProcFilterSuffix,
			"routes", attachment.Name+routesFilterSuffix,
			"catchall", attachment.Name+catchAllFilterSuffix,
			"catchallHostnames", len(mergedEntries))
	} else {
		// Delete catch-all EnvoyFilter if it exists but is no longer needed
		if err := r.deleteCatchAllEnvoyFilter(ctx, attachment); err != nil {
			return fmt.Errorf("failed to delete catch-all EnvoyFilter: %w", err)
		}
		logger.Info("EnvoyFilters reconciled successfully",
			"extproc", attachment.Name+extProcFilterSuffix,
			"routes", attachment.Name+routesFilterSuffix)
	}

	return nil
}

// catchAllEntry represents a hostname with its default backend
type catchAllEntry struct {
	hostname   string
	backendRef v1alpha1.BackendRef
}

// collectMergedCatchAllEntries lists CustomHTTPRoutes, collects their catchAllRoute entries,
// and merges with the EPA's own catchAllRoute config. EPA entries override route entries for same hostname.
func (r *ExternalProcessorAttachmentReconciler) collectMergedCatchAllEntries(
	ctx context.Context,
	attachment *v1alpha1.ExternalProcessorAttachment,
) []catchAllEntry {
	logger := log.FromContext(ctx)

	merged := make(map[string]v1alpha1.BackendRef)

	// Collect from CustomHTTPRoutes
	routeList := &v1alpha1.CustomHTTPRouteList{}
	if err := r.List(ctx, routeList); err != nil {
		logger.Error(err, "Failed to list CustomHTTPRoutes for catch-all aggregation")
	} else {
		for i := range routeList.Items {
			route := &routeList.Items[i]
			if route.DeletionTimestamp != nil && !route.DeletionTimestamp.IsZero() {
				continue
			}
			if route.Spec.CatchAllRoute == nil {
				continue
			}
			for _, hostname := range route.Spec.Hostnames {
				merged[hostname] = route.Spec.CatchAllRoute.BackendRef
			}
		}
	}

	// EPA's own catchAllRoute overrides
	if attachment.Spec.CatchAllRoute != nil {
		for _, hostname := range attachment.Spec.CatchAllRoute.Hostnames {
			merged[hostname] = attachment.Spec.CatchAllRoute.BackendRef
		}
	}

	entries := make([]catchAllEntry, 0, len(merged))
	for hostname, backendRef := range merged {
		entries = append(entries, catchAllEntry{
			hostname:   hostname,
			backendRef: backendRef,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].hostname < entries[j].hostname
	})

	return entries
}

// reconcileExtProcEnvoyFilter creates or updates the ext_proc EnvoyFilter
func (r *ExternalProcessorAttachmentReconciler) reconcileExtProcEnvoyFilter(
	ctx context.Context,
	attachment *v1alpha1.ExternalProcessorAttachment,
) error {
	filterName := attachment.Name + extProcFilterSuffix
	svcRef := attachment.Spec.ExternalProcessorRef.Service

	// Build Istio cluster name
	clusterName := fmt.Sprintf("outbound|%d||%s.%s.svc.cluster.local",
		svcRef.Port, svcRef.Name, svcRef.Namespace)

	// Build EnvoyFilter as unstructured
	ef := &unstructured.Unstructured{}
	ef.SetGroupVersionKind(envoyFilterGVK)
	ef.SetName(filterName)
	ef.SetNamespace(attachment.Namespace)
	ef.SetLabels(map[string]string{
		"app.kubernetes.io/name":  "customrouter",
		envoyFilterManagedByLabel: envoyFilterManagedByValue,
		envoyFilterOwnerLabel:     attachment.Name,
	})
	ef.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: attachment.APIVersion,
			Kind:       attachment.Kind,
			Name:       attachment.Name,
			UID:        attachment.UID,
			Controller: boolPtr(true),
		},
	})

	// Convert selector map[string]string to map[string]interface{}
	selectorInterface := make(map[string]interface{})
	for k, v := range attachment.Spec.GatewayRef.Selector {
		selectorInterface[k] = v
	}

	spec := map[string]interface{}{
		"workloadSelector": map[string]interface{}{
			"labels": selectorInterface,
		},
		"configPatches": []interface{}{
			map[string]interface{}{
				"applyTo": "HTTP_FILTER",
				"match": map[string]interface{}{
					"context": "GATEWAY",
					"listener": map[string]interface{}{
						"filterChain": map[string]interface{}{
							"filter": map[string]interface{}{
								"name": "envoy.filters.network.http_connection_manager",
								"subFilter": map[string]interface{}{
									"name": "envoy.filters.http.router",
								},
							},
						},
					},
				},
				"patch": map[string]interface{}{
					"operation": "INSERT_BEFORE",
					"value": map[string]interface{}{
						"name": "envoy.filters.http.ext_proc",
						"typed_config": map[string]interface{}{
							"@type": "type.googleapis.com/envoy.extensions.filters.http.ext_proc.v3.ExternalProcessor",
							"grpc_service": map[string]interface{}{
								"envoy_grpc": map[string]interface{}{
									"cluster_name": clusterName,
								},
								"timeout": getTimeout(attachment),
							},
							"failure_mode_allow": false,
							"message_timeout":    getMessageTimeout(attachment),
							"processing_mode": map[string]interface{}{
								"request_header_mode":   "SEND",
								"response_header_mode":  "SKIP",
								"request_body_mode":     "NONE",
								"response_body_mode":    "NONE",
								"request_trailer_mode":  "SKIP",
								"response_trailer_mode": "SKIP",
							},
							"mutation_rules": map[string]interface{}{
								"allow_all_routing": true,
								"allow_envoy":       false,
							},
						},
					},
				},
			},
		},
	}

	if err := unstructured.SetNestedField(ef.Object, spec, "spec"); err != nil {
		return fmt.Errorf("failed to set spec: %w", err)
	}

	return r.upsertUnstructured(ctx, ef)
}

// reconcileRoutesEnvoyFilter creates or updates the routes EnvoyFilter
func (r *ExternalProcessorAttachmentReconciler) reconcileRoutesEnvoyFilter(
	ctx context.Context,
	attachment *v1alpha1.ExternalProcessorAttachment,
) error {
	filterName := attachment.Name + routesFilterSuffix

	// Build EnvoyFilter as unstructured
	ef := &unstructured.Unstructured{}
	ef.SetGroupVersionKind(envoyFilterGVK)
	ef.SetName(filterName)
	ef.SetNamespace(attachment.Namespace)
	ef.SetLabels(map[string]string{
		"app.kubernetes.io/name":  "customrouter",
		envoyFilterManagedByLabel: envoyFilterManagedByValue,
		envoyFilterOwnerLabel:     attachment.Name,
	})
	ef.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: attachment.APIVersion,
			Kind:       attachment.Kind,
			Name:       attachment.Name,
			UID:        attachment.UID,
			Controller: boolPtr(true),
		},
	})

	// Convert selector map[string]string to map[string]interface{}
	selectorInterface := make(map[string]interface{})
	for k, v := range attachment.Spec.GatewayRef.Selector {
		selectorInterface[k] = v
	}

	spec := map[string]interface{}{
		"workloadSelector": map[string]interface{}{
			"labels": selectorInterface,
		},
		"configPatches": []interface{}{
			map[string]interface{}{
				"applyTo": "HTTP_ROUTE",
				"match": map[string]interface{}{
					"context":            "GATEWAY",
					"routeConfiguration": map[string]interface{}{},
				},
				"patch": map[string]interface{}{
					"operation": "INSERT_FIRST",
					"value": map[string]interface{}{
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
				},
			},
		},
	}

	if err := unstructured.SetNestedField(ef.Object, spec, "spec"); err != nil {
		return fmt.Errorf("failed to set spec: %w", err)
	}

	return r.upsertUnstructured(ctx, ef)
}

// reconcileCatchAllEnvoyFilter creates or updates the catch-all EnvoyFilter
// using the merged catch-all entries from both EPA config and CustomHTTPRoutes.
func (r *ExternalProcessorAttachmentReconciler) reconcileCatchAllEnvoyFilter(
	ctx context.Context,
	attachment *v1alpha1.ExternalProcessorAttachment,
	entries []catchAllEntry,
) error {
	filterName := attachment.Name + catchAllFilterSuffix

	// Build EnvoyFilter as unstructured
	ef := &unstructured.Unstructured{}
	ef.SetGroupVersionKind(envoyFilterGVK)
	ef.SetName(filterName)
	ef.SetNamespace(attachment.Namespace)
	ef.SetLabels(map[string]string{
		"app.kubernetes.io/name":  "customrouter",
		envoyFilterManagedByLabel: envoyFilterManagedByValue,
		envoyFilterOwnerLabel:     attachment.Name,
	})
	ef.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: attachment.APIVersion,
			Kind:       attachment.Kind,
			Name:       attachment.Name,
			UID:        attachment.UID,
			Controller: boolPtr(true),
		},
	})

	// Convert selector map[string]string to map[string]interface{}
	selectorInterface := make(map[string]interface{})
	for k, v := range attachment.Spec.GatewayRef.Selector {
		selectorInterface[k] = v
	}

	// Build config patches - one VIRTUAL_HOST patch per hostname
	configPatches := make([]interface{}, 0, len(entries))
	for _, entry := range entries {
		clusterName := buildCatchAllClusterName(entry.backendRef)

		patch := map[string]interface{}{
			"applyTo": "VIRTUAL_HOST",
			"match": map[string]interface{}{
				"context": "GATEWAY",
			},
			"patch": map[string]interface{}{
				"operation": "ADD",
				"value": map[string]interface{}{
					"name":    fmt.Sprintf("customrouter-catchall-%s", entry.hostname),
					"domains": []interface{}{entry.hostname},
					"routes": []interface{}{
						// Dynamic route - matches when ext_proc sets the cluster header
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
						// Default fallback route
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
func (r *ExternalProcessorAttachmentReconciler) deleteCatchAllEnvoyFilter(
	ctx context.Context,
	attachment *v1alpha1.ExternalProcessorAttachment,
) error {
	logger := log.FromContext(ctx)

	catchAllFilter := &unstructured.Unstructured{}
	catchAllFilter.SetGroupVersionKind(envoyFilterGVK)
	catchAllKey := types.NamespacedName{
		Name:      attachment.Name + catchAllFilterSuffix,
		Namespace: attachment.Namespace,
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
	logger.Info("Deleted catch-all EnvoyFilter", "name", catchAllKey.Name)

	return nil
}

// upsertUnstructured creates or updates an unstructured object
func (r *ExternalProcessorAttachmentReconciler) upsertUnstructured(
	ctx context.Context,
	obj *unstructured.Unstructured,
) error {
	// Try to get existing object
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

	// Update: preserve resourceVersion
	obj.SetResourceVersion(existing.GetResourceVersion())
	return r.Update(ctx, obj)
}

// deleteEnvoyFilters deletes all EnvoyFilters owned by this attachment
func (r *ExternalProcessorAttachmentReconciler) deleteEnvoyFilters(
	ctx context.Context,
	attachment *v1alpha1.ExternalProcessorAttachment,
) error {
	logger := log.FromContext(ctx)

	// Delete ext_proc EnvoyFilter
	extProcFilter := &unstructured.Unstructured{}
	extProcFilter.SetGroupVersionKind(envoyFilterGVK)
	extProcKey := types.NamespacedName{
		Name:      attachment.Name + extProcFilterSuffix,
		Namespace: attachment.Namespace,
	}
	if err := r.Get(ctx, extProcKey, extProcFilter); err == nil {
		if err := r.Delete(ctx, extProcFilter); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete ext_proc EnvoyFilter: %w", err)
		}
		logger.Info("Deleted ext_proc EnvoyFilter", "name", extProcKey.Name)
	}

	// Delete routes EnvoyFilter
	routesFilter := &unstructured.Unstructured{}
	routesFilter.SetGroupVersionKind(envoyFilterGVK)
	routesKey := types.NamespacedName{
		Name:      attachment.Name + routesFilterSuffix,
		Namespace: attachment.Namespace,
	}
	if err := r.Get(ctx, routesKey, routesFilter); err == nil {
		if err := r.Delete(ctx, routesFilter); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete routes EnvoyFilter: %w", err)
		}
		logger.Info("Deleted routes EnvoyFilter", "name", routesKey.Name)
	}

	// Delete catch-all EnvoyFilter
	catchAllFilter := &unstructured.Unstructured{}
	catchAllFilter.SetGroupVersionKind(envoyFilterGVK)
	catchAllKey := types.NamespacedName{
		Name:      attachment.Name + catchAllFilterSuffix,
		Namespace: attachment.Namespace,
	}
	if err := r.Get(ctx, catchAllKey, catchAllFilter); err == nil {
		if err := r.Delete(ctx, catchAllFilter); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete catch-all EnvoyFilter: %w", err)
		}
		logger.Info("Deleted catch-all EnvoyFilter", "name", catchAllKey.Name)
	}

	return nil
}

func boolPtr(b bool) *bool {
	return &b
}

func buildCatchAllClusterName(ref v1alpha1.BackendRef) string {
	if strings.Contains(ref.Name, ".") {
		return fmt.Sprintf("outbound|%d||%s", ref.Port, ref.Name)
	}
	return fmt.Sprintf("outbound|%d||%s.%s.svc.cluster.local", ref.Port, ref.Name, ref.Namespace)
}

// getTimeout returns the configured timeout or the default "5s"
func getTimeout(attachment *v1alpha1.ExternalProcessorAttachment) string {
	if attachment.Spec.ExternalProcessorRef.Timeout != "" {
		return attachment.Spec.ExternalProcessorRef.Timeout
	}
	return "5s"
}

// getMessageTimeout returns the configured message timeout or the default "5s"
func getMessageTimeout(attachment *v1alpha1.ExternalProcessorAttachment) string {
	if attachment.Spec.ExternalProcessorRef.MessageTimeout != "" {
		return attachment.Spec.ExternalProcessorRef.MessageTimeout
	}
	return "5s"
}
