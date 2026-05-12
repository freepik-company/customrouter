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
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	crv1alpha1 "github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/internal/controller"
)

const targetRefIndexField = ".spec.targetRef.name"

// DefaultRebuildCooldown is the minimum interval enforced between successful
// ConfigMap rebuilds for the same target. It bounds the API/etcd write rate
// that the controller can generate when many CustomHTTPRoutes sharing a
// target are enqueued together (typically at controller cache resync).
const DefaultRebuildCooldown = 500 * time.Millisecond

// CustomHTTPRouteReconciler reconciles a CustomHTTPRoute object
type CustomHTTPRouteReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	ConfigMapNamespace      string
	MaxConcurrentReconciles int

	// RebuildCooldown caps how often a single target's ConfigMaps can be
	// rebuilt. Reconciles arriving while a target is in cooldown are requeued
	// after the remaining wait instead of repeating the rebuild work.
	// When zero, DefaultRebuildCooldown is used.
	RebuildCooldown time.Duration

	// lastRebuildAt records the last successful rebuild time per target name.
	// Read/written under rebuildMu.
	lastRebuildAt map[string]time.Time
	rebuildMu     sync.Mutex
}

// effectiveRebuildCooldown returns the cooldown to apply. A zero value falls
// back to DefaultRebuildCooldown; a negative value disables throttling (useful
// in tests).
func (r *CustomHTTPRouteReconciler) effectiveRebuildCooldown() time.Duration {
	if r.RebuildCooldown == 0 {
		return DefaultRebuildCooldown
	}
	return r.RebuildCooldown
}

// rebuildWait reports the remaining cooldown for the given target. The bool
// result is true when the target is still in cooldown and the caller should
// requeue instead of rebuilding.
func (r *CustomHTTPRouteReconciler) rebuildWait(target string, now time.Time) (time.Duration, bool) {
	cooldown := r.effectiveRebuildCooldown()
	if cooldown <= 0 {
		return 0, false
	}
	r.rebuildMu.Lock()
	defer r.rebuildMu.Unlock()
	last, ok := r.lastRebuildAt[target]
	if !ok {
		return 0, false
	}
	elapsed := now.Sub(last)
	if elapsed >= cooldown {
		return 0, false
	}
	return cooldown - elapsed, true
}

// markRebuilt records a successful rebuild for the given target.
func (r *CustomHTTPRouteReconciler) markRebuilt(target string, when time.Time) {
	r.rebuildMu.Lock()
	defer r.rebuildMu.Unlock()
	if r.lastRebuildAt == nil {
		r.lastRebuildAt = make(map[string]time.Time)
	}
	r.lastRebuildAt[target] = when
}

// +kubebuilder:rbac:groups=customrouter.freepik.com,resources=customhttproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=customrouter.freepik.com,resources=customhttproutes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=customrouter.freepik.com,resources=customhttproutes/finalizers,verbs=update
// +kubebuilder:rbac:groups=customrouter.freepik.com,resources=externalprocessorattachments,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.istio.io,resources=envoyfilters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *CustomHTTPRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)

	// 1. Get the content of the resource
	objectManifest := &crv1alpha1.CustomHTTPRoute{}
	err = r.Get(ctx, req.NamespacedName, objectManifest)

	// 2. Check the existence inside the cluster
	if err != nil {
		if err = client.IgnoreNotFound(err); err == nil {
			logger.Info("Resource not found, ignoring since object must be deleted", "name", req.Name)
			return result, err
		}
		logger.Error(err, "Failed to get resource", "name", req.Name)
		return result, err
	}

	// 3. Check if the resource instance is marked to be deleted
	if !objectManifest.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(objectManifest, controller.ResourceFinalizer) {
			result, err = r.ReconcileObject(ctx, watch.Deleted, objectManifest)
			if err != nil {
				logger.Error(err, "Failed to reconcile deletion", "name", req.Name)
				return result, err
			}

			err = controller.UpdateWithRetry(ctx, r.Client, objectManifest, func(object client.Object) error {
				controllerutil.RemoveFinalizer(object, controller.ResourceFinalizer)
				return nil
			})
			if err != nil {
				logger.Error(err, "Failed to remove finalizer", "name", req.Name)
			}
		}
		return ctrl.Result{}, nil
	}

	// 4. Add a finalizer to the resource
	if !controllerutil.ContainsFinalizer(objectManifest, controller.ResourceFinalizer) {
		err = controller.UpdateWithRetry(ctx, r.Client, objectManifest, func(object client.Object) error {
			controllerutil.AddFinalizer(object, controller.ResourceFinalizer)
			return nil
		})
		if err != nil {
			return result, err
		}
	}

	// 5. Validate the resource
	if err = objectManifest.Validate(); err != nil {
		r.UpdateConditionReconciled(objectManifest)
		r.UpdateConditionConfigMapFailed(objectManifest, err.Error())
		logger.Info("Validation failed", "name", req.Name, "error", err.Error())
		return result, nil // Don't requeue validation errors
	}

	// 6. Update the status before the requeue
	defer func() {
		statusToApply := objectManifest.Status
		statusToApply.ObservedGeneration = objectManifest.Generation
		statusErr := controller.UpdateStatusWithRetry(ctx, r.Client, objectManifest, func(object client.Object) error {
			route := object.(*crv1alpha1.CustomHTTPRoute)
			route.Status = statusToApply
			return nil
		})
		if statusErr != nil {
			logger.Error(statusErr, "Failed to update status", "name", req.Name)
		}
	}()

	// 7. The resource already exists: manage the update
	result, err = r.ReconcileObject(ctx, watch.Modified, objectManifest)
	if err != nil {
		r.UpdateConditionReconciled(objectManifest)
		r.UpdateConditionConfigMapFailed(objectManifest, err.Error())
		logger.Error(err, "Failed to reconcile", "name", req.Name)
		return result, err
	}
	// If the rebuild was throttled (RequeueAfter set), stop here. Conditions
	// will be refreshed on the next reconcile when the rebuild actually runs.
	if result.RequeueAfter > 0 {
		return result, nil
	}

	// 8. Success, update the status
	r.UpdateConditionReconciled(objectManifest)
	r.UpdateConditionConfigMapSynced(objectManifest)

	catchAllStatus, catchAllErr := r.ComputeCatchAllProgrammedStatus(ctx, objectManifest)
	if catchAllErr != nil {
		logger.Error(catchAllErr, "Failed to compute CatchAllProgrammed status", "name", req.Name)
	} else {
		r.UpdateConditionCatchAllProgrammed(objectManifest, catchAllStatus)
	}

	return result, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *CustomHTTPRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&crv1alpha1.CustomHTTPRoute{},
		targetRefIndexField,
		func(obj client.Object) []string {
			route := obj.(*crv1alpha1.CustomHTTPRoute)
			return []string{route.Spec.TargetRef.Name}
		},
	); err != nil {
		return fmt.Errorf("failed to create field indexer for %s: %w", targetRefIndexField, err)
	}

	maxConcurrent := r.MaxConcurrentReconciles
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&crv1alpha1.CustomHTTPRoute{}).
		Watches(&corev1.Service{}, handler.EnqueueRequestsFromMapFunc(r.findRoutesForService)).
		Watches(&gatewayv1.HTTPRoute{}, handler.EnqueueRequestsFromMapFunc(r.findRoutesForHTTPRoute)).
		WithOptions(ctrlcontroller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Named("customhttproute").
		Complete(r)
}

// findRoutesForService returns reconcile requests for all CustomHTTPRoutes that reference the given Service.
func (r *CustomHTTPRouteReconciler) findRoutesForService(ctx context.Context, obj client.Object) []reconcile.Request {
	svc, ok := obj.(*corev1.Service)
	if !ok {
		return nil
	}

	routeList := &crv1alpha1.CustomHTTPRouteList{}
	if err := r.List(ctx, routeList); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, route := range routeList.Items {
		if routeReferencesService(&route, svc.Name, svc.Namespace) {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      route.Name,
					Namespace: route.Namespace,
				},
			})
		}
	}
	return requests
}

// routeReferencesService checks if a CustomHTTPRoute has any backendRef pointing to the given service.
func routeReferencesService(route *crv1alpha1.CustomHTTPRoute, svcName, svcNamespace string) bool {
	for _, rule := range route.Spec.Rules {
		for _, ref := range rule.BackendRefs {
			if ref.Name == svcName && ref.Namespace == svcNamespace {
				return true
			}
		}
	}
	return false
}

// findRoutesForHTTPRoute enqueues CustomHTTPRoutes whose catchAllRoute covers a hostname
// that the given HTTPRoute declares. A HTTPRoute create/update/delete can flip whether
// the catchall EnvoyFilter must ADD a new virtual host or inject into an existing one,
// so those CustomHTTPRoutes need re-reconciliation.
func (r *CustomHTTPRouteReconciler) findRoutesForHTTPRoute(ctx context.Context, obj client.Object) []reconcile.Request {
	hr, ok := obj.(*gatewayv1.HTTPRoute)
	if !ok || len(hr.Spec.Hostnames) == 0 {
		return nil
	}
	hostSet := make(map[string]struct{}, len(hr.Spec.Hostnames))
	for _, h := range hr.Spec.Hostnames {
		hostSet[string(h)] = struct{}{}
	}

	routeList := &crv1alpha1.CustomHTTPRouteList{}
	if err := r.List(ctx, routeList); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for i := range routeList.Items {
		route := &routeList.Items[i]
		if route.Spec.CatchAllRoute == nil {
			continue
		}
		for _, h := range route.Spec.Hostnames {
			if _, match := hostSet[h]; match {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      route.Name,
						Namespace: route.Namespace,
					},
				})
				break
			}
		}
	}
	return requests
}
