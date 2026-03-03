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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

const (
	// ConditionTypeReady indicates whether the EnvoyFilters were created successfully
	ConditionTypeReady = "Ready"
)

// updateConditionReady sets the Ready condition to True
func (r *ExternalProcessorAttachmentReconciler) updateConditionReady(attachment *v1alpha1.ExternalProcessorAttachment) {
	meta.SetStatusCondition(&attachment.Status.Conditions, metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: attachment.Generation,
		Reason:             "EnvoyFiltersCreated",
		Message:            "EnvoyFilters were created successfully",
	})
}

// updateConditionFailed sets the Ready condition to False
func (r *ExternalProcessorAttachmentReconciler) updateConditionFailed(attachment *v1alpha1.ExternalProcessorAttachment, message string) {
	meta.SetStatusCondition(&attachment.Status.Conditions, metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: attachment.Generation,
		Reason:             "EnvoyFiltersFailed",
		Message:            message,
	})
}
