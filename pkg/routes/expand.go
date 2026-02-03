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
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

// ExpandRoutes expands a CustomHTTPRoute into a list of routes per host
func ExpandRoutes(cr *v1alpha1.CustomHTTPRoute) map[string][]Route {
	hosts := make(map[string][]Route)

	for _, hostname := range cr.Spec.Hostnames {
		var routes []Route

		for _, rule := range cr.Spec.Rules {
			ruleRoutes := expandRule(cr.Spec.PathPrefixes, &rule)
			routes = append(routes, ruleRoutes...)
		}

		// Sort routes by specificity: Exact > Regex > Prefix, then by path length desc
		SortRoutes(routes)

		hosts[hostname] = routes
	}

	return hosts
}

// expandRule expands a single rule into multiple routes based on path prefixes
func expandRule(specPrefixes *v1alpha1.PathPrefixes, rule *v1alpha1.Rule) []Route {
	var routes []Route

	// Determine the effective policy for this rule
	policy := getEffectivePolicy(specPrefixes, rule)

	// Get prefixes to apply
	var prefixes []string
	if specPrefixes != nil {
		prefixes = specPrefixes.Values
	}

	// Build backend string
	backend := buildBackendString(rule.BackendRefs)

	// Convert actions from API type to routes type
	actions := convertActions(rule.Actions)

	for _, match := range rule.Matches {
		matchType := getMatchType(match.Type)
		priority := getEffectivePriority(match.Priority)

		// Exact type: no expansion, use literal path
		if match.Type == v1alpha1.MatchTypeExact {
			routes = append(routes, Route{
				Path:     match.Path,
				Type:     matchType,
				Backend:  backend,
				Priority: priority,
				Actions:  actions,
			})
			continue
		}

		// Regex type: expand by modifying the regex pattern
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

		// PathPrefix: expand based on policy
		switch policy {
		case v1alpha1.PathPrefixPolicyDisabled:
			// Only the literal path
			routes = append(routes, Route{
				Path:     match.Path,
				Type:     matchType,
				Backend:  backend,
				Priority: priority,
				Actions:  actions,
			})

		case v1alpha1.PathPrefixPolicyRequired:
			// Only with prefixes, not without
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
			// With prefixes AND without
			for _, prefix := range prefixes {
				routes = append(routes, Route{
					Path:     "/" + prefix + match.Path,
					Type:     matchType,
					Backend:  backend,
					Priority: priority,
					Actions:  actions,
				})
			}
			// Also add the path without prefix
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
