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

// Package routes provides shared types and utilities for the customrouter project.
// These types are used by both the controller (to generate routes) and the extproc (to serve them).
package routes

import (
	"encoding/json"
	"regexp"
	"strings"
)

// RouteAction represents an action to perform on a matched request
type RouteAction struct {
	Type string `json:"type"` // "redirect", "rewrite", "header-set", "header-add", "header-remove"

	// For redirect
	RedirectScheme             string `json:"redirectScheme,omitempty"`
	RedirectHostname           string `json:"redirectHostname,omitempty"`
	RedirectPath               string `json:"redirectPath,omitempty"`
	RedirectPort               int32  `json:"redirectPort,omitempty"`
	RedirectStatusCode         int32  `json:"redirectStatusCode,omitempty"`
	RedirectReplacePrefixMatch *bool  `json:"redirectReplacePrefixMatch,omitempty"`

	// For rewrite
	RewritePath               string `json:"rewritePath,omitempty"`
	RewriteHostname           string `json:"rewriteHostname,omitempty"`
	RewriteReplacePrefixMatch *bool  `json:"rewriteReplacePrefixMatch,omitempty"`

	// For header operations
	HeaderName string `json:"headerName,omitempty"`
	Value      string `json:"value,omitempty"`

	// preservePrefix is an expansion-time flag, not serialized to JSON.
	// When true, the prefix from pathPrefixes expansion is prepended to the
	// rewrite/redirect path for prefixed routes.
	preservePrefix bool
}

// HeaderMatchExact and HeaderMatchRegex are the comparison modes for RouteHeaderMatch.
const (
	HeaderMatchExact = "exact"
	HeaderMatchRegex = "regex"
)

// RouteHeaderMatch represents a single header matching criterion on a Route.
// It mirrors the API's HeaderMatch but lives in the runtime package so the
// extproc binary has no direct dependency on the API v1alpha1 types.
type RouteHeaderMatch struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	// Type is one of HeaderMatchExact (default, case-sensitive) or HeaderMatchRegex.
	Type string `json:"type,omitempty"`

	// compiledRegex is populated during CompileRegexes() for Type=regex. Not serialized.
	compiledRegex *regexp.Regexp
}

// RouteQueryParamMatch represents a single query parameter matching criterion.
// Parameter names are compared case-sensitively per RFC 3986.
type RouteQueryParamMatch struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	// Type is one of HeaderMatchExact (default) or HeaderMatchRegex. Shared with
	// header matches since the comparison semantics are identical.
	Type string `json:"type,omitempty"`

	// compiledRegex is populated during CompileRegexes() for Type=regex. Not serialized.
	compiledRegex *regexp.Regexp
}

// Route represents a single expanded route for the proxy
type Route struct {
	Path     string        `json:"path"`
	Type     string        `json:"type"` // "exact", "prefix", "regex"
	Backend  string        `json:"backend"`
	Priority int32         `json:"priority"`
	Actions  []RouteAction `json:"actions,omitempty"`

	// Method restricts the route to a specific HTTP method (e.g. "GET").
	// Empty means any method matches. Case-insensitive comparison at match time.
	Method string `json:"method,omitempty"`

	// Headers are the header matching criteria. All listed headers must be
	// satisfied by the request (AND). Empty means no header constraint.
	Headers []RouteHeaderMatch `json:"headers,omitempty"`

	// QueryParams are the query parameter matching criteria. All listed params
	// must be satisfied by the request (AND). Empty means no query constraint.
	QueryParams []RouteQueryParamMatch `json:"queryParams,omitempty"`

	// compiledRegex is the compiled regex for regex type routes (not serialized)
	compiledRegex *regexp.Regexp
}

// RequestMatch carries the per-request inputs used to match a Route.
// All fields are optional; empty fields act as "match any" for that dimension.
type RequestMatch struct {
	Path        string
	Method      string
	Headers     map[string]string // keys MUST be lowercased by caller
	QueryParams map[string]string // case-sensitive keys (RFC 3986)
}

// RoutesConfig is the top-level structure for the ConfigMap data
type RoutesConfig struct {
	Version int                `json:"version"`
	Hosts   map[string][]Route `json:"hosts"`
}

// RouteType constants
const (
	RouteTypeExact  = "exact"
	RouteTypePrefix = "prefix"
	RouteTypeRegex  = "regex"
)

// ActionType constants
const (
	ActionTypeRedirect     = "redirect"
	ActionTypeRewrite      = "rewrite"
	ActionTypeHeaderSet    = "header-set"
	ActionTypeHeaderAdd    = "header-add"
	ActionTypeHeaderRemove = "header-remove"
)

// ParseJSON parses a JSON byte slice into a RoutesConfig
func ParseJSON(data []byte) (*RoutesConfig, error) {
	var config RoutesConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

// ToJSON serializes the routes config to compact JSON (no indentation)
// to minimize ConfigMap size and ensure accurate size calculations
func (rc *RoutesConfig) ToJSON() ([]byte, error) {
	return json.Marshal(rc)
}

// CompileRegexes compiles all regex patterns in the routes config (both path
// regex routes and header matches with Type=regex). Should be called after
// loading the config.
func (rc *RoutesConfig) CompileRegexes() error {
	for host := range rc.Hosts {
		for i := range rc.Hosts[host] {
			route := &rc.Hosts[host][i]
			if route.Type == RouteTypeRegex {
				re, err := regexp.Compile(route.Path)
				if err != nil {
					return err
				}
				route.compiledRegex = re
			}
			for j := range route.Headers {
				h := &route.Headers[j]
				if h.Type == HeaderMatchRegex {
					re, err := regexp.Compile(h.Value)
					if err != nil {
						return err
					}
					h.compiledRegex = re
				}
			}
			for j := range route.QueryParams {
				q := &route.QueryParams[j]
				if q.Type == HeaderMatchRegex {
					re, err := regexp.Compile(q.Value)
					if err != nil {
						return err
					}
					q.compiledRegex = re
				}
			}
		}
	}
	return nil
}

// Match checks if the given request matches this route. All match criteria
// (path, method, headers, ...) are AND-combined; an empty criterion on the
// Route means "match any value for this dimension".
func (r *Route) Match(req RequestMatch) bool {
	if !r.matchMethod(req.Method) {
		return false
	}
	if !r.matchHeaders(req.Headers) {
		return false
	}
	if !r.matchQueryParams(req.QueryParams) {
		return false
	}
	return r.matchPath(req.Path)
}

// matchPath evaluates only the path portion of the match.
func (r *Route) matchPath(path string) bool {
	switch r.Type {
	case RouteTypeExact:
		return path == r.Path
	case RouteTypePrefix:
		if strings.HasPrefix(path, r.Path) {
			// Ensure match is on a complete path segment boundary per Gateway API spec.
			// "/app" must match "/app", "/app/" but NOT "/app-settings".
			rest := path[len(r.Path):]
			if len(rest) == 0 || rest[0] == '/' || strings.HasSuffix(r.Path, "/") {
				return true
			}
		}
		// Match path without trailing slash, consistent with Gateway API HTTPRoute behavior.
		// A prefix "/audio/download/" should also match "/audio/download".
		if strings.HasSuffix(r.Path, "/") && path == strings.TrimSuffix(r.Path, "/") {
			return true
		}
		return false
	case RouteTypeRegex:
		if r.compiledRegex != nil {
			return r.compiledRegex.MatchString(path)
		}
		// Fallback: compile on the fly (slower)
		re, err := regexp.Compile(r.Path)
		if err != nil {
			return false
		}
		return re.MatchString(path)
	default:
		return false
	}
}

// matchMethod returns true when the route has no method restriction or the
// request method matches it (case-insensitive).
func (r *Route) matchMethod(method string) bool {
	if r.Method == "" {
		return true
	}
	return strings.EqualFold(r.Method, method)
}

// matchHeaders returns true when every required RouteHeaderMatch on the route
// is satisfied by the request headers. Header names are matched case-insensitively.
// An Exact match compares values case-sensitively per RFC 7230 semantics; a
// regex match uses the compiled pattern (falling back to on-the-fly compilation
// if CompileRegexes was not called).
func (r *Route) matchHeaders(requestHeaders map[string]string) bool {
	if len(r.Headers) == 0 {
		return true
	}
	for i := range r.Headers {
		h := &r.Headers[i]
		reqValue, ok := requestHeaders[strings.ToLower(h.Name)]
		if !ok {
			return false
		}
		switch h.Type {
		case HeaderMatchRegex:
			if h.compiledRegex != nil {
				if !h.compiledRegex.MatchString(reqValue) {
					return false
				}
				continue
			}
			re, err := regexp.Compile(h.Value)
			if err != nil {
				return false
			}
			if !re.MatchString(reqValue) {
				return false
			}
		default:
			if reqValue != h.Value {
				return false
			}
		}
	}
	return true
}

// matchQueryParams returns true when every required RouteQueryParamMatch on
// the route is satisfied by the request query parameters. Parameter names are
// matched case-sensitively (RFC 3986).
func (r *Route) matchQueryParams(requestParams map[string]string) bool {
	if len(r.QueryParams) == 0 {
		return true
	}
	for i := range r.QueryParams {
		q := &r.QueryParams[i]
		reqValue, ok := requestParams[q.Name]
		if !ok {
			return false
		}
		switch q.Type {
		case HeaderMatchRegex:
			if q.compiledRegex != nil {
				if !q.compiledRegex.MatchString(reqValue) {
					return false
				}
				continue
			}
			re, err := regexp.Compile(q.Value)
			if err != nil {
				return false
			}
			if !re.MatchString(reqValue) {
				return false
			}
		default:
			if reqValue != q.Value {
				return false
			}
		}
	}
	return true
}

// ParseBackend parses the backend string into host and port
// Backend format: "service.namespace.svc.cluster.local:port"
func (r *Route) ParseBackend() (host string, port string) {
	parts := strings.Split(r.Backend, ":")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return r.Backend, "80"
}
