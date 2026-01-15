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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

const (
	// ConditionTypeReady indicates whether the EnvoyFilters were created successfully
	ConditionTypeReady = "Ready"
)

// updateConditionReady sets the Ready condition to True
func (r *ExternalProcessorAttachmentReconciler) updateConditionReady(attachment *v1alpha1.ExternalProcessorAttachment) {
	condition := metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             "EnvoyFiltersCreated",
		Message:            "EnvoyFilters were created successfully",
		LastTransitionTime: metav1.NewTime(time.Now()),
	}
	setCondition(attachment, condition)
}

// updateConditionFailed sets the Ready condition to False
func (r *ExternalProcessorAttachmentReconciler) updateConditionFailed(attachment *v1alpha1.ExternalProcessorAttachment, message string) {
	condition := metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             metav1.ConditionFalse,
		Reason:             "EnvoyFiltersFailed",
		Message:            message,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}
	setCondition(attachment, condition)
}

// setCondition updates or adds a condition to the attachment status
func setCondition(attachment *v1alpha1.ExternalProcessorAttachment, condition metav1.Condition) {
	for i, c := range attachment.Status.Conditions {
		if c.Type == condition.Type {
			// Only update if status changed
			if c.Status != condition.Status {
				attachment.Status.Conditions[i] = condition
			}
			return
		}
	}
	// Condition not found, add it
	attachment.Status.Conditions = append(attachment.Status.Conditions, condition)
}
