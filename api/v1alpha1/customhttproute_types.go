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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PathPrefixPolicy defines how path prefixes are applied to routes
// +kubebuilder:validation:Enum=Optional;Required;Disabled
type PathPrefixPolicy string

const (
	// PathPrefixPolicyOptional generates routes with and without prefix
	PathPrefixPolicyOptional PathPrefixPolicy = "Optional"

	// PathPrefixPolicyRequired generates routes only with prefix
	PathPrefixPolicyRequired PathPrefixPolicy = "Required"

	// PathPrefixPolicyDisabled generates routes without any prefix
	PathPrefixPolicyDisabled PathPrefixPolicy = "Disabled"
)

// MatchType defines the type of path matching
// +kubebuilder:validation:Enum=PathPrefix;Exact;Regex
type MatchType string

const (
	// MatchTypePathPrefix matches paths that start with the specified value
	MatchTypePathPrefix MatchType = "PathPrefix"

	// MatchTypeExact matches paths that are exactly equal to the specified value
	MatchTypeExact MatchType = "Exact"

	// MatchTypeRegex matches paths using Go regexp syntax
	MatchTypeRegex MatchType = "Regex"
)

const (
	// DefaultPriority is the default priority for routes
	DefaultPriority int32 = 1000
)

// Status condition types for CustomHTTPRoute
const (
	// ConditionTypeReconciled indicates whether the CustomHTTPRoute manifest was processed
	ConditionTypeReconciled = "Reconciled"

	// ConditionTypeConfigMapSynced indicates whether the ConfigMap was successfully generated
	ConditionTypeConfigMapSynced = "ConfigMapSynced"
)

// PathPrefixes defines path prefixes configuration (e.g., for languages)
type PathPrefixes struct {
	// values is the list of prefixes to prepend to paths (e.g., ["es", "fr", "it"])
	// +required
	// +kubebuilder:validation:MinItems=1
	Values []string `json:"values"`

	// policy defines how prefixes are applied
	// Optional: generates routes with and without prefix (default)
	// Required: generates routes only with prefix
	// Disabled: generates routes without any prefix
	// +optional
	// +kubebuilder:default=Optional
	Policy PathPrefixPolicy `json:"policy,omitempty"`
}

// PathMatch defines a path matching rule
type PathMatch struct {
	// path is the value to match against the request path
	// +required
	Path string `json:"path"`

	// type is the type of path matching
	// PathPrefix: matches paths starting with this value (default)
	// Exact: matches paths exactly equal to this value
	// Regex: matches paths using Go regexp syntax
	// +optional
	// +kubebuilder:default=PathPrefix
	Type MatchType `json:"type,omitempty"`

	// priority defines the order in which routes are evaluated
	// Higher values are evaluated first. Default is 1000.
	// +optional
	// +kubebuilder:default=1000
	Priority int32 `json:"priority,omitempty"`
}

// BackendRef defines a reference to a backend service
type BackendRef struct {
	// name is the name of the Service
	// +required
	Name string `json:"name"`

	// namespace is the namespace of the Service
	// +required
	Namespace string `json:"namespace"`

	// port is the port of the Service
	// +required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
}

// RulePathPrefixes defines path prefix overrides for a specific rule
type RulePathPrefixes struct {
	// policy overrides the spec-level pathPrefixes.policy for this rule
	// +required
	Policy PathPrefixPolicy `json:"policy"`
}

// TargetRef identifies the target external processor for this route
type TargetRef struct {
	// name is the identifier of the target external processor.
	// Routes with the same targetRef.name will be aggregated into the same ConfigMaps.
	// The external processor should be started with --target-name matching this value.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`
}

// Rule defines a routing rule
type Rule struct {
	// matches defines the conditions for matching this rule
	// +required
	// +kubebuilder:validation:MinItems=1
	Matches []PathMatch `json:"matches"`

	// backendRefs defines the backend services to route to
	// +required
	// +kubebuilder:validation:MinItems=1
	BackendRefs []BackendRef `json:"backendRefs"`

	// pathPrefixes overrides the spec-level pathPrefixes configuration for this rule
	// +optional
	PathPrefixes *RulePathPrefixes `json:"pathPrefixes,omitempty"`
}

// CustomHTTPRouteSpec defines the desired state of CustomHTTPRoute
type CustomHTTPRouteSpec struct {
	// targetRef identifies the target external processor for this route.
	// Routes are grouped by targetRef.name into separate ConfigMaps.
	// +required
	TargetRef TargetRef `json:"targetRef"`

	// hostnames is a list of hostnames that this route applies to
	// +required
	// +kubebuilder:validation:MinItems=1
	Hostnames []string `json:"hostnames"`

	// pathPrefixes defines prefixes to prepend to paths (e.g., language prefixes)
	// +optional
	PathPrefixes *PathPrefixes `json:"pathPrefixes,omitempty"`

	// rules defines the routing rules
	// +required
	// +kubebuilder:validation:MinItems=1
	Rules []Rule `json:"rules"`
}

// CustomHTTPRouteStatus defines the observed state of CustomHTTPRoute.
type CustomHTTPRouteStatus struct {
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Target",type="string",JSONPath=".spec.targetRef.name",description="Target external processor"
// +kubebuilder:printcolumn:name="Reconciled",type="string",JSONPath=".status.conditions[?(@.type=='Reconciled')].status",description="Whether the manifest was reconciled"
// +kubebuilder:printcolumn:name="ConfigMapSynced",type="string",JSONPath=".status.conditions[?(@.type=='ConfigMapSynced')].status",description="Whether the ConfigMap was synced"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// CustomHTTPRoute is the Schema for the customhttproutes API
type CustomHTTPRoute struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of CustomHTTPRoute
	// +required
	Spec CustomHTTPRouteSpec `json:"spec"`

	// status defines the observed state of CustomHTTPRoute
	// +optional
	Status CustomHTTPRouteStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CustomHTTPRouteList contains a list of CustomHTTPRoute
type CustomHTTPRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CustomHTTPRoute `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CustomHTTPRoute{}, &CustomHTTPRouteList{})
}
