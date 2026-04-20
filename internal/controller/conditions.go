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

package controller

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

	// ConditionReasonCatchAllProgrammed indicates the catchAllRoute is applied on at least one EPA
	ConditionReasonCatchAllProgrammed        = "Programmed"
	ConditionReasonCatchAllProgrammedMessage = "catchAllRoute is applied to the dataplane"

	// ConditionReasonCatchAllNotConfigured indicates the route has no catchAllRoute in its spec
	ConditionReasonCatchAllNotConfigured        = "NotConfigured"
	ConditionReasonCatchAllNotConfiguredMessage = "Route has no catchAllRoute configured"

	// ConditionReasonCatchAllNoEPA indicates catchAllRoute is configured but no EPA exists to apply it
	ConditionReasonCatchAllNoEPA        = "NoExternalProcessor"
	ConditionReasonCatchAllNoEPAMessage = "catchAllRoute is configured but no ExternalProcessorAttachment exists"

	// ConditionReasonCatchAllOverriddenByEPA indicates an EPA's own catchAllRoute overrides this route's
	ConditionReasonCatchAllOverriddenByEPA        = "OverriddenByEPA"
	ConditionReasonCatchAllOverriddenByEPAMessage = "catchAllRoute is overridden by an ExternalProcessorAttachment catchAllRoute for the same hostname"

	// ConditionReasonCatchAllOverriddenByRoute indicates another CustomHTTPRoute wins the dedup for all hostnames
	ConditionReasonCatchAllOverriddenByRoute        = "OverriddenByRoute"
	ConditionReasonCatchAllOverriddenByRouteMessage = "catchAllRoute is overridden by another CustomHTTPRoute for the same hostname"
)
