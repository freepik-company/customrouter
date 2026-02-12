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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	customrouterfreepikcomv1alpha1 "github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/internal/controller"
)

const (
	ExternalProcessorAttachmentResourceType = "ExternalProcessorAttachment"
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

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ExternalProcessorAttachmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)

	// 1. Get the content of the resource
	attachment := &customrouterfreepikcomv1alpha1.ExternalProcessorAttachment{}
	err = r.Get(ctx, req.NamespacedName, attachment)

	// 2. Check the existence inside the cluster
	if err != nil {
		// 2.1 It does NOT exist: manage removal
		if err = client.IgnoreNotFound(err); err == nil {
			logger.Info(fmt.Sprintf(controller.ResourceNotFoundError, ExternalProcessorAttachmentResourceType, req.Name))
			return result, err
		}

		// 2.2 Failed to get the resource, requeue the request
		logger.Info(fmt.Sprintf(controller.ResourceRetrievalError, ExternalProcessorAttachmentResourceType, req.Name, err.Error()))
		return result, err
	}

	// 3. Check if the resource instance is marked to be deleted
	if !attachment.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(attachment, controller.ResourceFinalizer) {
			// Delete the EnvoyFilters
			if err = r.deleteEnvoyFilters(ctx, attachment); err != nil {
				logger.Info(fmt.Sprintf(controller.ResourceReconcileError, ExternalProcessorAttachmentResourceType, req.Name, err.Error()))
				return result, err
			}

			// Remove the finalizer using Patch
			patch := client.MergeFrom(attachment.DeepCopy())
			controllerutil.RemoveFinalizer(attachment, controller.ResourceFinalizer)
			if err = r.Patch(ctx, attachment, patch); err != nil {
				logger.Info(fmt.Sprintf(controller.ResourceFinalizersUpdateError, ExternalProcessorAttachmentResourceType, req.Name, err.Error()))
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
		conditionsToApply := attachment.Status.Conditions
		statusErr := controller.UpdateStatusWithRetry(ctx, r.Client, attachment, func(object client.Object) error {
			att := object.(*customrouterfreepikcomv1alpha1.ExternalProcessorAttachment)
			att.Status.Conditions = conditionsToApply
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
		logger.Info(fmt.Sprintf(controller.ResourceReconcileError, ExternalProcessorAttachmentResourceType, req.Name, err.Error()))
		return result, err
	}

	// 7. Success
	r.updateConditionReady(attachment)

	return result, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *ExternalProcessorAttachmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&customrouterfreepikcomv1alpha1.ExternalProcessorAttachment{}).
		Named("externalprocessorattachment").
		Complete(r)
}
