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

package externalprocessorattachment

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	ef "github.com/freepik-company/customrouter/internal/controller/envoyfilter"
)

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
		hostnames := make([]string, 0, len(mergedEntries))
		for _, e := range mergedEntries {
			hostnames = append(hostnames, e.Hostname)
		}
		httpRouteList := &gatewayv1.HTTPRouteList{}
		if err := r.List(ctx, httpRouteList); err != nil {
			return fmt.Errorf("failed to list HTTPRoutes: %w", err)
		}
		hostnamesWithHTTPRoute := ef.CollectHostnamesWithHTTPRoute(httpRouteList, hostnames)

		envoyFilter, err := ef.BuildCatchAllEnvoyFilter(attachment, mergedEntries, hostnamesWithHTTPRoute)
		if err != nil {
			return fmt.Errorf("failed to build catch-all EnvoyFilter: %w", err)
		}
		if err := ef.UpsertUnstructured(ctx, r.Client, envoyFilter); err != nil {
			return fmt.Errorf("failed to reconcile catch-all EnvoyFilter: %w", err)
		}
	} else {
		key := types.NamespacedName{
			Name:      attachment.Name + ef.CatchAllFilterSuffix,
			Namespace: attachment.Namespace,
		}
		if err := ef.DeleteEnvoyFilter(ctx, r.Client, key); err != nil {
			return fmt.Errorf("failed to delete catch-all EnvoyFilter: %w", err)
		}
	}

	mirrorEntries := r.collectMirrorEntries(ctx)
	if len(mirrorEntries) > 0 {
		envoyFilter, err := ef.BuildMirrorEnvoyFilter(attachment, mirrorEntries)
		if err != nil {
			return fmt.Errorf("failed to build mirror EnvoyFilter: %w", err)
		}
		if err := ef.UpsertUnstructured(ctx, r.Client, envoyFilter); err != nil {
			return fmt.Errorf("failed to reconcile mirror EnvoyFilter: %w", err)
		}
	} else {
		key := types.NamespacedName{
			Name:      attachment.Name + ef.MirrorFilterSuffix,
			Namespace: attachment.Namespace,
		}
		if err := ef.DeleteEnvoyFilter(ctx, r.Client, key); err != nil {
			return fmt.Errorf("failed to delete mirror EnvoyFilter: %w", err)
		}
	}

	corsEntries := r.collectCORSEntries(ctx)
	if len(corsEntries) > 0 {
		envoyFilter, err := ef.BuildCORSEnvoyFilter(attachment, corsEntries)
		if err != nil {
			return fmt.Errorf("failed to build CORS EnvoyFilter: %w", err)
		}
		if err := ef.UpsertUnstructured(ctx, r.Client, envoyFilter); err != nil {
			return fmt.Errorf("failed to reconcile CORS EnvoyFilter: %w", err)
		}
	} else {
		key := types.NamespacedName{
			Name:      attachment.Name + ef.CORSFilterSuffix,
			Namespace: attachment.Namespace,
		}
		if err := ef.DeleteEnvoyFilter(ctx, r.Client, key); err != nil {
			return fmt.Errorf("failed to delete CORS EnvoyFilter: %w", err)
		}
	}

	logger.Info("EnvoyFilters reconciled successfully",
		"extproc", attachment.Name+ef.ExtProcFilterSuffix,
		"routes", attachment.Name+ef.RoutesFilterSuffix,
		"catchallHostnames", len(mergedEntries),
		"mirrorEntries", len(mirrorEntries),
		"corsEntries", len(corsEntries))

	return nil
}

// collectMirrorEntries lists every CustomHTTPRoute and extracts the request-mirror
// targets across all of them. Follows the same "list then aggregate" pattern as
// collectMergedCatchAllEntries so behaviour between the two EnvoyFilters is
// consistent.
func (r *ExternalProcessorAttachmentReconciler) collectMirrorEntries(
	ctx context.Context,
) []ef.MirrorEntry {
	logger := log.FromContext(ctx)

	routeList := &v1alpha1.CustomHTTPRouteList{}
	if err := r.List(ctx, routeList); err != nil {
		logger.Error(err, "Failed to list CustomHTTPRoutes for mirror aggregation")
		return nil
	}

	return ef.CollectMirrorEntries(routeList)
}

// collectCORSEntries mirrors collectMirrorEntries for CORS actions.
func (r *ExternalProcessorAttachmentReconciler) collectCORSEntries(
	ctx context.Context,
) []ef.CORSEntry {
	logger := log.FromContext(ctx)

	routeList := &v1alpha1.CustomHTTPRouteList{}
	if err := r.List(ctx, routeList); err != nil {
		logger.Error(err, "Failed to list CustomHTTPRoutes for CORS aggregation")
		return nil
	}

	return ef.CollectCORSEntries(routeList)
}

// collectMergedCatchAllEntries lists CustomHTTPRoutes, collects their catchAllRoute entries,
// and merges with the EPA's own catchAllRoute config. EPA entries override route entries for same hostname.
func (r *ExternalProcessorAttachmentReconciler) collectMergedCatchAllEntries(
	ctx context.Context,
	attachment *v1alpha1.ExternalProcessorAttachment,
) []ef.CatchAllEntry {
	logger := log.FromContext(ctx)

	routeList := &v1alpha1.CustomHTTPRouteList{}
	if err := r.List(ctx, routeList); err != nil {
		logger.Error(err, "Failed to list CustomHTTPRoutes for catch-all aggregation")
		return nil
	}

	entries := ef.CollectCatchAllEntries(routeList)
	return ef.MergeCatchAllEntries(entries, attachment)
}

// reconcileExtProcEnvoyFilter creates or updates the ext_proc EnvoyFilter
func (r *ExternalProcessorAttachmentReconciler) reconcileExtProcEnvoyFilter(
	ctx context.Context,
	attachment *v1alpha1.ExternalProcessorAttachment,
) error {
	filterName := attachment.Name + ef.ExtProcFilterSuffix
	svcRef := attachment.Spec.ExternalProcessorRef.Service

	clusterName := fmt.Sprintf("outbound|%d||%s.%s.svc.cluster.local",
		svcRef.Port, svcRef.Name, svcRef.Namespace)

	envoyFilter := &unstructured.Unstructured{}
	envoyFilter.SetGroupVersionKind(ef.GVK)
	envoyFilter.SetName(filterName)
	envoyFilter.SetNamespace(attachment.Namespace)
	envoyFilter.SetLabels(ef.StandardLabels(attachment.Name))
	envoyFilter.SetOwnerReferences([]metav1.OwnerReference{ef.NewOwnerReference(attachment)})

	selectorInterface := ef.SelectorToInterface(attachment.Spec.GatewayRef.Selector)

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
							"failure_mode_allow": attachment.Spec.ExternalProcessorRef.FailureModeAllow,
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

	if err := unstructured.SetNestedField(envoyFilter.Object, spec, "spec"); err != nil {
		return fmt.Errorf("failed to set spec: %w", err)
	}

	return ef.UpsertUnstructured(ctx, r.Client, envoyFilter)
}

// reconcileRoutesEnvoyFilter creates or updates the routes EnvoyFilter
func (r *ExternalProcessorAttachmentReconciler) reconcileRoutesEnvoyFilter(
	ctx context.Context,
	attachment *v1alpha1.ExternalProcessorAttachment,
) error {
	filterName := attachment.Name + ef.RoutesFilterSuffix

	envoyFilter := &unstructured.Unstructured{}
	envoyFilter.SetGroupVersionKind(ef.GVK)
	envoyFilter.SetName(filterName)
	envoyFilter.SetNamespace(attachment.Namespace)
	envoyFilter.SetLabels(ef.StandardLabels(attachment.Name))
	envoyFilter.SetOwnerReferences([]metav1.OwnerReference{ef.NewOwnerReference(attachment)})

	selectorInterface := ef.SelectorToInterface(attachment.Spec.GatewayRef.Selector)

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
						"route": buildRoutesRouteAction(attachment),
					},
				},
			},
		},
	}

	if err := unstructured.SetNestedField(envoyFilter.Object, spec, "spec"); err != nil {
		return fmt.Errorf("failed to set spec: %w", err)
	}

	return ef.UpsertUnstructured(ctx, r.Client, envoyFilter)
}

// buildRoutesRouteAction builds the "route" stanza emitted into the routes EnvoyFilter,
// applying the per-EPA timeout and (optionally) retry_policy. Kept here so the
// inline spec stays readable.
func buildRoutesRouteAction(attachment *v1alpha1.ExternalProcessorAttachment) map[string]interface{} {
	routeAction := map[string]interface{}{
		"cluster_header": "x-customrouter-cluster",
		"timeout":        ef.GetRouteTimeout(attachment),
	}
	ef.ApplyRetryPolicy(routeAction, attachment)
	return routeAction
}

// deleteEnvoyFilters deletes all EnvoyFilters owned by this attachment
func (r *ExternalProcessorAttachmentReconciler) deleteEnvoyFilters(
	ctx context.Context,
	attachment *v1alpha1.ExternalProcessorAttachment,
) error {
	suffixes := []string{
		ef.ExtProcFilterSuffix,
		ef.RoutesFilterSuffix,
		ef.CatchAllFilterSuffix,
		ef.MirrorFilterSuffix,
		ef.CORSFilterSuffix,
	}

	for _, suffix := range suffixes {
		key := types.NamespacedName{
			Name:      attachment.Name + suffix,
			Namespace: attachment.Namespace,
		}
		if err := ef.DeleteEnvoyFilter(ctx, r.Client, key); err != nil {
			return err
		}
	}

	return nil
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
