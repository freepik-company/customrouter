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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/internal/controller"
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
