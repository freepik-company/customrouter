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
	extProcFilterSuffix = "-extproc"
	routesFilterSuffix  = "-routes"
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

	logger.Info("EnvoyFilters reconciled successfully",
		"extproc", attachment.Name+extProcFilterSuffix,
		"routes", attachment.Name+routesFilterSuffix)

	return nil
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
								"timeout": "1s",
							},
							"failure_mode_allow": false,
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

	return nil
}

func boolPtr(b bool) *bool {
	return &b
}
