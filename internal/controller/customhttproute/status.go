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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	//
	"github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/internal/controller"
)

// UpdateConditionReconciled sets the Reconciled condition to True
func (r *CustomHTTPRouteReconciler) UpdateConditionReconciled(object *v1alpha1.CustomHTTPRoute) {
	condition := controller.NewCondition(
		v1alpha1.ConditionTypeReconciled,
		metav1.ConditionTrue,
		controller.ConditionReasonReconcileSuccess,
		controller.ConditionReasonReconcileSuccessMessage,
	)
	controller.UpdateCondition(&object.Status.Conditions, condition)
}

// UpdateConditionReconcileFailed sets the Reconciled condition to False
func (r *CustomHTTPRouteReconciler) UpdateConditionReconcileFailed(object *v1alpha1.CustomHTTPRoute, message string) {
	msg := controller.ConditionReasonReconcileErrorMessage
	if message != "" {
		msg = message
	}
	condition := controller.NewCondition(
		v1alpha1.ConditionTypeReconciled,
		metav1.ConditionFalse,
		controller.ConditionReasonReconcileError,
		msg,
	)
	controller.UpdateCondition(&object.Status.Conditions, condition)
}

// UpdateConditionConfigMapSynced sets the ConfigMapSynced condition to True
func (r *CustomHTTPRouteReconciler) UpdateConditionConfigMapSynced(object *v1alpha1.CustomHTTPRoute) {
	condition := controller.NewCondition(
		v1alpha1.ConditionTypeConfigMapSynced,
		metav1.ConditionTrue,
		controller.ConditionReasonConfigMapSuccess,
		controller.ConditionReasonConfigMapSuccessMessage,
	)
	controller.UpdateCondition(&object.Status.Conditions, condition)
}

// UpdateConditionConfigMapFailed sets the ConfigMapSynced condition to False
func (r *CustomHTTPRouteReconciler) UpdateConditionConfigMapFailed(object *v1alpha1.CustomHTTPRoute, message string) {
	msg := controller.ConditionReasonConfigMapErrorMessage
	if message != "" {
		msg = message
	}
	condition := controller.NewCondition(
		v1alpha1.ConditionTypeConfigMapSynced,
		metav1.ConditionFalse,
		controller.ConditionReasonConfigMapError,
		msg,
	)
	controller.UpdateCondition(&object.Status.Conditions, condition)
}
