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

package controller

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition reasons for CustomHTTPRoute
const (
	// ConditionReasonReconcileSuccess indicates successful reconciliation
	ConditionReasonReconcileSuccess        = "ReconcileSuccess"
	ConditionReasonReconcileSuccessMessage = "CustomHTTPRoute was reconciled successfully"

	// ConditionReasonReconcileError indicates an error during reconciliation
	ConditionReasonReconcileError        = "ReconcileError"
	ConditionReasonReconcileErrorMessage = "Failed to reconcile CustomHTTPRoute"

	// ConditionReasonConfigMapSuccess indicates ConfigMap was synced successfully
	ConditionReasonConfigMapSuccess        = "ConfigMapSyncSuccess"
	ConditionReasonConfigMapSuccessMessage = "ConfigMap was generated and synced successfully"

	// ConditionReasonConfigMapError indicates an error syncing the ConfigMap
	ConditionReasonConfigMapError        = "ConfigMapSyncError"
	ConditionReasonConfigMapErrorMessage = "Failed to generate or sync ConfigMap"
)

// NewCondition a set of default options for creating a Condition.
func NewCondition(condType string, status metav1.ConditionStatus, reason, message string) metav1.Condition {
	return metav1.Condition{
		Type:               condType,
		Status:             status,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
}

func getCondition(conditions *[]metav1.Condition, condType string) *metav1.Condition {
	for i, v := range *conditions {
		if v.Type == condType {
			return &(*conditions)[i]
		}
	}
	return nil
}

func UpdateCondition(conditions *[]metav1.Condition, condition metav1.Condition) {

	// Get the condition
	currentCondition := getCondition(conditions, condition.Type)

	if currentCondition == nil {
		// Create the condition when not existent
		*conditions = append(*conditions, condition)
	} else {
		// Update the condition when existent.
		currentCondition.Status = condition.Status
		currentCondition.Reason = condition.Reason
		currentCondition.Message = condition.Message
		currentCondition.LastTransitionTime = metav1.Now()
	}
}
