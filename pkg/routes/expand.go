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

package routes

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

const (
	MaxRoutesPerCRD = 500_000
)

// ExpandRoutes expands a CustomHTTPRoute into a list of routes per host.
// It caps the total number of generated routes to MaxRoutesPerCRD to prevent
// resource exhaustion from overly large CRDs.
func ExpandRoutes(cr *v1alpha1.CustomHTTPRoute) (map[string][]Route, error) {
	hosts := make(map[string][]Route)

	numPrefixes := 0
	if cr.Spec.PathPrefixes != nil {
		numPrefixes = len(cr.Spec.PathPrefixes.Values)
	}
	var totalMatches int
	for _, rule := range cr.Spec.Rules {
		totalMatches += len(rule.Matches)
	}
	multiplier := numPrefixes + 1
	estimatedRoutes := len(cr.Spec.Hostnames) * totalMatches * multiplier
	if estimatedRoutes > MaxRoutesPerCRD {
		return nil, fmt.Errorf(
			"CustomHTTPRoute %s/%s would generate ~%d routes (limit %d): reduce hostnames, rules, matches, or prefixes",
			cr.Namespace, cr.Name, estimatedRoutes, MaxRoutesPerCRD,
		)
	}

	for _, hostname := range cr.Spec.Hostnames {
		var routes []Route

		for _, rule := range cr.Spec.Rules {
			ruleRoutes := expandRule(cr.Spec.PathPrefixes, &rule)
			routes = append(routes, ruleRoutes...)
		}

		SortRoutes(routes)

		hosts[hostname] = routes
	}

	return hosts, nil
}

// expandRule expands a single rule into multiple routes based on path prefixes
func expandRule(specPrefixes *v1alpha1.PathPrefixes, rule *v1alpha1.Rule) []Route {
	var routes []Route

	policy := getEffectivePolicy(specPrefixes, rule)
	expandTypes := getEffectiveExpandMatchTypes(specPrefixes, rule)

	var prefixes []string
	if specPrefixes != nil {
		prefixes = specPrefixes.Values
	}

	backend := buildBackendString(rule.BackendRefs)
	actions := convertActions(rule.Actions)

	for _, match := range rule.Matches {
		matchType := getMatchType(match.Type)
		priority := getEffectivePriority(match.Priority)

		shouldExpand := shouldExpandMatchType(match.Type, expandTypes)

		if !shouldExpand {
			routes = append(routes, Route{
				Path:     match.Path,
				Type:     matchType,
				Backend:  backend,
				Priority: priority,
				Actions:  actions,
			})
			continue
		}

		if match.Type == v1alpha1.MatchTypeRegex {
			expandedPath := expandRegexWithPrefixes(match.Path, prefixes, policy)
			routes = append(routes, Route{
				Path:     expandedPath,
				Type:     matchType,
				Backend:  backend,
				Priority: priority,
				Actions:  actions,
			})
			continue
		}

		// Exact and PathPrefix: expand by generating separate routes per prefix
		switch policy {
		case v1alpha1.PathPrefixPolicyDisabled:
			routes = append(routes, Route{
				Path:     match.Path,
				Type:     matchType,
				Backend:  backend,
				Priority: priority,
				Actions:  actions,
			})

		case v1alpha1.PathPrefixPolicyRequired:
			for _, prefix := range prefixes {
				routes = append(routes, Route{
					Path:     "/" + prefix + match.Path,
					Type:     matchType,
					Backend:  backend,
					Priority: priority,
					Actions:  actions,
				})
			}

		case v1alpha1.PathPrefixPolicyOptional:
			for _, prefix := range prefixes {
				routes = append(routes, Route{
					Path:     "/" + prefix + match.Path,
					Type:     matchType,
					Backend:  backend,
					Priority: priority,
					Actions:  actions,
				})
			}
			routes = append(routes, Route{
				Path:     match.Path,
				Type:     matchType,
				Backend:  backend,
				Priority: priority,
				Actions:  actions,
			})
		}
	}

	return routes
}

// convertActions converts API actions to route actions
func convertActions(apiActions []v1alpha1.Action) []RouteAction {
	if len(apiActions) == 0 {
		return nil
	}

	actions := make([]RouteAction, 0, len(apiActions))
	for _, a := range apiActions {
		action := RouteAction{
			Type: string(a.Type),
		}

		switch a.Type {
		case v1alpha1.ActionTypeRedirect:
			if a.Redirect != nil {
				action.RedirectScheme = a.Redirect.Scheme
				action.RedirectHostname = a.Redirect.Hostname
				action.RedirectPath = a.Redirect.Path
				if a.Redirect.Port != nil {
					action.RedirectPort = *a.Redirect.Port
				}
				action.RedirectStatusCode = a.Redirect.StatusCode
				if action.RedirectStatusCode == 0 {
					action.RedirectStatusCode = 302
				}
			}
		case v1alpha1.ActionTypeRewrite:
			if a.Rewrite != nil {
				action.RewritePath = a.Rewrite.Path
				action.RewriteHostname = a.Rewrite.Hostname
				action.RewriteReplacePrefixMatch = a.Rewrite.ReplacePrefixMatch
			}
		case v1alpha1.ActionTypeHeaderSet, v1alpha1.ActionTypeHeaderAdd:
			if a.Header != nil {
				action.HeaderName = a.Header.Name
				action.Value = a.Header.Value
			}
		case v1alpha1.ActionTypeHeaderRemove:
			action.HeaderName = a.HeaderName
		}

		actions = append(actions, action)
	}

	return actions
}

// getEffectivePolicy returns the policy to use for a rule
func getEffectivePolicy(specPrefixes *v1alpha1.PathPrefixes, rule *v1alpha1.Rule) v1alpha1.PathPrefixPolicy {
	// Rule-level override takes precedence
	if rule.PathPrefixes != nil {
		return rule.PathPrefixes.Policy
	}

	// Fall back to spec-level policy
	if specPrefixes != nil && specPrefixes.Policy != "" {
		return specPrefixes.Policy
	}

	// Default is Optional
	return v1alpha1.PathPrefixPolicyOptional
}

// getEffectiveExpandMatchTypes returns the list of match types that should be expanded.
// Rule-level overrides spec-level. Empty list means expand all types (default).
func getEffectiveExpandMatchTypes(specPrefixes *v1alpha1.PathPrefixes, rule *v1alpha1.Rule) []v1alpha1.MatchType {
	if rule.PathPrefixes != nil && len(rule.PathPrefixes.ExpandMatchTypes) > 0 {
		return rule.PathPrefixes.ExpandMatchTypes
	}

	if specPrefixes != nil && len(specPrefixes.ExpandMatchTypes) > 0 {
		return specPrefixes.ExpandMatchTypes
	}

	return nil
}

// shouldExpandMatchType returns true if the given match type should be expanded with prefixes.
// When expandTypes is nil/empty, all types are expanded (default behavior).
func shouldExpandMatchType(matchType v1alpha1.MatchType, expandTypes []v1alpha1.MatchType) bool {
	if len(expandTypes) == 0 {
		return true
	}
	for _, t := range expandTypes {
		if t == matchType {
			return true
		}
	}
	return false
}

// getMatchType converts the API MatchType to string for JSON
func getMatchType(t v1alpha1.MatchType) string {
	switch t {
	case v1alpha1.MatchTypeExact:
		return RouteTypeExact
	case v1alpha1.MatchTypeRegex:
		return RouteTypeRegex
	default:
		return RouteTypePrefix
	}
}

// getEffectivePriority returns the priority to use, defaulting to DefaultPriority if not set
func getEffectivePriority(priority int32) int32 {
	if priority == 0 {
		return v1alpha1.DefaultPriority
	}
	return priority
}

// buildBackendString builds the backend address from BackendRefs
func buildBackendString(refs []v1alpha1.BackendRef) string {
	if len(refs) == 0 {
		return ""
	}
	// For now, use the first backend ref
	ref := refs[0]
	// If the name contains a dot, treat it as an external hostname
	// and don't append the .svc.cluster.local suffix
	if strings.Contains(ref.Name, ".") {
		return ref.Name + ":" + strconv.Itoa(int(ref.Port))
	}
	return ref.Name + "." + ref.Namespace + ".svc.cluster.local:" + strconv.Itoa(int(ref.Port))
}

// SortRoutes sorts routes by priority (descending), then by type, then by path length
func SortRoutes(routes []Route) {
	sort.Slice(routes, func(i, j int) bool {
		// First by priority descending (higher priority first)
		if routes[i].Priority != routes[j].Priority {
			return routes[i].Priority > routes[j].Priority
		}

		// Then by type priority: exact > regex > prefix
		typePriority := map[string]int{RouteTypeExact: 0, RouteTypeRegex: 1, RouteTypePrefix: 2}
		pi, pj := typePriority[routes[i].Type], typePriority[routes[j].Type]
		if pi != pj {
			return pi < pj
		}

		// Then by path length descending (longer paths first)
		return len(routes[i].Path) > len(routes[j].Path)
	})
}

// MergeRoutesConfig merges routes from multiple CustomHTTPRoutes into a single config
func MergeRoutesConfig(configs ...map[string][]Route) *RoutesConfig {
	result := &RoutesConfig{
		Version: 1,
		Hosts:   make(map[string][]Route),
	}

	for _, config := range configs {
		for host, routes := range config {
			if existing, ok := result.Hosts[host]; ok {
				result.Hosts[host] = append(existing, routes...)
			} else {
				result.Hosts[host] = routes
			}
		}
	}

	// Sort routes for each host after merging
	for host := range result.Hosts {
		SortRoutes(result.Hosts[host])
	}

	return result
}

// expandRegexWithPrefixes modifies a regex pattern to include language prefix matching.
// It handles the insertion point carefully to maintain regex validity.
//
// For policy Optional: ^/path$ becomes ^(?:/(es|fr|it))?/path$
// For policy Required: ^/path$ becomes ^/(es|fr|it)/path$
// For policy Disabled: returns the original regex unchanged
func expandRegexWithPrefixes(pattern string, prefixes []string, policy v1alpha1.PathPrefixPolicy) string {
	// No modification needed for disabled policy or empty prefixes
	if policy == v1alpha1.PathPrefixPolicyDisabled || len(prefixes) == 0 {
		return pattern
	}

	// Build the language alternation group
	langGroup := "(" + strings.Join(prefixes, "|") + ")"

	// If the pattern contains {prefix}, substitute it inline
	if strings.Contains(pattern, "{prefix}") {
		switch policy {
		case v1alpha1.PathPrefixPolicyRequired:
			return strings.ReplaceAll(pattern, "{prefix}", langGroup)
		case v1alpha1.PathPrefixPolicyOptional:
			return strings.ReplaceAll(pattern, "{prefix}", langGroup+"?")
		default:
			return pattern
		}
	}

	// Find where to insert the language prefix pattern
	// We need to insert after ^ (if present) and before the first /
	hasStartAnchor := strings.HasPrefix(pattern, "^")

	// Remove ^ temporarily for processing
	workPattern := pattern
	if hasStartAnchor {
		workPattern = pattern[1:]
	}

	// Check if pattern starts with /
	if !strings.HasPrefix(workPattern, "/") {
		// Pattern doesn't start with /, don't modify it
		return pattern
	}

	// Build the prefix pattern based on policy
	var prefixPattern string
	switch policy {
	case v1alpha1.PathPrefixPolicyRequired:
		// Must have a language prefix: /(es|fr|it)
		prefixPattern = "/" + langGroup
	case v1alpha1.PathPrefixPolicyOptional:
		// Language prefix is optional: (?:/(es|fr|it))?
		prefixPattern = "(?:/" + langGroup + ")?"
	default:
		return pattern
	}

	// Insert the prefix pattern after ^ and before the path
	var result string
	if hasStartAnchor {
		result = "^" + prefixPattern + workPattern
	} else {
		result = prefixPattern + workPattern
	}

	return result
}

// IsValidRegex checks if a regex pattern compiles successfully
func IsValidRegex(pattern string) bool {
	_, err := regexp.Compile(pattern)
	return err == nil
}
