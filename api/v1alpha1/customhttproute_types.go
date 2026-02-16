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

// ActionType defines the type of action to perform
// +kubebuilder:validation:Enum=redirect;rewrite;header-set;header-add;header-remove
type ActionType string

const (
	// ActionTypeRedirect returns an HTTP redirect response to the client
	ActionTypeRedirect ActionType = "redirect"

	// ActionTypeRewrite rewrites the request path and/or hostname before forwarding
	ActionTypeRewrite ActionType = "rewrite"

	// ActionTypeHeaderSet sets a header, overwriting if it exists
	ActionTypeHeaderSet ActionType = "header-set"

	// ActionTypeHeaderAdd adds a header value, appending if it exists
	ActionTypeHeaderAdd ActionType = "header-add"

	// ActionTypeHeaderRemove removes a header
	ActionTypeHeaderRemove ActionType = "header-remove"
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
	// +optional
	Values []string `json:"values,omitempty"`

	// policy defines how prefixes are applied
	// Optional: generates routes with and without prefix (default)
	// Required: generates routes only with prefix
	// Disabled: generates routes without any prefix
	// +optional
	// +kubebuilder:default=Optional
	Policy PathPrefixPolicy `json:"policy,omitempty"`

	// expandMatchTypes controls which match types are expanded with path prefixes.
	// Accepts a list of match types: "PathPrefix", "Exact", "Regex".
	// When empty or not specified, all match types are expanded (default behavior).
	// Example: ["PathPrefix", "Exact"] expands only PathPrefix and Exact matches.
	// +optional
	ExpandMatchTypes []MatchType `json:"expandMatchTypes,omitempty"`
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

// RewriteConfig defines URL rewrite configuration
type RewriteConfig struct {
	// path is the new path to rewrite to. Supports variables:
	// ${path} - original request path
	// ${host} - original request host
	// ${method} - HTTP method (GET, POST, etc.)
	// ${scheme} - request scheme (http or https)
	// ${path.segment.N} - Nth segment of the path (0-indexed)
	//
	// For PathPrefix matches: if the path does not contain variables (${...}),
	// only the matched prefix is replaced and the remaining suffix and query
	// parameters are preserved (prefix rewrite). If the path contains variables,
	// the entire path is replaced (full rewrite).
	//
	// This automatic behavior can be overridden with replacePrefixMatch.
	// +optional
	Path string `json:"path,omitempty"`

	// replacePrefixMatch explicitly controls whether prefix rewrite is used.
	// When true, only the matched prefix is replaced and the remaining path
	// suffix and query parameters are preserved. When false, the entire path
	// is replaced. When not set, the behavior is inferred automatically:
	// prefix rewrite for PathPrefix matches without variables, full rewrite otherwise.
	// +optional
	ReplacePrefixMatch *bool `json:"replacePrefixMatch,omitempty"`

	// hostname is the new hostname to rewrite to
	// +optional
	Hostname string `json:"hostname,omitempty"`
}

// RedirectConfig defines HTTP redirect configuration
type RedirectConfig struct {
	// scheme is the scheme to redirect to (http or https)
	// +optional
	// +kubebuilder:validation:Enum=http;https
	Scheme string `json:"scheme,omitempty"`

	// hostname is the hostname to redirect to
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// path is the path to redirect to. Supports variables:
	// ${path} - original request path
	// ${host} - original request host
	// ${method} - HTTP method (GET, POST, etc.)
	// ${scheme} - request scheme (http or https)
	// ${path.segment.N} - Nth segment of the path (0-indexed)
	// +optional
	Path string `json:"path,omitempty"`

	// port is the port to redirect to
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port *int32 `json:"port,omitempty"`

	// statusCode is the HTTP status code to use for the redirect
	// +optional
	// +kubebuilder:default=302
	// +kubebuilder:validation:Enum=301;302;303;307;308
	StatusCode int32 `json:"statusCode,omitempty"`
}

// HeaderConfig defines a header name-value pair
type HeaderConfig struct {
	// name is the header name
	// +required
	Name string `json:"name"`

	// value is the header value. Supports variables:
	// ${client_ip} - client IP address from X-Forwarded-For
	// ${request_id} - request ID from X-Request-ID header
	// ${host} - original request host
	// ${path} - original request path
	// ${method} - HTTP method (GET, POST, etc.)
	// ${scheme} - request scheme (http or https)
	// +required
	Value string `json:"value"`
}

// Action defines an action to perform on a matched request
type Action struct {
	// type is the type of action to perform
	// +required
	Type ActionType `json:"type"`

	// redirect specifies redirect configuration (required when type is "redirect")
	// When a redirect action is present, the request is not forwarded to the backend
	// +optional
	Redirect *RedirectConfig `json:"redirect,omitempty"`

	// rewrite specifies URL rewrite configuration (required when type is "rewrite")
	// +optional
	Rewrite *RewriteConfig `json:"rewrite,omitempty"`

	// header specifies header configuration (required when type is "header-set" or "header-add")
	// +optional
	Header *HeaderConfig `json:"header,omitempty"`

	// headerName specifies the header name to remove (required when type is "header-remove")
	// +optional
	HeaderName string `json:"headerName,omitempty"`
}

// RulePathPrefixes defines path prefix overrides for a specific rule
type RulePathPrefixes struct {
	// policy overrides the spec-level pathPrefixes.policy for this rule
	// +required
	Policy PathPrefixPolicy `json:"policy"`

	// expandMatchTypes overrides the spec-level pathPrefixes.expandMatchTypes for this rule.
	// Accepts a list of match types: "PathPrefix", "Exact", "Regex".
	// When not specified, inherits from spec-level pathPrefixes.expandMatchTypes.
	// +optional
	ExpandMatchTypes []MatchType `json:"expandMatchTypes,omitempty"`
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

	// actions defines transformations to apply to matched requests
	// Actions are applied in order: redirect (terminates), rewrite, then header modifications
	// +optional
	Actions []Action `json:"actions,omitempty"`

	// backendRefs defines the backend services to route to
	// Required unless actions contains a redirect action
	// +optional
	BackendRefs []BackendRef `json:"backendRefs,omitempty"`

	// pathPrefixes overrides the spec-level pathPrefixes configuration for this rule
	// +optional
	PathPrefixes *RulePathPrefixes `json:"pathPrefixes,omitempty"`
}

// CatchAllBackendRef defines the default backend for catch-all route generation.
// When specified on a CustomHTTPRoute, the operator will generate catch-all virtual hosts
// for the route's hostnames, allowing requests to be processed without requiring a base HTTPRoute.
type CatchAllBackendRef struct {
	// backendRef defines the default backend service to route unmatched requests to.
	// +required
	BackendRef BackendRef `json:"backendRef"`
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

	// catchAllRoute configures automatic generation of catch-all virtual hosts for this route's hostnames.
	// When specified, the operator generates an EnvoyFilter that creates default routes for the hostnames,
	// allowing CustomHTTPRoute to handle requests without requiring a base HTTPRoute.
	// The hostnames are taken from spec.hostnames; the backendRef defines the default backend.
	// +optional
	CatchAllRoute *CatchAllBackendRef `json:"catchAllRoute,omitempty"`

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
