/*
Copyright 2024.

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
)

// ReconcileObject handles the reconciliation logic for CustomHTTPRoute resources.
// It rebuilds the ConfigMaps with all routes whenever a CustomHTTPRoute is created, updated, or deleted.
func (r *CustomHTTPRouteReconciler) ReconcileObject(
	ctx context.Context,
	eventType watch.EventType,
	resourceManifest *v1alpha1.CustomHTTPRoute,
) error {
	logger := log.FromContext(ctx)

	switch eventType {
	case watch.Modified:
		logger.Info("CustomHTTPRoute modified, rebuilding routes ConfigMaps",
			"name", resourceManifest.Name,
			"namespace", resourceManifest.Namespace,
			"target", resourceManifest.Spec.TargetRef.Name)
	case watch.Deleted:
		logger.Info("CustomHTTPRoute deleted, rebuilding routes ConfigMaps",
			"name", resourceManifest.Name,
			"namespace", resourceManifest.Namespace,
			"target", resourceManifest.Spec.TargetRef.Name)
	}

	return r.rebuildAllConfigMaps(ctx)
}

// rebuildAllConfigMaps fetches all CustomHTTPRoutes, groups them by target, and rebuilds ConfigMaps
func (r *CustomHTTPRouteReconciler) rebuildAllConfigMaps(ctx context.Context) error {
	logger := log.FromContext(ctx)

	// List all CustomHTTPRoutes in the cluster
	routeList := &v1alpha1.CustomHTTPRouteList{}
	if err := r.List(ctx, routeList); err != nil {
		return fmt.Errorf("failed to list CustomHTTPRoutes: %w", err)
	}

	// Group routes by target
	routesByTarget := make(map[string][]*v1alpha1.CustomHTTPRoute)
	for i := range routeList.Items {
		route := &routeList.Items[i]
		// Skip routes that are being deleted
		if !route.DeletionTimestamp.IsZero() {
			continue
		}
		target := route.Spec.TargetRef.Name
		routesByTarget[target] = append(routesByTarget[target], route)
	}

	// Track all active ConfigMap names across all targets
	allActiveNames := make(map[string]bool)

	// Process each target
	for target, targetRoutes := range routesByTarget {
		// Build hostname-to-namespace ownership map.
		// Each hostname is owned by the namespace that appears first alphabetically
		// among all CustomHTTPRoutes that declare it. Routes from non-owning
		// namespaces are dropped to prevent cross-namespace priority hijacking.
		hostnameOwner := make(map[string]string)
		for _, route := range targetRoutes {
			for _, hostname := range route.Spec.Hostnames {
				if owner, exists := hostnameOwner[hostname]; !exists || route.Namespace < owner {
					hostnameOwner[hostname] = route.Namespace
				}
			}
		}

		// Expand routes from all CustomHTTPRoutes for this target
		allRoutes := make([]map[string][]routes.Route, 0, len(targetRoutes))
		for _, route := range targetRoutes {
			expanded, err := routes.ExpandRoutes(route)
			if err != nil {
				logger.Error(err, "skipping CustomHTTPRoute due to route expansion limit",
					"name", route.Name,
					"namespace", route.Namespace,
					"target", target)
				continue
			}

			// Filter out hostnames not owned by this route's namespace
			for hostname := range expanded {
				if hostnameOwner[hostname] != route.Namespace {
					logger.Info("dropping routes for hostname from non-owning namespace",
						"hostname", hostname,
						"routeNamespace", route.Namespace,
						"ownerNamespace", hostnameOwner[hostname],
						"routeName", route.Name,
						"target", target)
					delete(expanded, hostname)
				}
			}

			allRoutes = append(allRoutes, expanded)
		}

		// Merge all routes into a single config
		config := routes.MergeRoutesConfig(allRoutes...)

		// Partition the config into multiple ConfigMaps if needed
		partitions := r.partitionConfig(target, config)

		// Create or update the ConfigMaps for this target
		if err := r.upsertConfigMaps(ctx, target, partitions); err != nil {
			return fmt.Errorf("failed to upsert ConfigMaps for target %s: %w", target, err)
		}

		// Track active names
		for _, p := range partitions {
			allActiveNames[p.Name] = true
		}

		logger.Info("ConfigMaps updated successfully",
			"target", target,
			"namespace", r.ConfigMapNamespace,
			"hostsCount", len(config.Hosts),
			"partitions", len(partitions))
	}

	// Delete stale ConfigMaps (from targets that no longer have routes)
	if err := r.deleteStaleConfigMaps(ctx, allActiveNames); err != nil {
		return err
	}

	// Reconcile catch-all EnvoyFilter from aggregated catchAllRoute declarations
	if err := r.reconcileCatchAllFromRoutes(ctx, routeList); err != nil {
		return fmt.Errorf("failed to reconcile catch-all routes: %w", err)
	}

	return nil
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

	currentRoutes := make([]routes.Route, 0)
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
	target string,
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

	return retry.RetryOnConflict(backoff, func() error {
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
					"routes.json": partition.Data,
				},
			}
			return r.Create(ctx, cm)
		}

		if err != nil {
			return err
		}

		existingCM.Labels = configMapLabels
		existingCM.Data = map[string]string{
			"routes.json": partition.Data,
		}
		return r.Update(ctx, existingCM)
	})
}

// deleteStaleConfigMaps removes ConfigMaps that are no longer needed
func (r *CustomHTTPRouteReconciler) deleteStaleConfigMaps(
	ctx context.Context,
	activeNames map[string]bool,
) error {
	// List all ConfigMaps managed by this controller
	configMapList := &corev1.ConfigMapList{}
	labelSelector := labels.SelectorFromSet(map[string]string{
		configMapManagedByLabel: configMapManagedByValue,
	})

	if err := r.List(ctx, configMapList, &client.ListOptions{
		Namespace:     r.ConfigMapNamespace,
		LabelSelector: labelSelector,
	}); err != nil {
		return fmt.Errorf("failed to list ConfigMaps: %w", err)
	}

	// Delete ConfigMaps that are not in the active set
	for i := range configMapList.Items {
		cm := &configMapList.Items[i]
		// Only delete if it starts with our base name (to avoid deleting unrelated ConfigMaps)
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
