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
	"hash/fnv"
	"sort"
	"strconv"
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
	ctrl "sigs.k8s.io/controller-runtime"
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

	// hadMirrorAnnotation tracks whether the route previously had a request-mirror action
	hadMirrorAnnotation = "customrouter.freepik.com/had-mirror"

	// hadCORSAnnotation tracks whether the route previously had a cors action
	hadCORSAnnotation = "customrouter.freepik.com/had-cors"

	// annotationValueTrue is the canonical string value for boolean true annotations
	annotationValueTrue = "true"
)

// ReconcileObject handles the reconciliation logic for CustomHTTPRoute resources.
// It rebuilds only the ConfigMaps for the affected target, not all targets.
//
// To bound the API/etcd write rate when many CustomHTTPRoutes sharing a
// target are reconciled together (typically at controller cache resync), the
// rebuild is rate-limited per target via rebuildWait. Reconciles arriving
// while a target is in cooldown return a non-zero ctrl.Result.RequeueAfter so
// the work item is reprocessed later — by which time a single concurrent
// rebuild will have incorporated every CR's current state.
func (r *CustomHTTPRouteReconciler) ReconcileObject(
	ctx context.Context,
	eventType watch.EventType,
	resourceManifest *v1alpha1.CustomHTTPRoute,
) (ctrl.Result, *v1alpha1.CustomHTTPRouteList, *v1alpha1.ExternalProcessorAttachmentList, error) {
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

	// Cooldown: cap rebuild rate per target. Skipped for deletions so that
	// stale ConfigMaps and EnvoyFilters are cleaned up promptly when a CR
	// goes away.
	if eventType != watch.Deleted {
		if remaining, throttled := r.rebuildWait(target, time.Now()); throttled {
			logger.V(1).Info("target rebuild in cooldown, requeueing",
				"target", target,
				"wait", remaining.String())
			return ctrl.Result{RequeueAfter: remaining}, nil, nil, nil
		}
	}

	// Snapshot current annotation state BEFORE any modifications. These are
	// used below to detect whether catch-all / mirror / CORS axes were
	// previously active and therefore need reconciliation even when the
	// current spec no longer declares them.
	hadCatchAll := resourceManifest.Annotations[hadCatchAllAnnotation] == annotationValueTrue
	hadMirror := resourceManifest.Annotations[hadMirrorAnnotation] == annotationValueTrue
	hadCORS := resourceManifest.Annotations[hadCORSAnnotation] == annotationValueTrue

	// If the target changed, also rebuild the old target to clean up its stale ConfigMaps
	if previousTarget, ok := resourceManifest.Annotations[lastTargetAnnotation]; ok && previousTarget != target {
		logger.Info("Target changed, also rebuilding previous target",
			"name", resourceManifest.Name,
			"previousTarget", previousTarget,
			"newTarget", target)
		if err := r.rebuildConfigMapsForTarget(ctx, previousTarget); err != nil {
			return ctrl.Result{}, nil, nil, fmt.Errorf("failed to rebuild ConfigMaps for previous target %s: %w", previousTarget, err)
		}
		r.markRebuilt(previousTarget, time.Now())
	}

	// Rebuild ConfigMaps for the current target
	if err := r.rebuildConfigMapsForTarget(ctx, target); err != nil {
		return ctrl.Result{}, nil, nil, err
	}
	r.markRebuilt(target, time.Now())

	// Reconcile catch-all / mirror / CORS EnvoyFilters when any axis is
	// active or was previously active for this route. To avoid listing
	// CustomHTTPRoutes and ExternalProcessorAttachments three separate
	// times (once per axis), list them once here and pass them into each
	// reconciler. On a controller resync against a large catalogue this
	// removes 4 redundant API/cache reads (2 axes × 2 list types) per
	// reconcile, on top of the writes already saved by the cooldown.
	hasCatchAll := resourceManifest.Spec.CatchAllRoute != nil
	hasMirror := routeHasMirrorAction(resourceManifest)
	hasCORS := routeHasCORSAction(resourceManifest)
	needCatchAll := hasCatchAll || eventType == watch.Deleted || hadCatchAll
	needMirror := hasMirror || eventType == watch.Deleted || hadMirror
	needCORS := hasCORS || eventType == watch.Deleted || hadCORS

	var routeList *v1alpha1.CustomHTTPRouteList
	var epaList *v1alpha1.ExternalProcessorAttachmentList

	if needCatchAll || needMirror || needCORS {
		routeList = &v1alpha1.CustomHTTPRouteList{}
		if err := r.List(ctx, routeList); err != nil {
			return ctrl.Result{}, nil, nil, fmt.Errorf("failed to list CustomHTTPRoutes for envoyfilter reconciliation: %w", err)
		}
		epaList = &v1alpha1.ExternalProcessorAttachmentList{}
		if err := r.List(ctx, epaList); err != nil {
			return ctrl.Result{}, nil, nil, fmt.Errorf("failed to list ExternalProcessorAttachments for envoyfilter reconciliation: %w", err)
		}

		if needCatchAll {
			if err := r.reconcileCatchAllFromRoutes(ctx, routeList, epaList); err != nil {
				return ctrl.Result{}, nil, nil, fmt.Errorf("failed to reconcile catch-all routes: %w", err)
			}
		}
		if needMirror {
			if err := r.reconcileMirrorFromRoutes(ctx, routeList, epaList); err != nil {
				return ctrl.Result{}, nil, nil, fmt.Errorf("failed to reconcile mirror routes: %w", err)
			}
		}
		if needCORS {
			if err := r.reconcileCORSFromRoutes(ctx, routeList, epaList); err != nil {
				return ctrl.Result{}, nil, nil, fmt.Errorf("failed to reconcile cors routes: %w", err)
			}
		}
	}

	// Batch-update all tracking annotations in a single API call to minimise
	// etcd writes and avoid cascading reconciles from individual updates.
	// Previously each annotation was updated separately, triggering up to 4
	// additional reconcile cycles per route change.
	if eventType != watch.Deleted {
		if err := r.ensureAnnotations(ctx, resourceManifest, target, hasCatchAll, hasMirror, hasCORS); err != nil {
			return ctrl.Result{}, nil, nil, fmt.Errorf("failed to update tracking annotations: %w", err)
		}
	}

	return ctrl.Result{}, routeList, epaList, nil
}

// routeHasCORSAction returns true if any rule in the route declares a cors action.
func routeHasCORSAction(cr *v1alpha1.CustomHTTPRoute) bool {
	for _, rule := range cr.Spec.Rules {
		for _, a := range rule.Actions {
			if a.Type == v1alpha1.ActionTypeCORS {
				return true
			}
		}
	}
	return false
}

// routeHasMirrorAction returns true if any rule in the route declares a
// request-mirror action. Kept package-local for use in the reconcile trigger.
func routeHasMirrorAction(cr *v1alpha1.CustomHTTPRoute) bool {
	for _, rule := range cr.Spec.Rules {
		for _, a := range rule.Actions {
			if a.Type == v1alpha1.ActionTypeRequestMirror {
				return true
			}
		}
	}
	return false
}

// ensureAnnotations batch-updates all tracking annotations (last-target,
// had-catch-all, had-mirror, had-cors) in a single API call. This replaces
// the previous per-annotation Update calls that each triggered a new
// reconcile via the controller watch, multiplying etcd writes.
func (r *CustomHTTPRouteReconciler) ensureAnnotations(
	ctx context.Context,
	resource *v1alpha1.CustomHTTPRoute,
	target string,
	hasCatchAll, hasMirror, hasCORS bool,
) error {
	if annotationsUpToDate(resource.Annotations, target, hasCatchAll, hasMirror, hasCORS) {
		return nil
	}

	if resource.Annotations == nil {
		resource.Annotations = make(map[string]string)
	}
	resource.Annotations[lastTargetAnnotation] = target
	setBoolAnnotation(resource.Annotations, hadCatchAllAnnotation, hasCatchAll)
	setBoolAnnotation(resource.Annotations, hadMirrorAnnotation, hasMirror)
	setBoolAnnotation(resource.Annotations, hadCORSAnnotation, hasCORS)

	return r.Update(ctx, resource)
}

// annotationsUpToDate returns true when all tracking annotations already
// reflect the desired state, so no Update call is needed.
func annotationsUpToDate(ann map[string]string, target string, hasCatchAll, hasMirror, hasCORS bool) bool {
	if ann == nil {
		return false
	}
	if ann[lastTargetAnnotation] != target {
		return false
	}
	return boolAnnotationCurrent(ann, hadCatchAllAnnotation, hasCatchAll) &&
		boolAnnotationCurrent(ann, hadMirrorAnnotation, hasMirror) &&
		boolAnnotationCurrent(ann, hadCORSAnnotation, hasCORS)
}

// boolAnnotationCurrent checks if a boolean annotation matches the desired state.
func boolAnnotationCurrent(ann map[string]string, key string, desired bool) bool {
	current, exists := ann[key]
	if desired {
		return exists && current == annotationValueTrue
	}
	return !exists || current == ""
}

// setBoolAnnotation sets or removes a boolean tracking annotation.
func setBoolAnnotation(ann map[string]string, key string, value bool) {
	if value {
		ann[key] = annotationValueTrue
	} else {
		delete(ann, key)
	}
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
		partitions, err := r.partitionConfig(target, config)
		if err != nil {
			return fmt.Errorf("failed to partition routes for target %s: %w", target, err)
		}

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

	// When all routes for this target have been removed, purge the
	// in-memory cooldown and hash-cache entries so they don't accumulate
	// as targets are created and deleted over the lifetime of the process.
	if len(targetRoutes) == 0 {
		r.clearTargetState(target)
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
) ([]ConfigMapPartition, error) {
	// Try single partition first
	data, err := config.ToJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to serialize routes for target %s: %w", target, err)
	}
	if len(data) <= maxConfigMapSize {
		return []ConfigMapPartition{
			{
				Name:   r.partitionName(target, 0),
				Target: target,
				Data:   string(data),
			},
		}, nil
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
) ([]ConfigMapPartition, error) {
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
		hostData, err := hostConfig.ToJSON()
		if err != nil {
			return nil, fmt.Errorf("failed to serialize host %s: %w", host, err)
		}
		hostSize := len(hostData)

		// If single host exceeds limit, we need to split the host's routes
		if hostSize > maxConfigMapSize {
			// Flush current partition if not empty
			if len(currentPartition.Hosts) > 0 {
				partData, err := currentPartition.ToJSON()
				if err != nil {
					return nil, fmt.Errorf("failed to serialize partition %d: %w", partIndex, err)
				}
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
			hostPartitions, nextIndex, err := r.splitHostRoutes(target, host, hostRoutes, partIndex)
			if err != nil {
				return nil, err
			}
			partitions = append(partitions, hostPartitions...)
			partIndex = nextIndex
			continue
		}

		// Check if adding this host would exceed the limit
		if currentSize+hostSize > maxConfigMapSize && len(currentPartition.Hosts) > 0 {
			// Flush current partition
			partData, err := currentPartition.ToJSON()
			if err != nil {
				return nil, fmt.Errorf("failed to serialize partition %d: %w", partIndex, err)
			}
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
		partData, err := currentPartition.ToJSON()
		if err != nil {
			return nil, fmt.Errorf("failed to serialize final partition %d: %w", partIndex, err)
		}
		partitions = append(partitions, ConfigMapPartition{
			Name:   r.partitionName(target, partIndex),
			Target: target,
			Data:   string(partData),
		})
	}

	return partitions, nil
}

// splitHostRoutes splits a single host's routes across multiple partitions.
//
// Assignment is stable: each route is mapped to a partition by hashing a key
// derived from its identity (path type, path, method, backend, header and
// query-param signatures). A single route mutation in the input therefore
// changes at most one partition's content, instead of cascading through every
// downstream partition as a greedy size-based packing would. Within each
// partition the routes are sorted so the JSON serialisation is deterministic
// for the per-partition content dedup at upsertSingleConfigMap.
//
// The bucket count is derived from the total payload size with a generous
// safety margin so small growth does not force a re-bucket. If an unlucky
// hash distribution puts a bucket over maxConfigMapSize the bucket count is
// doubled and the assignment retried.
//
// The second return value is the next partition index the caller should use
// for subsequent hosts. It is always startIndex + bucketCount even when some
// buckets are empty, so partition naming for a given bucket position stays
// stable across reconciles (an empty bucket today does not shift the index
// of its non-empty neighbours tomorrow) and downstream hosts do not collide
// on the names of the empty-bucket slots reserved here.
func (r *CustomHTTPRouteReconciler) splitHostRoutes(
	target string,
	host string,
	hostRoutes []routes.Route,
	startIndex int,
) ([]ConfigMapPartition, int, error) {
	if len(hostRoutes) == 0 {
		return nil, startIndex, nil
	}

	// Estimate the total payload and the minimum number of buckets needed so
	// each bucket fits under maxConfigMapSize. To keep the bucket count
	// stable across reconciles (so a route's hash modulo bucketCount maps to
	// the same bucket between runs), we round up to the next power of two
	// large enough to absorb at least one doubling of the input. Small churn
	// in totalSize therefore does not change bucketCount, which means a
	// route mutation only modifies its own bucket's ConfigMap; bucketCount
	// only grows (one-shot re-bucketing event) when total payload more than
	// doubles since the last bucket-count step.
	baseSize := len(fmt.Sprintf(`{"version":1,"hosts":{"%s":[]}}`, host))
	usableSize := maxConfigMapSize - baseSize
	if usableSize <= 0 {
		usableSize = maxConfigMapSize
	}

	routeSizes := make([]int, len(hostRoutes))
	totalSize := 0
	for i, route := range hostRoutes {
		routeData, err := json.Marshal(route)
		if err != nil {
			return nil, startIndex, fmt.Errorf("failed to serialize route %d for host %s: %w", i, host, err)
		}
		routeSizes[i] = len(routeData) + 1 // +1 for comma
		if routeSizes[i]+baseSize > maxConfigMapSize {
			return nil, startIndex, fmt.Errorf(
				"route %d for host %s exceeds single-partition limit: routeBytes=%d baseOverhead=%d max=%d",
				i,
				host,
				routeSizes[i],
				baseSize,
				maxConfigMapSize,
			)
		}
		totalSize += routeSizes[i]
	}

	minBuckets := 1
	if totalSize > usableSize {
		minBuckets = (totalSize + usableSize - 1) / usableSize
	}
	bucketCount := stableBucketCount(minBuckets)

	// Try to assign with the current bucket count; if any bucket ends up
	// above maxConfigMapSize, double and try again. Capped to avoid runaway
	// growth on pathological inputs; if still overflowing after all retries,
	// return an explicit error.
	var buckets [][]routes.Route
	const maxRetries = 4
	overflow := false
	for attempt := 0; attempt < maxRetries; attempt++ {
		buckets = make([][]routes.Route, bucketCount)
		bucketBytes := make([]int, bucketCount)
		overflow = false

		for i, route := range hostRoutes {
			idx := int(routeBucket(host, route, uint32(bucketCount)))
			buckets[idx] = append(buckets[idx], route)
			bucketBytes[idx] += routeSizes[i]
			if bucketBytes[idx]+baseSize > maxConfigMapSize {
				overflow = true
			}
		}

		if !overflow {
			break
		}
		if attempt < maxRetries-1 {
			bucketCount *= 2
		}
	}
	if overflow {
		return nil, startIndex, fmt.Errorf(
			"unable to partition host %s within size limit after %d retries (bucketCount=%d)",
			host,
			maxRetries,
			bucketCount,
		)
	}

	// Sort each bucket so the JSON output is byte-stable across reconciles.
	// SortRoutes itself can leave ties (e.g. equal path length) in input
	// order, so we apply a full-identity tiebreaker here. The extproc loader
	// merges all partitions for a host and re-applies SortRoutes globally,
	// so the per-bucket order has no effect on routing priority — only on
	// the byte equality used by the per-partition dedup at
	// upsertSingleConfigMap.
	for i := range buckets {
		if len(buckets[i]) <= 1 {
			continue
		}
		if err := sortRoutesByIdentity(buckets[i]); err != nil {
			return nil, startIndex, fmt.Errorf("failed to sort routes for host %s: %w", host, err)
		}
	}

	partitions := make([]ConfigMapPartition, 0, bucketCount)
	for bucketIdx, bucket := range buckets {
		if len(bucket) == 0 {
			// Empty buckets do not emit a ConfigMap, but the bucket index is
			// still reserved so a non-empty neighbour keeps the same partition
			// name across reconciles (stability), and so downstream hosts
			// (advanced via the returned index) do not reuse our names.
			continue
		}
		partConfig := &routes.RoutesConfig{
			Version: 1,
			Hosts:   map[string][]routes.Route{host: bucket},
		}
		partData, err := partConfig.ToJSON()
		if err != nil {
			return nil, startIndex, fmt.Errorf("failed to serialize bucket %d for host %s: %w", bucketIdx, host, err)
		}
		partitions = append(partitions, ConfigMapPartition{
			Name:   r.partitionName(target, startIndex+bucketIdx),
			Target: target,
			Data:   string(partData),
		})
	}
	return partitions, startIndex + bucketCount, nil
}

// stableBucketCount rounds the minimum required bucket count up to the next
// power of two AND ensures we leave at least one doubling of headroom before
// the next step. This keeps the bucket count stable across reconciles: small
// changes in total payload do not change bucketCount, so a route's
// hash-modulo-bucketCount placement is unaffected by churn elsewhere. The
// count only grows when the working set more than doubles, which is a rare
// event and a single one-shot rebucketing is the only cost.
func stableBucketCount(minBuckets int) int {
	if minBuckets <= 1 {
		return 1
	}
	// Smallest power of two >= 2 * minBuckets.
	target := 2 * minBuckets
	bc := 2
	for bc < target {
		bc *= 2
	}
	return bc
}

// sortRoutesByIdentity orders routes by a fully deterministic identity
// signature so the JSON output of a bucket is byte-stable across reconciles
// even when SortRoutes would tie on its real-routing-priority comparisons.
// Used only inside splitHostRoutes; routing priority is re-established by
// the extproc loader after merge.
func sortRoutesByIdentity(rs []routes.Route) error {
	// Pre-compute identity keys once (O(n)) so the sort comparator does not
	// re-marshal routes on every comparison (which would be O(n log n)
	// allocations in the worst case, creating significant GC pressure for
	// large buckets).
	keys := make([]string, len(rs))
	for i := range rs {
		k, err := json.Marshal(rs[i])
		if err != nil {
			return fmt.Errorf("failed to compute identity key for route %d: %w", i, err)
		}
		keys[i] = string(k)
	}

	sort.Stable(routesByIdentity{routes: rs, keys: keys})
	return nil
}

// routesByIdentity implements sort.Interface, keeping the pre-computed
// identity keys in sync with the routes slice during swaps. Using
// sort.SliceStable would desync the keys array because it only swaps
// elements in the target slice.
type routesByIdentity struct {
	routes []routes.Route
	keys   []string
}

func (r routesByIdentity) Len() int { return len(r.routes) }
func (r routesByIdentity) Swap(i, j int) {
	r.routes[i], r.routes[j] = r.routes[j], r.routes[i]
	r.keys[i], r.keys[j] = r.keys[j], r.keys[i]
}
func (r routesByIdentity) Less(i, j int) bool {
	a, b := r.routes[i], r.routes[j]
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	if a.Method != b.Method {
		return a.Method < b.Method
	}
	if a.Backend != b.Backend {
		return a.Backend < b.Backend
	}
	// Final tiebreaker: the pre-computed marshalled byte form is fully
	// deterministic once Path/Type/Method/Backend match.
	return r.keys[i] < r.keys[j]
}

// routeBucket maps a route to a partition index. The hash input combines the
// host with the route identity fields so the same route is always assigned to
// the same bucket given a fixed bucketCount.
func routeBucket(host string, route routes.Route, bucketCount uint32) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(host))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(route.Type))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(route.Path))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(route.Method))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(route.Backend))
	_, _ = h.Write([]byte{0})
	for _, hm := range route.Headers {
		_, _ = h.Write([]byte(hm.Name))
		_, _ = h.Write([]byte{1})
		_, _ = h.Write([]byte(hm.Value))
		_, _ = h.Write([]byte{1})
		_, _ = h.Write([]byte(hm.Type))
		_, _ = h.Write([]byte{2})
	}
	for _, qm := range route.QueryParams {
		_, _ = h.Write([]byte(qm.Name))
		_, _ = h.Write([]byte{1})
		_, _ = h.Write([]byte(qm.Value))
		_, _ = h.Write([]byte{1})
		_, _ = h.Write([]byte(qm.Type))
		_, _ = h.Write([]byte{2})
	}
	return h.Sum32() % bucketCount
}

// partitionName generates the name for a partition: customrouter-routes-<target>-<index>
func (r *CustomHTTPRouteReconciler) partitionName(target string, index int) string {
	return fmt.Sprintf("%s-%s-%d", configMapBaseName, target, index)
}

// parsePartitionName parses a ConfigMap name produced by partitionName and
// returns the embedded target name. The boolean is true when the name matches
// the expected pattern "customrouter-routes-<target>-<index>" with <index> a
// non-negative decimal integer. Used to evict per-target cache entries
// without prefix-matching pitfalls (e.g. target "foo" must not match a
// partition belonging to "foo-bar").
func parsePartitionName(name string) (target string, index int, ok bool) {
	prefix := configMapBaseName + "-"
	if !strings.HasPrefix(name, prefix) {
		return "", 0, false
	}
	rest := name[len(prefix):]
	dash := strings.LastIndexByte(rest, '-')
	if dash <= 0 || dash == len(rest)-1 {
		return "", 0, false
	}
	idxStr := rest[dash+1:]
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idx < 0 {
		return "", 0, false
	}
	return rest[:dash], idx, true
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
//
// An in-memory hash cache (partitionHashes) is checked first: when the
// computed partition data hash matches the last successfully written value,
// the entire Get+Compare+Update cycle is skipped, avoiding any etcd
// interaction for unchanged partitions.
func (r *CustomHTTPRouteReconciler) upsertSingleConfigMap(
	ctx context.Context,
	partition ConfigMapPartition,
) error {
	// Fast-path: skip the entire Get+Compare cycle when the partition
	// content has not changed since the last successful write.
	dataHash := fnvHash(partition.Data)
	if r.partitionHashHit(partition.Name, dataHash) {
		return nil
	}

	configMapKey := types.NamespacedName{
		Name:      partition.Name,
		Namespace: r.ConfigMapNamespace,
	}

	partNumber := "0"
	if _, idx, ok := parsePartitionName(partition.Name); ok {
		partNumber = strconv.Itoa(idx)
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

	err := retry.OnError(backoff, func(err error) bool {
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

	if err == nil {
		r.setPartitionHash(partition.Name, dataHash)
	}
	return err
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
			r.clearPartitionHash(cm.Name)
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

// fnvHash returns the FNV-32a hash of a string.
func fnvHash(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// partitionHashHit returns true if the cached hash for the given ConfigMap
// name matches the provided hash, meaning the content is unchanged.
func (r *CustomHTTPRouteReconciler) partitionHashHit(name string, hash uint32) bool {
	r.partitionHashesMu.Lock()
	defer r.partitionHashesMu.Unlock()
	cached, ok := r.partitionHashes[name]
	return ok && cached == hash
}

// setPartitionHash stores the content hash for a ConfigMap partition.
func (r *CustomHTTPRouteReconciler) setPartitionHash(name string, hash uint32) {
	r.partitionHashesMu.Lock()
	defer r.partitionHashesMu.Unlock()
	if r.partitionHashes == nil {
		r.partitionHashes = make(map[string]uint32)
	}
	r.partitionHashes[name] = hash
}

// clearPartitionHash removes the cached hash for a ConfigMap partition.
func (r *CustomHTTPRouteReconciler) clearPartitionHash(name string) {
	r.partitionHashesMu.Lock()
	defer r.partitionHashesMu.Unlock()
	delete(r.partitionHashes, name)
}
