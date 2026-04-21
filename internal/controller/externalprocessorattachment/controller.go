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

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	crv1alpha1 "github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/internal/controller"
)

// ExternalProcessorAttachmentReconciler reconciles a ExternalProcessorAttachment object
type ExternalProcessorAttachmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=customrouter.freepik.com,resources=externalprocessorattachments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=customrouter.freepik.com,resources=externalprocessorattachments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=customrouter.freepik.com,resources=externalprocessorattachments/finalizers,verbs=update
// +kubebuilder:rbac:groups=customrouter.freepik.com,resources=customhttproutes,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.istio.io,resources=envoyfilters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ExternalProcessorAttachmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)

	// 1. Get the content of the resource
	attachment := &crv1alpha1.ExternalProcessorAttachment{}
	err = r.Get(ctx, req.NamespacedName, attachment)

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
	if !attachment.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(attachment, controller.ResourceFinalizer) {
			if err = r.deleteEnvoyFilters(ctx, attachment); err != nil {
				logger.Error(err, "Failed to delete EnvoyFilters", "name", req.Name)
				return result, err
			}

			patch := client.MergeFrom(attachment.DeepCopy())
			controllerutil.RemoveFinalizer(attachment, controller.ResourceFinalizer)
			if err = r.Patch(ctx, attachment, patch); err != nil {
				logger.Error(err, "Failed to remove finalizer", "name", req.Name)
			}
		}
		return ctrl.Result{}, nil
	}

	// 4. Add a finalizer to the resource
	if !controllerutil.ContainsFinalizer(attachment, controller.ResourceFinalizer) {
		patch := client.MergeFrom(attachment.DeepCopy())
		controllerutil.AddFinalizer(attachment, controller.ResourceFinalizer)
		if err = r.Patch(ctx, attachment, patch); err != nil {
			return result, err
		}
	}

	// 5. Update the status before the requeue
	defer func() {
		statusToApply := attachment.Status
		statusToApply.ObservedGeneration = attachment.Generation
		statusErr := controller.UpdateStatusWithRetry(ctx, r.Client, attachment, func(object client.Object) error {
			att := object.(*crv1alpha1.ExternalProcessorAttachment)
			att.Status = statusToApply
			return nil
		})
		if statusErr != nil {
			logger.Error(statusErr, "Failed to update status", "name", req.Name)
		}
	}()

	// 6. Reconcile the EnvoyFilters
	err = r.reconcileEnvoyFilters(ctx, attachment)
	if err != nil {
		r.updateConditionFailed(attachment, err.Error())
		logger.Error(err, "Failed to reconcile EnvoyFilters", "name", req.Name)
		return result, err
	}

	// 7. Success
	r.updateConditionReady(attachment)

	return result, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *ExternalProcessorAttachmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&crv1alpha1.ExternalProcessorAttachment{}).
		Watches(&gatewayv1.HTTPRoute{}, handler.EnqueueRequestsFromMapFunc(r.findEPAsForHTTPRoute)).
		Named("externalprocessorattachment").
		Complete(r)
}

// findEPAsForHTTPRoute enqueues every EPA when an HTTPRoute changes. HTTPRoute
// create/update/delete can flip whether the EPA's catchall EnvoyFilter must ADD a
// new virtual host or inject into an existing one. The blast radius is contained by
// the fact that there are typically only a handful of EPAs per cluster.
func (r *ExternalProcessorAttachmentReconciler) findEPAsForHTTPRoute(ctx context.Context, _ client.Object) []reconcile.Request {
	epaList := &crv1alpha1.ExternalProcessorAttachmentList{}
	if err := r.List(ctx, epaList); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(epaList.Items))
	for i := range epaList.Items {
		epa := &epaList.Items[i]
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      epa.Name,
				Namespace: epa.Namespace,
			},
		})
	}
	return requests
}
