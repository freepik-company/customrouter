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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/internal/controller"
	ef "github.com/freepik-company/customrouter/internal/controller/envoyfilter"
)

// UpdateConditionReconciled sets the Reconciled condition to True
func (r *CustomHTTPRouteReconciler) UpdateConditionReconciled(object *v1alpha1.CustomHTTPRoute) {
	meta.SetStatusCondition(&object.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeReconciled,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: object.Generation,
		Reason:             controller.ConditionReasonReconcileSuccess,
		Message:            controller.ConditionReasonReconcileSuccessMessage,
	})
}

// UpdateConditionReconcileFailed sets the Reconciled condition to False
func (r *CustomHTTPRouteReconciler) UpdateConditionReconcileFailed(object *v1alpha1.CustomHTTPRoute, message string) {
	msg := controller.ConditionReasonReconcileErrorMessage
	if message != "" {
		msg = message
	}
	meta.SetStatusCondition(&object.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeReconciled,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: object.Generation,
		Reason:             controller.ConditionReasonReconcileError,
		Message:            msg,
	})
}

// UpdateConditionConfigMapSynced sets the ConfigMapSynced condition to True
func (r *CustomHTTPRouteReconciler) UpdateConditionConfigMapSynced(object *v1alpha1.CustomHTTPRoute) {
	meta.SetStatusCondition(&object.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeConfigMapSynced,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: object.Generation,
		Reason:             controller.ConditionReasonConfigMapSuccess,
		Message:            controller.ConditionReasonConfigMapSuccessMessage,
	})
}

// UpdateConditionConfigMapFailed sets the ConfigMapSynced condition to False
func (r *CustomHTTPRouteReconciler) UpdateConditionConfigMapFailed(object *v1alpha1.CustomHTTPRoute, message string) {
	msg := controller.ConditionReasonConfigMapErrorMessage
	if message != "" {
		msg = message
	}
	meta.SetStatusCondition(&object.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeConfigMapSynced,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: object.Generation,
		Reason:             controller.ConditionReasonConfigMapError,
		Message:            msg,
	})
}

// UpdateConditionCatchAllProgrammed sets the CatchAllProgrammed condition from the given evaluation result.
func (r *CustomHTTPRouteReconciler) UpdateConditionCatchAllProgrammed(
	object *v1alpha1.CustomHTTPRoute,
	status ef.CatchAllProgrammedStatus,
) {
	condStatus := metav1.ConditionFalse
	if status.Programmed {
		condStatus = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&object.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeCatchAllProgrammed,
		Status:             condStatus,
		ObservedGeneration: object.Generation,
		Reason:             status.Reason,
		Message:            catchAllMessageFor(status.Reason),
	})
}

// ComputeCatchAllProgrammedStatus resolves the CatchAllProgrammed state for a route by listing
// the routes and EPAs needed to decide dedup and overrides. Returns NotConfigured without
// any List call when the spec has no catchAllRoute.
func (r *CustomHTTPRouteReconciler) ComputeCatchAllProgrammedStatus(
	ctx context.Context,
	route *v1alpha1.CustomHTTPRoute,
	routeList *v1alpha1.CustomHTTPRouteList,
	epaList *v1alpha1.ExternalProcessorAttachmentList,
) (ef.CatchAllProgrammedStatus, error) {
	if route.Spec.CatchAllRoute == nil {
		return ef.CatchAllProgrammedStatus{Reason: controller.ConditionReasonCatchAllNotConfigured}, nil
	}

	if routeList == nil {
		routeList = &v1alpha1.CustomHTTPRouteList{}
		if err := r.List(ctx, routeList); err != nil {
			return ef.CatchAllProgrammedStatus{}, fmt.Errorf("failed to list CustomHTTPRoutes: %w", err)
		}
	}

	if epaList == nil {
		epaList = &v1alpha1.ExternalProcessorAttachmentList{}
		if err := r.List(ctx, epaList); err != nil {
			return ef.CatchAllProgrammedStatus{}, fmt.Errorf("failed to list ExternalProcessorAttachments: %w", err)
		}
	}

	return ef.EvaluateCatchAllProgrammed(route, routeList, epaList), nil
}

func catchAllMessageFor(reason string) string {
	switch reason {
	case controller.ConditionReasonCatchAllProgrammed:
		return controller.ConditionReasonCatchAllProgrammedMessage
	case controller.ConditionReasonCatchAllNotConfigured:
		return controller.ConditionReasonCatchAllNotConfiguredMessage
	case controller.ConditionReasonCatchAllNoEPA:
		return controller.ConditionReasonCatchAllNoEPAMessage
	case controller.ConditionReasonCatchAllOverriddenByEPA:
		return controller.ConditionReasonCatchAllOverriddenByEPAMessage
	case controller.ConditionReasonCatchAllOverriddenByRoute:
		return controller.ConditionReasonCatchAllOverriddenByRouteMessage
	default:
		return ""
	}
}
