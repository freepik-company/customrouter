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

	//
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	//
	customrouterfreepikcomv1alpha1 "github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/internal/controller"
)

// CustomHTTPRouteReconciler reconciles a CustomHTTPRoute object
type CustomHTTPRouteReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	ConfigMapNamespace string
}

// +kubebuilder:rbac:groups=customrouter.freepik.com,resources=customhttproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=customrouter.freepik.com,resources=customhttproutes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=customrouter.freepik.com,resources=customhttproutes/finalizers,verbs=update
// +kubebuilder:rbac:groups=customrouter.freepik.com,resources=externalprocessorattachments,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=envoyfilters,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.4/pkg/reconcile
func (r *CustomHTTPRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)

	// 1. Get the content of the resource
	objectManifest := &customrouterfreepikcomv1alpha1.CustomHTTPRoute{}
	err = r.Get(ctx, req.NamespacedName, objectManifest)

	// 2. Check the existence inside the cluster
	if err != nil {

		// 2.1 It does NOT exist: manage removal
		if err = client.IgnoreNotFound(err); err == nil {
			logger.Info(fmt.Sprintf(controller.ResourceNotFoundError, controller.CustomHttpRouteResourceType, req.Name))
			return result, err
		}

		// 2.2 Failed to get the resource, requeue the request
		logger.Info(fmt.Sprintf(controller.ResourceRetrievalError, controller.CustomHttpRouteResourceType, req.Name, err.Error()))
		return result, err
	}

	// 3. Check if the resource instance is marked to be deleted: indicated by the deletion timestamp being set
	if !objectManifest.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(objectManifest, controller.ResourceFinalizer) {
			// Delete Notification from WatcherPool
			err = r.ReconcileObject(ctx, watch.Deleted, objectManifest)
			if err != nil {
				logger.Info(fmt.Sprintf(controller.ResourceReconcileError, controller.CustomHttpRouteResourceType, req.Name, err.Error()))
				return result, err
			}

			// Remove the finalizers on the resource
			controllerutil.RemoveFinalizer(objectManifest, controller.ResourceFinalizer)
			err = controller.UpdateWithRetry(ctx, r.Client, objectManifest, func(object client.Object) error {
				controllerutil.RemoveFinalizer(object, controller.ResourceFinalizer)
				return nil
			})
			if err != nil {
				logger.Info(fmt.Sprintf(controller.ResourceFinalizersUpdateError, controller.CustomHttpRouteResourceType, req.Name, err.Error()))
			}
		}
		result = ctrl.Result{}
		err = nil
		return result, err
	}

	// 4. Add a finalizer to the resource
	if !controllerutil.ContainsFinalizer(objectManifest, controller.ResourceFinalizer) {
		err = controller.UpdateWithRetry(ctx, r.Client, objectManifest, func(object client.Object) error {
			controllerutil.AddFinalizer(objectManifest, controller.ResourceFinalizer)
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
		logger.Info(fmt.Sprintf(controller.ResourceValidationError, controller.CustomHttpRouteResourceType, req.Name, err.Error()))
		return result, nil // Don't requeue validation errors
	}

	// 6. Update the status before the requeue
	defer func() {
		// Save conditions before the Get overwrites objectManifest
		conditionsToApply := objectManifest.Status.Conditions
		statusErr := controller.UpdateStatusWithRetry(ctx, r.Client, objectManifest, func(object client.Object) error {
			// Copy the saved conditions to the fresh object
			route := object.(*customrouterfreepikcomv1alpha1.CustomHTTPRoute)
			route.Status.Conditions = conditionsToApply
			return nil
		})
		if statusErr != nil {
			logger.Error(statusErr, "Failed to update status", "name", req.Name)
		}
	}()

	// 7. The resource already exists: manage the update
	err = r.ReconcileObject(ctx, watch.Modified, objectManifest)
	if err != nil {
		// Set Reconciled to True (manifest was processed) but ConfigMapSynced to False (ConfigMap failed)
		r.UpdateConditionReconciled(objectManifest)
		r.UpdateConditionConfigMapFailed(objectManifest, err.Error())
		logger.Info(fmt.Sprintf(controller.ResourceReconcileError, controller.CustomHttpRouteResourceType, req.Name, err.Error()))
		return result, err
	}

	// 8. Success, update the status
	r.UpdateConditionReconciled(objectManifest)
	r.UpdateConditionConfigMapSynced(objectManifest)

	return result, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *CustomHTTPRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&customrouterfreepikcomv1alpha1.CustomHTTPRoute{}).
		Named("customhttproute").
		Complete(r)
}
