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

package webhook

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	customrouterv1alpha1 "github.com/freepik-company/customrouter/api/v1alpha1"
)

// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch

// HostnameChecker detects hostname conflicts between CustomHTTPRoutes and HTTPRoutes.
type HostnameChecker struct {
	Client client.Reader
}

// CheckCustomHTTPRouteHostnames checks whether any hostname in the given CustomHTTPRoute
// conflicts with another CustomHTTPRoute (same targetRef) or any HTTPRoute in the cluster.
// A conflict requires overlapping hostnames AND overlapping route matches
// (path + method + headers + query parameters). It excludes self by UID to allow updates.
func (c *HostnameChecker) CheckCustomHTTPRouteHostnames(ctx context.Context, route *customrouterv1alpha1.CustomHTTPRoute) error {
	hostnames := route.Spec.Hostnames
	if len(hostnames) == 0 {
		return nil
	}
	hostnameSet := toSet(hostnames)
	routeMatches := extractCustomRouteMatches(route)

	// Check against other CustomHTTPRoutes with the same targetRef
	var customRoutes customrouterv1alpha1.CustomHTTPRouteList
	if err := c.Client.List(ctx, &customRoutes); err != nil {
		return fmt.Errorf("listing CustomHTTPRoutes: %w", err)
	}

	for i := range customRoutes.Items {
		other := &customRoutes.Items[i]
		if other.UID == route.UID {
			continue
		}
		if other.Spec.TargetRef.Name != route.Spec.TargetRef.Name {
			continue
		}
		hostConflicts := findOverlap(hostnameSet, other.Spec.Hostnames)
		if len(hostConflicts) == 0 {
			continue
		}
		// Same target + same hostname: only conflict if route matches overlap
		otherMatches := extractCustomRouteMatches(other)
		if matchConflicts := findRouteMatchOverlap(routeMatches, otherMatches); len(matchConflicts) > 0 {
			return fmt.Errorf(
				"route conflict on hostnames %v: %v already defined in CustomHTTPRoute %s (target %q)",
				hostConflicts, matchConflicts, formatNamespacedName(other), route.Spec.TargetRef.Name,
			)
		}
	}

	// Check against HTTPRoutes (hostname + path + header overlap is a conflict)
	var httpRoutes gatewayv1.HTTPRouteList
	if err := c.Client.List(ctx, &httpRoutes); err != nil {
		return fmt.Errorf("listing HTTPRoutes: %w", err)
	}

	for i := range httpRoutes.Items {
		hr := &httpRoutes.Items[i]
		hrHostnames := gatewayHostnames(hr)
		if len(hrHostnames) == 0 {
			continue
		}
		hostConflicts := findOverlap(hostnameSet, hrHostnames)
		if len(hostConflicts) == 0 {
			continue
		}
		hrMatches := extractHTTPRouteMatches(hr)
		if matchConflicts := findRouteMatchOverlap(routeMatches, hrMatches); len(matchConflicts) > 0 {
			return fmt.Errorf(
				"route conflict on hostnames %v: %v already defined in HTTPRoute %s/%s",
				hostConflicts, matchConflicts, hr.Namespace, hr.Name,
			)
		}
	}

	return nil
}

// CheckHTTPRouteHostnames checks whether any hostname in the given HTTPRoute
// conflicts with an existing CustomHTTPRoute.
// A conflict requires overlapping hostnames AND overlapping route matches
// (path + method + headers + query parameters).
func (c *HostnameChecker) CheckHTTPRouteHostnames(ctx context.Context, httpRoute *gatewayv1.HTTPRoute) error {
	hrHostnames := gatewayHostnames(httpRoute)
	if len(hrHostnames) == 0 {
		return nil
	}
	hostnameSet := toSet(hrHostnames)
	hrMatches := extractHTTPRouteMatches(httpRoute)

	var customRoutes customrouterv1alpha1.CustomHTTPRouteList
	if err := c.Client.List(ctx, &customRoutes); err != nil {
		return fmt.Errorf("listing CustomHTTPRoutes: %w", err)
	}

	for i := range customRoutes.Items {
		cr := &customRoutes.Items[i]
		hostConflicts := findOverlap(hostnameSet, cr.Spec.Hostnames)
		if len(hostConflicts) == 0 {
			continue
		}
		crMatches := extractCustomRouteMatches(cr)
		if matchConflicts := findRouteMatchOverlap(hrMatches, crMatches); len(matchConflicts) > 0 {
			return fmt.Errorf(
				"route conflict on hostnames %v: %v already defined in CustomHTTPRoute %s",
				hostConflicts, matchConflicts, formatNamespacedName(cr),
			)
		}
	}

	return nil
}

// routeMatch represents a single match criterion within a routing rule.
// Two routeMatches conflict when they could match the same HTTP request,
// determined by: path + method + headers + query parameters.
type routeMatch struct {
	PathType    string
	Path        string
	Method      string
	Headers     []headerMatch
	QueryParams []queryParamMatch
}

func (r routeMatch) String() string {
	var parts []string

	base := fmt.Sprintf("%s:%s", r.PathType, r.Path)
	if r.Method != "" {
		base = fmt.Sprintf("%s %s:%s", r.Method, r.PathType, r.Path)
	}
	parts = append(parts, base)

	if len(r.Headers) > 0 {
		hdrs := make([]string, len(r.Headers))
		for i, h := range r.Headers {
			hdrs[i] = fmt.Sprintf("%s=%s", h.Name, h.Value)
		}
		parts = append(parts, fmt.Sprintf("headers[%s]", strings.Join(hdrs, ",")))
	}
	if len(r.QueryParams) > 0 {
		qps := make([]string, len(r.QueryParams))
		for i, q := range r.QueryParams {
			qps[i] = fmt.Sprintf("%s=%s", q.Name, q.Value)
		}
		parts = append(parts, fmt.Sprintf("params[%s]", strings.Join(qps, ",")))
	}

	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts, " ")
}

// headerMatch represents an HTTP header matching criterion.
type headerMatch struct {
	Name  string
	Value string
}

// queryParamMatch represents an HTTP query parameter matching criterion.
type queryParamMatch struct {
	Name  string
	Value string
}

// extractCustomRouteMatches returns all unique route matches from a CustomHTTPRoute.
// CustomHTTPRoute does not support Method, header, or query parameter matching,
// so those fields are always empty (which means "matches all" during comparison).
func extractCustomRouteMatches(route *customrouterv1alpha1.CustomHTTPRoute) []routeMatch {
	seen := make(map[string]struct{})
	var matches []routeMatch
	for _, rule := range route.Spec.Rules {
		for _, m := range rule.Matches {
			path := normalizePath(m.Path)
			key := string(m.Type) + ":" + path
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			matches = append(matches, routeMatch{
				PathType: string(m.Type),
				Path:     path,
			})
		}
	}
	return matches
}

// extractHTTPRouteMatches returns all route matches from a Gateway API HTTPRoute,
// including path, method, header, and query parameter criteria. A rule with no
// matches defaults to PathPrefix "/" (Gateway API default). An HTTPRoute with no
// rules also defaults to PathPrefix "/" as a catch-all.
func extractHTTPRouteMatches(hr *gatewayv1.HTTPRoute) []routeMatch {
	var matches []routeMatch
	for _, rule := range hr.Spec.Rules {
		if len(rule.Matches) == 0 {
			// Gateway API default: a rule with no matches is a catch-all
			matches = append(matches, routeMatch{
				PathType: string(gatewayv1.PathMatchPathPrefix),
				Path:     "/",
			})
			continue
		}
		for _, m := range rule.Matches {
			rm := routeMatch{
				PathType: string(gatewayv1.PathMatchPathPrefix),
				Path:     "/",
			}
			if m.Path != nil {
				if m.Path.Type != nil {
					rm.PathType = string(*m.Path.Type)
				}
				if m.Path.Value != nil {
					rm.Path = normalizePath(*m.Path.Value)
				}
			}
			if m.Method != nil {
				rm.Method = string(*m.Method)
			}
			for _, h := range m.Headers {
				rm.Headers = append(rm.Headers, headerMatch{
					Name:  string(h.Name),
					Value: h.Value,
				})
			}
			sort.Slice(rm.Headers, func(i, j int) bool {
				return strings.ToLower(rm.Headers[i].Name) < strings.ToLower(rm.Headers[j].Name)
			})
			for _, q := range m.QueryParams {
				rm.QueryParams = append(rm.QueryParams, queryParamMatch{
					Name:  string(q.Name),
					Value: q.Value,
				})
			}
			sort.Slice(rm.QueryParams, func(i, j int) bool {
				return strings.ToLower(rm.QueryParams[i].Name) < strings.ToLower(rm.QueryParams[j].Name)
			})
			matches = append(matches, rm)
		}
	}
	// An HTTPRoute with no rules defaults to a catch-all
	if len(hr.Spec.Rules) == 0 {
		matches = append(matches, routeMatch{
			PathType: string(gatewayv1.PathMatchPathPrefix),
			Path:     "/",
		})
	}
	return matches
}

// findRouteMatchOverlap returns matches from a that overlap with matches in b.
// Two matches overlap when they have the same path type and path value,
// compatible methods, compatible headers, and compatible query parameters
// (i.e. they could match the same HTTP request).
func findRouteMatchOverlap(a, b []routeMatch) []routeMatch {
	var overlaps []routeMatch
	for _, ma := range a {
		for _, mb := range b {
			if ma.PathType == mb.PathType && ma.Path == mb.Path &&
				methodsCompatible(ma.Method, mb.Method) &&
				headersCompatible(ma.Headers, mb.Headers) &&
				queryParamsCompatible(ma.QueryParams, mb.QueryParams) {
				overlaps = append(overlaps, ma)
				break
			}
		}
	}
	return overlaps
}

// headersCompatible returns true if two sets of header matches could match the
// same HTTP request. An empty header set matches all requests, so it is always
// compatible. Two non-empty sets are incompatible only when they require
// different values for the same header name (case-insensitive).
func headersCompatible(a, b []headerMatch) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	bMap := make(map[string]string, len(b))
	for _, h := range b {
		bMap[strings.ToLower(h.Name)] = h.Value
	}
	for _, h := range a {
		if bVal, ok := bMap[strings.ToLower(h.Name)]; ok && bVal != h.Value {
			return false
		}
	}
	return true
}

// normalizePath strips a single trailing slash from a path to prevent false
// negatives (e.g. "/api" vs "/api/"). The root path "/" is preserved as-is.
func normalizePath(p string) string {
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		return strings.TrimRight(p, "/")
	}
	return p
}

// methodsCompatible returns true if two method constraints could match the same
// HTTP request. An empty method means "matches all methods", so it is always
// compatible. Two non-empty methods are incompatible only when they differ.
func methodsCompatible(a, b string) bool {
	if a == "" || b == "" {
		return true
	}
	return strings.EqualFold(a, b)
}

// queryParamsCompatible returns true if two sets of query parameter matches could
// match the same HTTP request. An empty set matches all requests, so it is always
// compatible. Two non-empty sets are incompatible only when they require different
// values for the same parameter name (case-insensitive).
func queryParamsCompatible(a, b []queryParamMatch) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	bMap := make(map[string]string, len(b))
	for _, q := range b {
		bMap[strings.ToLower(q.Name)] = q.Value
	}
	for _, q := range a {
		if bVal, ok := bMap[strings.ToLower(q.Name)]; ok && bVal != q.Value {
			return false
		}
	}
	return true
}

// gatewayHostnames extracts hostnames from an HTTPRoute, converting from gateway-api's Hostname type.
func gatewayHostnames(hr *gatewayv1.HTTPRoute) []string {
	if len(hr.Spec.Hostnames) == 0 {
		return nil
	}
	out := make([]string, len(hr.Spec.Hostnames))
	for i, h := range hr.Spec.Hostnames {
		out[i] = string(h)
	}
	return out
}

// toSet converts a slice of strings to a map for O(1) lookup.
func toSet(items []string) map[string]struct{} {
	s := make(map[string]struct{}, len(items))
	for _, item := range items {
		s[item] = struct{}{}
	}
	return s
}

// findOverlap returns the items from candidates that exist in the set.
func findOverlap(set map[string]struct{}, candidates []string) []string {
	var overlap []string
	for _, c := range candidates {
		if _, ok := set[c]; ok {
			overlap = append(overlap, c)
		}
	}
	return overlap
}

// formatNamespacedName returns "namespace/name" for a CustomHTTPRoute.
func formatNamespacedName(cr *customrouterv1alpha1.CustomHTTPRoute) string {
	return types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}.String()
}
