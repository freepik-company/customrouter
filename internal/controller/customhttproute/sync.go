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

package customhttproute

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/pkg/routes"
)

const (
	// maxConfigMapSize is the maximum size for a ConfigMap data in etcd (1MB with some margin)
	maxConfigMapSize = 900 * 1024 // 900KB to leave margin for metadata

	// configMapPartLabel is the label used to identify ConfigMap partitions
	configMapPartLabel = "customrouter.freepik.com/part"

	// configMapTargetLabel is the label used to identify the target external processor
	configMapTargetLabel = "customrouter.freepik.com/target"

	// configMapManagedByLabel is the label to identify ConfigMaps managed by this controller
	configMapManagedByLabel = "app.kubernetes.io/managed-by"
	configMapManagedByValue = "customrouter-controller"

	// configMapBaseName is the base name for all route ConfigMaps
	configMapBaseName = "customrouter-routes"

	// routesDataKey is the key used in ConfigMap data for the routes JSON
	routesDataKey = "routes.json"

	// lastTargetAnnotation tracks the previous targetRef to clean up stale ConfigMaps on target changes
	lastTargetAnnotation = "customrouter.freepik.com/last-target"

	// hadCatchAllAnnotation tracks whether the route previously had catchAllRoute configured
	hadCatchAllAnnotation = "customrouter.freepik.com/had-catch-all"

	// annotationValueTrue is the canonical string value for boolean true annotations
	annotationValueTrue = "true"
)

// ReconcileObject handles the reconciliation logic for CustomHTTPRoute resources.
// It rebuilds only the ConfigMaps for the affected target, not all targets.
func (r *CustomHTTPRouteReconciler) ReconcileObject(
	ctx context.Context,
	eventType watch.EventType,
	resourceManifest *v1alpha1.CustomHTTPRoute,
) error {
	logger := log.FromContext(ctx)
	target := resourceManifest.Spec.TargetRef.Name

	switch eventType {
	case watch.Modified:
		logger.Info("CustomHTTPRoute modified, rebuilding routes ConfigMaps",
			"name", resourceManifest.Name,
			"namespace", resourceManifest.Namespace,
			"target", target)
	case watch.Deleted:
		logger.Info("CustomHTTPRoute deleted, rebuilding routes ConfigMaps",
			"name", resourceManifest.Name,
			"namespace", resourceManifest.Namespace,
			"target", target)
	}

	// If the target changed, also rebuild the old target to clean up its stale ConfigMaps
	if previousTarget, ok := resourceManifest.Annotations[lastTargetAnnotation]; ok && previousTarget != target {
		logger.Info("Target changed, also rebuilding previous target",
			"name", resourceManifest.Name,
			"previousTarget", previousTarget,
			"newTarget", target)
		if err := r.rebuildConfigMapsForTarget(ctx, previousTarget); err != nil {
			return fmt.Errorf("failed to rebuild ConfigMaps for previous target %s: %w", previousTarget, err)
		}
	}

	// Update the last-target annotation — skip for deletions as the resource is being removed
	if eventType != watch.Deleted {
		if err := r.ensureLastTargetAnnotation(ctx, resourceManifest, target); err != nil {
			return fmt.Errorf("failed to update last-target annotation: %w", err)
		}
	}

	// Rebuild ConfigMaps for the current target
	if err := r.rebuildConfigMapsForTarget(ctx, target); err != nil {
		return err
	}

	// Reconcile catch-all EnvoyFilter when:
	// - the route currently has catchAllRoute configured
	// - the route is being deleted (may have had catch-all entries)
	// - the route previously had catchAllRoute but it was removed (annotation present, field nil)
	hadCatchAll := resourceManifest.Annotations[hadCatchAllAnnotation] == annotationValueTrue
	hasCatchAll := resourceManifest.Spec.CatchAllRoute != nil
	needsCatchAllReconcile := hasCatchAll || eventType == watch.Deleted || hadCatchAll

	if needsCatchAllReconcile {
		if err := r.reconcileCatchAllFromAllRoutes(ctx); err != nil {
			return fmt.Errorf("failed to reconcile catch-all routes: %w", err)
		}
	}

	// Track whether this route has catchAllRoute for future change detection — skip for deletions
	if eventType != watch.Deleted {
		if err := r.ensureHadCatchAllAnnotation(ctx, resourceManifest, hasCatchAll); err != nil {
			return fmt.Errorf("failed to update had-catch-all annotation: %w", err)
		}
	}

	return nil
}

// ensureLastTargetAnnotation sets the last-target annotation on the resource if not already correct.
func (r *CustomHTTPRouteReconciler) ensureLastTargetAnnotation(
	ctx context.Context,
	resource *v1alpha1.CustomHTTPRoute,
	target string,
) error {
	if resource.Annotations != nil && resource.Annotations[lastTargetAnnotation] == target {
		return nil
	}
	if resource.Annotations == nil {
		resource.Annotations = make(map[string]string)
	}
	resource.Annotations[lastTargetAnnotation] = target
	return r.Update(ctx, resource)
}

// ensureHadCatchAllAnnotation sets or removes the had-catch-all annotation to track catchAllRoute presence.
func (r *CustomHTTPRouteReconciler) ensureHadCatchAllAnnotation(
	ctx context.Context,
	resource *v1alpha1.CustomHTTPRoute,
	hasCatchAll bool,
) error {
	currentValue := resource.Annotations[hadCatchAllAnnotation]
	desiredValue := ""
	if hasCatchAll {
		desiredValue = "true"
	}
	if currentValue == desiredValue {
		return nil
	}
	if resource.Annotations == nil {
		resource.Annotations = make(map[string]string)
	}
	if hasCatchAll {
		resource.Annotations[hadCatchAllAnnotation] = annotationValueTrue
	} else {
		delete(resource.Annotations, hadCatchAllAnnotation)
	}
	return r.Update(ctx, resource)
}

// reconcileCatchAllFromAllRoutes lists all routes and reconciles catch-all EnvoyFilters.
// This is only called when a route with catchAllRoute is modified or any route is deleted.
func (r *CustomHTTPRouteReconciler) reconcileCatchAllFromAllRoutes(ctx context.Context) error {
	routeList := &v1alpha1.CustomHTTPRouteList{}
	if err := r.List(ctx, routeList); err != nil {
		return fmt.Errorf("failed to list CustomHTTPRoutes for catch-all reconciliation: %w", err)
	}
	return r.reconcileCatchAllFromRoutes(ctx, routeList)
}

// rebuildConfigMapsForTarget rebuilds ConfigMaps only for a specific target
func (r *CustomHTTPRouteReconciler) rebuildConfigMapsForTarget(ctx context.Context, target string) error {
	logger := log.FromContext(ctx)

	// List only CustomHTTPRoutes for this target using the field indexer
	routeList := &v1alpha1.CustomHTTPRouteList{}
	if err := r.List(ctx, routeList, client.MatchingFields{
		targetRefIndexField: target,
	}); err != nil {
		return fmt.Errorf("failed to list CustomHTTPRoutes for target %s: %w", target, err)
	}

	// Collect active (non-deleting) routes for this target
	var targetRoutes []*v1alpha1.CustomHTTPRoute
	for i := range routeList.Items {
		route := &routeList.Items[i]
		if route.DeletionTimestamp.IsZero() {
			targetRoutes = append(targetRoutes, route)
		}
	}

	// Track active ConfigMap names for this target
	activeNames := make(map[string]bool)

	if len(targetRoutes) > 0 {
		// Pre-resolve ExternalName services for this target's routes
		externalNames := r.resolveExternalNames(ctx, targetRoutes)

		// Expand routes from all CustomHTTPRoutes for this target
		allRoutes := make([]map[string][]routes.Route, 0, len(targetRoutes))
		for _, route := range targetRoutes {
			expanded, err := routes.ExpandRoutes(route, externalNames)
			if err != nil {
				logger.Error(err, "skipping CustomHTTPRoute due to route expansion limit",
					"name", route.Name,
					"namespace", route.Namespace,
					"target", target)
				continue
			}
			allRoutes = append(allRoutes, expanded)
		}

		// Merge all routes into a single config
		config := routes.MergeRoutesConfig(allRoutes...)

		// Partition the config into multiple ConfigMaps if needed
		partitions := r.partitionConfig(target, config)

		// Create or update the ConfigMaps for this target
		if err := r.upsertConfigMaps(ctx, partitions); err != nil {
			return fmt.Errorf("failed to upsert ConfigMaps for target %s: %w", target, err)
		}

		for _, p := range partitions {
			activeNames[p.Name] = true
		}

		logger.Info("ConfigMaps updated successfully",
			"target", target,
			"namespace", r.ConfigMapNamespace,
			"hostsCount", len(config.Hosts),
			"partitions", len(partitions))
	}

	// Delete stale ConfigMaps for this target
	if err := r.deleteStaleConfigMapsForTarget(ctx, target, activeNames); err != nil {
		return err
	}

	return nil
}

// resolveExternalNames pre-resolves ExternalName services referenced by the given routes.
// Services that are not ExternalName type are marked as seen to avoid redundant lookups.
func (r *CustomHTTPRouteReconciler) resolveExternalNames(
	ctx context.Context,
	targetRoutes []*v1alpha1.CustomHTTPRoute,
) map[string]string {
	externalNames := make(map[string]string)
	seen := make(map[string]bool)

	for _, route := range targetRoutes {
		for _, rule := range route.Spec.Rules {
			for _, ref := range rule.BackendRefs {
				key := ref.Name + "/" + ref.Namespace
				if seen[key] {
					continue
				}
				seen[key] = true
				svc := &corev1.Service{}
				if err := r.Get(ctx, client.ObjectKey{Name: ref.Name, Namespace: ref.Namespace}, svc); err != nil {
					continue
				}
				if svc.Spec.Type == corev1.ServiceTypeExternalName {
					externalNames[key] = svc.Spec.ExternalName
				}
			}
		}
	}
	return externalNames
}

// partitionConfig splits the routes config into multiple partitions if it exceeds the size limit
func (r *CustomHTTPRouteReconciler) partitionConfig(
	target string,
	config *routes.RoutesConfig,
) []ConfigMapPartition {
	// Try single partition first
	data, _ := config.ToJSON()
	if len(data) <= maxConfigMapSize {
		return []ConfigMapPartition{
			{
				Name:   r.partitionName(target, 0),
				Target: target,
				Data:   string(data),
			},
		}
	}

	// Need to split by hosts
	return r.splitByHosts(target, config)
}

// ConfigMapPartition represents a single ConfigMap partition
type ConfigMapPartition struct {
	Name   string
	Target string
	Data   string
}

// splitByHosts splits the config into multiple partitions, each containing a subset of hosts
func (r *CustomHTTPRouteReconciler) splitByHosts(
	target string,
	config *routes.RoutesConfig,
) []ConfigMapPartition {
	var partitions []ConfigMapPartition

	// Sort hosts for deterministic ordering
	hosts := make([]string, 0, len(config.Hosts))
	for host := range config.Hosts {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)

	currentPartition := &routes.RoutesConfig{
		Version: config.Version,
		Hosts:   make(map[string][]routes.Route),
	}
	currentSize := 0
	partIndex := 0

	for _, host := range hosts {
		hostRoutes := config.Hosts[host]

		// Estimate size for this host
		hostConfig := &routes.RoutesConfig{
			Version: config.Version,
			Hosts:   map[string][]routes.Route{host: hostRoutes},
		}
		hostData, _ := hostConfig.ToJSON()
		hostSize := len(hostData)

		// If single host exceeds limit, we need to split the host's routes
		if hostSize > maxConfigMapSize {
			// Flush current partition if not empty
			if len(currentPartition.Hosts) > 0 {
				partData, _ := currentPartition.ToJSON()
				partitions = append(partitions, ConfigMapPartition{
					Name:   r.partitionName(target, partIndex),
					Target: target,
					Data:   string(partData),
				})
				partIndex++
				currentPartition = &routes.RoutesConfig{
					Version: config.Version,
					Hosts:   make(map[string][]routes.Route),
				}
				currentSize = 0
			}

			// Split this host's routes across multiple partitions
			hostPartitions := r.splitHostRoutes(target, host, hostRoutes, partIndex)
			partitions = append(partitions, hostPartitions...)
			partIndex += len(hostPartitions)
			continue
		}

		// Check if adding this host would exceed the limit
		if currentSize+hostSize > maxConfigMapSize && len(currentPartition.Hosts) > 0 {
			// Flush current partition
			partData, _ := currentPartition.ToJSON()
			partitions = append(partitions, ConfigMapPartition{
				Name:   r.partitionName(target, partIndex),
				Target: target,
				Data:   string(partData),
			})
			partIndex++

			// Start new partition
			currentPartition = &routes.RoutesConfig{
				Version: config.Version,
				Hosts:   make(map[string][]routes.Route),
			}
			currentSize = 0
		}

		// Add host to current partition
		currentPartition.Hosts[host] = hostRoutes
		currentSize += hostSize
	}

	// Flush remaining partition
	if len(currentPartition.Hosts) > 0 {
		partData, _ := currentPartition.ToJSON()
		partitions = append(partitions, ConfigMapPartition{
			Name:   r.partitionName(target, partIndex),
			Target: target,
			Data:   string(partData),
		})
	}

	return partitions
}

// splitHostRoutes splits a single host's routes across multiple partitions
func (r *CustomHTTPRouteReconciler) splitHostRoutes(
	target string,
	host string,
	hostRoutes []routes.Route,
	startIndex int,
) []ConfigMapPartition {
	var partitions []ConfigMapPartition
	partIndex := startIndex

	currentRoutes := make([]routes.Route, 0, len(hostRoutes))
	currentSize := 0
	baseSize := len(fmt.Sprintf(`{"version":1,"hosts":{"%s":[]}}`, host))

	for _, route := range hostRoutes {
		routeData, _ := json.Marshal(route)
		routeSize := len(routeData) + 1 // +1 for comma

		if currentSize+routeSize+baseSize > maxConfigMapSize && len(currentRoutes) > 0 {
			// Flush current routes
			partConfig := &routes.RoutesConfig{
				Version: 1,
				Hosts:   map[string][]routes.Route{host: currentRoutes},
			}
			partData, _ := partConfig.ToJSON()
			partitions = append(partitions, ConfigMapPartition{
				Name:   r.partitionName(target, partIndex),
				Target: target,
				Data:   string(partData),
			})
			partIndex++

			currentRoutes = make([]routes.Route, 0)
			currentSize = 0
		}

		currentRoutes = append(currentRoutes, route)
		currentSize += routeSize
	}

	// Flush remaining routes
	if len(currentRoutes) > 0 {
		partConfig := &routes.RoutesConfig{
			Version: 1,
			Hosts:   map[string][]routes.Route{host: currentRoutes},
		}
		partData, _ := partConfig.ToJSON()
		partitions = append(partitions, ConfigMapPartition{
			Name:   r.partitionName(target, partIndex),
			Target: target,
			Data:   string(partData),
		})
	}

	return partitions
}

// partitionName generates the name for a partition: customrouter-routes-<target>-<index>
func (r *CustomHTTPRouteReconciler) partitionName(target string, index int) string {
	return fmt.Sprintf("%s-%s-%d", configMapBaseName, target, index)
}

// upsertConfigMaps creates or updates all ConfigMap partitions for a target
func (r *CustomHTTPRouteReconciler) upsertConfigMaps(
	ctx context.Context,
	partitions []ConfigMapPartition,
) error {
	// Create or update each partition
	for _, partition := range partitions {
		if err := r.upsertSingleConfigMap(ctx, partition); err != nil {
			return err
		}
	}

	return nil
}

// upsertSingleConfigMap creates or updates a single ConfigMap with retry-on-conflict.
// Multiple CustomHTTPRoute reconciliations run concurrently and all update the same
// ConfigMaps, so conflicts are expected. We retry with a fresh Get on each attempt.
func (r *CustomHTTPRouteReconciler) upsertSingleConfigMap(
	ctx context.Context,
	partition ConfigMapPartition,
) error {
	configMapKey := types.NamespacedName{
		Name:      partition.Name,
		Namespace: r.ConfigMapNamespace,
	}

	partNumber := "0"
	parts := strings.Split(partition.Name, "-")
	if len(parts) > 0 {
		partNumber = parts[len(parts)-1]
	}

	configMapLabels := map[string]string{
		"app.kubernetes.io/name": "customrouter",
		configMapManagedByLabel:  configMapManagedByValue,
		configMapTargetLabel:     partition.Target,
		configMapPartLabel:       partNumber,
	}

	backoff := wait.Backoff{
		Steps:    5,
		Duration: 200 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.2,
	}

	return retry.OnError(backoff, func(err error) bool {
		return errors.IsConflict(err) || errors.IsAlreadyExists(err)
	}, func() error {
		existingCM := &corev1.ConfigMap{}
		err := r.Get(ctx, configMapKey, existingCM)

		if errors.IsNotFound(err) {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      partition.Name,
					Namespace: r.ConfigMapNamespace,
					Labels:    configMapLabels,
				},
				Data: map[string]string{
					routesDataKey: partition.Data,
				},
			}
			return r.Create(ctx, cm)
		}

		if err != nil {
			return err
		}

		// Skip update if content and labels are already correct
		if existingCM.Data[routesDataKey] == partition.Data &&
			mapsEqual(existingCM.Labels, configMapLabels) {
			return nil
		}

		existingCM.Labels = configMapLabels
		existingCM.Data = map[string]string{
			routesDataKey: partition.Data,
		}
		return r.Update(ctx, existingCM)
	})
}

// deleteStaleConfigMapsForTarget removes ConfigMaps for a specific target that are no longer needed
func (r *CustomHTTPRouteReconciler) deleteStaleConfigMapsForTarget(
	ctx context.Context,
	target string,
	activeNames map[string]bool,
) error {
	configMapList := &corev1.ConfigMapList{}
	labelSelector := labels.SelectorFromSet(map[string]string{
		configMapManagedByLabel: configMapManagedByValue,
		configMapTargetLabel:    target,
	})

	if err := r.List(ctx, configMapList, &client.ListOptions{
		Namespace:     r.ConfigMapNamespace,
		LabelSelector: labelSelector,
	}); err != nil {
		return fmt.Errorf("failed to list ConfigMaps for target %s: %w", target, err)
	}

	for i := range configMapList.Items {
		cm := &configMapList.Items[i]
		if !strings.HasPrefix(cm.Name, configMapBaseName) {
			continue
		}
		if !activeNames[cm.Name] {
			if err := r.Delete(ctx, cm); err != nil && !errors.IsNotFound(err) {
				return fmt.Errorf("failed to delete stale ConfigMap %s: %w", cm.Name, err)
			}
		}
	}

	return nil
}

// mapsEqual returns true if two string maps have identical keys and values.
func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
