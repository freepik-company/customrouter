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
func ExpandRoutes(cr *v1alpha1.CustomHTTPRoute, externalNames map[string]string) (map[string][]Route, error) {
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
			ruleRoutes := expandRule(cr.Spec.PathPrefixes, &rule, externalNames)
			routes = append(routes, ruleRoutes...)
		}

		SortRoutes(routes)

		hosts[hostname] = routes
	}

	return hosts, nil
}

// expandRule expands a single rule into multiple routes based on path prefixes
func expandRule(specPrefixes *v1alpha1.PathPrefixes, rule *v1alpha1.Rule, externalNames map[string]string) []Route {
	var routes []Route

	policy := GetEffectivePolicy(specPrefixes, rule)
	expandTypes := GetEffectiveExpandMatchTypes(specPrefixes, rule)

	var prefixes []string
	if specPrefixes != nil {
		prefixes = specPrefixes.Values
	}

	backend := buildBackendString(rule.BackendRefs, externalNames)
	actions := convertActions(rule.Actions)
	mirrors := extractMirrors(rule.Actions)
	cors := extractCORS(rule.Actions)

	for _, match := range rule.Matches {
		matchType := getMatchType(match.Type)
		priority := getEffectivePriority(match.Priority)

		shouldExpand := ShouldExpandMatchType(match.Type, expandTypes)

		method := string(match.Method)
		headers := convertHeaderMatches(match.Headers)
		queryParams := convertQueryParamMatches(match.QueryParams)

		if !shouldExpand {
			routes = append(routes, Route{
				Path:        match.Path,
				Type:        matchType,
				Backend:     backend,
				Priority:    priority,
				Actions:     actions,
				Method:      method,
				Headers:     headers,
				QueryParams: queryParams,
			})
			continue
		}

		if match.Type == v1alpha1.MatchTypeRegex {
			expandedPath := ExpandRegexWithPrefixes(match.Path, prefixes, policy)
			routes = append(routes, Route{
				Path:        expandedPath,
				Type:        matchType,
				Backend:     backend,
				Priority:    priority,
				Actions:     actions,
				Method:      method,
				Headers:     headers,
				QueryParams: queryParams,
			})
			continue
		}

		// Exact and PathPrefix: expand by generating separate routes per prefix
		switch policy {
		case v1alpha1.PathPrefixPolicyDisabled:
			routes = append(routes, Route{
				Path:        match.Path,
				Type:        matchType,
				Backend:     backend,
				Priority:    priority,
				Actions:     actions,
				Method:      method,
				Headers:     headers,
				QueryParams: queryParams,
			})

		case v1alpha1.PathPrefixPolicyRequired:
			needsPreserve := actionsNeedPreservePrefix(actions)
			for _, prefix := range prefixes {
				prefixedActions := actions
				if needsPreserve {
					prefixedActions = applyPreservePrefix(actions, prefix)
				}
				routes = append(routes, Route{
					Path:        prefixPath(prefix, match.Path),
					Type:        matchType,
					Backend:     backend,
					Priority:    priority,
					Actions:     prefixedActions,
					Method:      method,
					Headers:     headers,
					QueryParams: queryParams,
				})
			}

		case v1alpha1.PathPrefixPolicyOptional:
			needsPreserve := actionsNeedPreservePrefix(actions)
			for _, prefix := range prefixes {
				prefixedActions := actions
				if needsPreserve {
					prefixedActions = applyPreservePrefix(actions, prefix)
				}
				routes = append(routes, Route{
					Path:        prefixPath(prefix, match.Path),
					Type:        matchType,
					Backend:     backend,
					Priority:    priority,
					Actions:     prefixedActions,
					Method:      method,
					Headers:     headers,
					QueryParams: queryParams,
				})
			}
			routes = append(routes, Route{
				Path:        match.Path,
				Type:        matchType,
				Backend:     backend,
				Priority:    priority,
				Actions:     actions,
				Method:      method,
				Headers:     headers,
				QueryParams: queryParams,
			})
		}
	}

	if len(mirrors) > 0 {
		for i := range routes {
			routes[i].Mirrors = mirrors
		}
	}
	if cors != nil {
		for i := range routes {
			routes[i].CORS = cors
		}
	}

	return routes
}

// prefixPath prepends a language prefix to a path, avoiding double slashes.
// For path "/", it returns "/<prefix>" instead of "/<prefix>/".
func prefixPath(prefix, path string) string {
	if path == "/" {
		return "/" + prefix
	}
	return "/" + prefix + path
}

// convertHeaderMatches converts API HeaderMatch entries to runtime RouteHeaderMatch.
// The Type field is normalized to the runtime constants (Exact → "", Regex → "regex").
func convertHeaderMatches(apiHeaders []v1alpha1.HeaderMatch) []RouteHeaderMatch {
	if len(apiHeaders) == 0 {
		return nil
	}
	out := make([]RouteHeaderMatch, len(apiHeaders))
	for i, h := range apiHeaders {
		out[i] = RouteHeaderMatch{
			Name:  h.Name,
			Value: h.Value,
		}
		if h.Type == v1alpha1.HeaderMatchTypeRegularExpression {
			out[i].Type = HeaderMatchRegex
		}
	}
	return out
}

// convertQueryParamMatches converts API QueryParamMatch entries to runtime
// RouteQueryParamMatch. Type is normalized to the runtime constants
// (Exact → "", RegularExpression → "regex").
func convertQueryParamMatches(apiParams []v1alpha1.QueryParamMatch) []RouteQueryParamMatch {
	if len(apiParams) == 0 {
		return nil
	}
	out := make([]RouteQueryParamMatch, len(apiParams))
	for i, q := range apiParams {
		out[i] = RouteQueryParamMatch{
			Name:  q.Name,
			Value: q.Value,
		}
		if q.Type == v1alpha1.QueryParamMatchTypeRegularExpression {
			out[i].Type = HeaderMatchRegex
		}
	}
	return out
}

// convertActions converts API actions to route actions. Mirror and CORS
// actions are intentionally excluded — they are dispatched natively by Envoy,
// and carrying them through the ConfigMap would bloat the ExtProc hot path
// without purpose. See extractMirrors and extractCORS for the controller-side
// counterparts.
func convertActions(apiActions []v1alpha1.Action) []RouteAction {
	if len(apiActions) == 0 {
		return nil
	}

	actions := make([]RouteAction, 0, len(apiActions))
	for _, a := range apiActions {
		if a.Type == v1alpha1.ActionTypeRequestMirror || a.Type == v1alpha1.ActionTypeCORS {
			continue
		}
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
				action.RedirectReplacePrefixMatch = a.Redirect.ReplacePrefixMatch
				if a.Redirect.PreservePrefix != nil && *a.Redirect.PreservePrefix {
					action.preservePrefix = true
				}
			}
		case v1alpha1.ActionTypeRewrite:
			if a.Rewrite != nil {
				action.RewritePath = a.Rewrite.Path
				action.RewriteHostname = a.Rewrite.Hostname
				action.RewriteReplacePrefixMatch = a.Rewrite.ReplacePrefixMatch
				if a.Rewrite.PreservePrefix != nil && *a.Rewrite.PreservePrefix {
					action.preservePrefix = true
				}
			}
		case v1alpha1.ActionTypeHeaderSet, v1alpha1.ActionTypeHeaderAdd,
			v1alpha1.ActionTypeResponseHeaderSet, v1alpha1.ActionTypeResponseHeaderAdd:
			if a.Header != nil {
				action.HeaderName = a.Header.Name
				action.Value = a.Header.Value
			}
		case v1alpha1.ActionTypeHeaderRemove, v1alpha1.ActionTypeResponseHeaderRemove:
			action.HeaderName = a.HeaderName
		}

		actions = append(actions, action)
	}

	return actions
}

// extractMirrors pulls request-mirror actions out of the rule's action list
// and returns their runtime representation. The BackendRef is preserved so
// the controller can render the correct Istio cluster name when emitting
// the mirror EnvoyFilter.
func extractMirrors(apiActions []v1alpha1.Action) []RouteMirror {
	mirrors := make([]RouteMirror, 0, len(apiActions))
	for _, a := range apiActions {
		if a.Type != v1alpha1.ActionTypeRequestMirror || a.Mirror == nil {
			continue
		}
		mirrors = append(mirrors, RouteMirror{
			BackendRef: a.Mirror.BackendRef,
			Percent:    a.Mirror.Percent,
		})
	}
	return mirrors
}

// extractCORS pulls the first cors action from the rule's action list and
// converts it into the runtime form. Only the last cors action wins if
// multiple are declared (Envoy's typed_per_filter_config is a single policy
// per filter per route).
func extractCORS(apiActions []v1alpha1.Action) *RouteCORS {
	var last *v1alpha1.CORSConfig
	for i := range apiActions {
		if apiActions[i].Type != v1alpha1.ActionTypeCORS || apiActions[i].CORS == nil {
			continue
		}
		last = apiActions[i].CORS
	}
	if last == nil {
		return nil
	}
	return &RouteCORS{
		AllowOrigins:     append([]string(nil), last.AllowOrigins...),
		AllowMethods:     append([]string(nil), last.AllowMethods...),
		AllowHeaders:     append([]string(nil), last.AllowHeaders...),
		ExposeHeaders:    append([]string(nil), last.ExposeHeaders...),
		AllowCredentials: last.AllowCredentials,
		MaxAge:           last.MaxAge,
	}
}

// actionsNeedPreservePrefix returns true if any action has preservePrefix set
func actionsNeedPreservePrefix(actions []RouteAction) bool {
	for _, a := range actions {
		if a.preservePrefix {
			return true
		}
	}
	return false
}

// applyPreservePrefix clones the actions slice and prepends the prefix to
// rewrite/redirect paths for actions that have preservePrefix=true.
func applyPreservePrefix(actions []RouteAction, prefix string) []RouteAction {
	cloned := make([]RouteAction, len(actions))
	copy(cloned, actions)
	pfx := "/" + prefix
	for i := range cloned {
		if !cloned[i].preservePrefix {
			continue
		}
		if cloned[i].RewritePath != "" {
			cloned[i].RewritePath = pfx + cloned[i].RewritePath
		}
		if cloned[i].RedirectPath != "" {
			cloned[i].RedirectPath = pfx + cloned[i].RedirectPath
		}
		// *ReplacePrefixMatch are pointers; if the original was non-nil,
		// we must allocate a new bool so the cloned action doesn't share memory.
		if cloned[i].RewriteReplacePrefixMatch != nil {
			v := *cloned[i].RewriteReplacePrefixMatch
			cloned[i].RewriteReplacePrefixMatch = &v
		}
		if cloned[i].RedirectReplacePrefixMatch != nil {
			v := *cloned[i].RedirectReplacePrefixMatch
			cloned[i].RedirectReplacePrefixMatch = &v
		}
	}
	return cloned
}

// GetEffectivePolicy returns the policy to use for a rule
func GetEffectivePolicy(specPrefixes *v1alpha1.PathPrefixes, rule *v1alpha1.Rule) v1alpha1.PathPrefixPolicy {
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

// GetEffectiveExpandMatchTypes returns the list of match types that should be expanded.
// Rule-level overrides spec-level. Empty list means expand all types (default).
func GetEffectiveExpandMatchTypes(specPrefixes *v1alpha1.PathPrefixes, rule *v1alpha1.Rule) []v1alpha1.MatchType {
	if rule.PathPrefixes != nil && len(rule.PathPrefixes.ExpandMatchTypes) > 0 {
		return rule.PathPrefixes.ExpandMatchTypes
	}

	if specPrefixes != nil && len(specPrefixes.ExpandMatchTypes) > 0 {
		return specPrefixes.ExpandMatchTypes
	}

	return nil
}

// ShouldExpandMatchType returns true if the given match type should be expanded with prefixes.
// When expandTypes is nil/empty, all types are expanded (default behavior).
func ShouldExpandMatchType(matchType v1alpha1.MatchType, expandTypes []v1alpha1.MatchType) bool {
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
func buildBackendString(refs []v1alpha1.BackendRef, externalNames map[string]string) string {
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
	// Check if this is an ExternalName service
	if externalNames != nil {
		if extName, ok := externalNames[ref.Name+"/"+ref.Namespace]; ok {
			return extName + ":" + strconv.Itoa(int(ref.Port))
		}
	}
	return ref.Name + "." + ref.Namespace + ".svc.cluster.local:" + strconv.Itoa(int(ref.Port))
}

// typePriority defines the sort precedence of route types: exact > regex > prefix.
var typePriority = map[string]int{RouteTypeExact: 0, RouteTypeRegex: 1, RouteTypePrefix: 2}

// SortRoutes sorts routes by priority (descending), then by type, then by path
// length. When those are tied, more specific request match constraints win:
// method-constrained routes come before unconstrained routes, followed by
// routes with more header matches and then more query param matches.
func SortRoutes(routes []Route) {
	sort.SliceStable(routes, func(i, j int) bool {
		// First by priority descending (higher priority first)
		if routes[i].Priority != routes[j].Priority {
			return routes[i].Priority > routes[j].Priority
		}

		// Then by type priority: exact > regex > prefix
		pi, pj := typePriority[routes[i].Type], typePriority[routes[j].Type]
		if pi != pj {
			return pi < pj
		}

		// Then by path length descending (longer paths first)
		if len(routes[i].Path) != len(routes[j].Path) {
			return len(routes[i].Path) > len(routes[j].Path)
		}

		// Then by method specificity: constrained routes before unconstrained routes
		mi, mj := routeMethodSpecificity(routes[i]), routeMethodSpecificity(routes[j])
		if mi != mj {
			return mi > mj
		}

		// Then by header specificity: more header matches first
		if len(routes[i].Headers) != len(routes[j].Headers) {
			return len(routes[i].Headers) > len(routes[j].Headers)
		}

		// Then by query param specificity: more query param matches first
		if len(routes[i].QueryParams) != len(routes[j].QueryParams) {
			return len(routes[i].QueryParams) > len(routes[j].QueryParams)
		}

		return false
	})
}

// routeMethodSpecificity reports whether a route restricts the HTTP method.
// Returns 1 when the route requires a specific method, 0 otherwise. Used as a
// tie-breaker in SortRoutes so method-constrained routes win over generic ones.
func routeMethodSpecificity(route Route) int {
	if route.Method == "" {
		return 0
	}
	return 1
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

// ExpandRegexWithPrefixes modifies a regex pattern to include language prefix matching.
// It handles the insertion point carefully to maintain regex validity.
//
// For policy Optional: ^/path$ becomes ^(?:/(es|fr|it))?/path$
// For policy Required: ^/path$ becomes ^/(es|fr|it)/path$
// For policy Disabled: returns the original regex unchanged
func ExpandRegexWithPrefixes(pattern string, prefixes []string, policy v1alpha1.PathPrefixPolicy) string {
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
