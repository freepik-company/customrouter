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
// It excludes self by UID to allow updates.
func (c *HostnameChecker) CheckCustomHTTPRouteHostnames(ctx context.Context, route *customrouterv1alpha1.CustomHTTPRoute) error {
	hostnames := route.Spec.Hostnames
	if len(hostnames) == 0 {
		return nil
	}
	hostnameSet := toSet(hostnames)

	// Check against other CustomHTTPRoutes with the same targetRef
	var customRoutes customrouterv1alpha1.CustomHTTPRouteList
	if err := c.Client.List(ctx, &customRoutes); err != nil {
		return fmt.Errorf("listing CustomHTTPRoutes: %w", err)
	}

	routePaths := extractPathMatches(route)

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
		// Same target + same hostname: only conflict if path matches overlap
		otherPaths := extractPathMatches(other)
		if pathConflicts := findPathMatchOverlap(routePaths, otherPaths); len(pathConflicts) > 0 {
			return fmt.Errorf(
				"path conflict on hostnames %v: %v already defined in CustomHTTPRoute %s (target %q)",
				hostConflicts, pathConflicts, formatNamespacedName(other), route.Spec.TargetRef.Name,
			)
		}
	}

	// Check against HTTPRoutes (any hostname overlap is a conflict, regardless of target)
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
		if conflicts := findOverlap(hostnameSet, hrHostnames); len(conflicts) > 0 {
			return fmt.Errorf(
				"hostname conflict: %v already defined in HTTPRoute %s/%s",
				conflicts, hr.Namespace, hr.Name,
			)
		}
	}

	return nil
}

// CheckHTTPRouteHostnames checks whether any hostname in the given HTTPRoute
// conflicts with an existing CustomHTTPRoute.
func (c *HostnameChecker) CheckHTTPRouteHostnames(ctx context.Context, httpRoute *gatewayv1.HTTPRoute) error {
	hrHostnames := gatewayHostnames(httpRoute)
	if len(hrHostnames) == 0 {
		return nil
	}
	hostnameSet := toSet(hrHostnames)

	var customRoutes customrouterv1alpha1.CustomHTTPRouteList
	if err := c.Client.List(ctx, &customRoutes); err != nil {
		return fmt.Errorf("listing CustomHTTPRoutes: %w", err)
	}

	for i := range customRoutes.Items {
		cr := &customRoutes.Items[i]
		if conflicts := findOverlap(hostnameSet, cr.Spec.Hostnames); len(conflicts) > 0 {
			return fmt.Errorf(
				"hostname conflict: %v already defined in CustomHTTPRoute %s",
				conflicts, formatNamespacedName(cr),
			)
		}
	}

	return nil
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

// pathMatchKey uniquely identifies a path match by type and path value.
type pathMatchKey struct {
	Type string
	Path string
}

func (p pathMatchKey) String() string {
	return fmt.Sprintf("%s:%s", p.Type, p.Path)
}

// extractPathMatches returns all unique (type, path) pairs from a CustomHTTPRoute.
func extractPathMatches(route *customrouterv1alpha1.CustomHTTPRoute) map[pathMatchKey]struct{} {
	matches := make(map[pathMatchKey]struct{})
	for _, rule := range route.Spec.Rules {
		for _, m := range rule.Matches {
			matches[pathMatchKey{Type: string(m.Type), Path: m.Path}] = struct{}{}
		}
	}
	return matches
}

// findPathMatchOverlap returns the path matches that exist in both sets.
func findPathMatchOverlap(a, b map[pathMatchKey]struct{}) []pathMatchKey {
	var overlap []pathMatchKey
	for pm := range a {
		if _, ok := b[pm]; ok {
			overlap = append(overlap, pm)
		}
	}
	return overlap
}

// formatNamespacedName returns "namespace/name" for a CustomHTTPRoute.
func formatNamespacedName(cr *customrouterv1alpha1.CustomHTTPRoute) string {
	return types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}.String()
}
