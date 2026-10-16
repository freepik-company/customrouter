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
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	ef "github.com/freepik-company/customrouter/internal/controller/envoyfilter"
)

const (
	// ConditionTypeReady indicates whether the EnvoyFilters were created successfully
	ConditionTypeReady = "Ready"

	// ConditionTypeVirtualHosts reports the automatic virtual hosts (spec.virtualHosts).
	ConditionTypeVirtualHosts = "VirtualHostsProgrammed"
)

// updateConditionVirtualHosts records which CustomHTTPRoute hostnames got a virtual host
// and which were skipped (and why), or removes the condition when the feature is off.
func (r *ExternalProcessorAttachmentReconciler) updateConditionVirtualHosts(
	attachment *v1alpha1.ExternalProcessorAttachment,
	plan ef.VirtualHostsPlan,
) {
	if !ef.VirtualHostsEnabled(attachment) {
		meta.RemoveStatusCondition(&attachment.Status.Conditions, ConditionTypeVirtualHosts)
		return
	}
	skipped := make([]string, 0, len(plan.Skipped))
	for hostname, reason := range plan.Skipped {
		skipped = append(skipped, hostname+" ("+reason+")")
	}
	sort.Strings(skipped)
	message := fmt.Sprintf("%d virtual hosts managed", len(plan.Managed))
	if len(skipped) > 0 {
		message += "; skipped: " + strings.Join(skipped, ", ")
	}
	meta.SetStatusCondition(&attachment.Status.Conditions, metav1.Condition{
		Type:               ConditionTypeVirtualHosts,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: attachment.Generation,
		Reason:             "VirtualHostsReconciled",
		Message:            message,
	})
}

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
