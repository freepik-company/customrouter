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

package v1alpha1

import (
	"fmt"
	"regexp"
	"strings"
)

// ValidateCustomHTTPRoute validates the CustomHTTPRoute spec
func (r *CustomHTTPRoute) Validate() error {
	for i, rule := range r.Spec.Rules {
		if err := validateRule(i, &rule); err != nil {
			return err
		}
	}
	return nil
}

// validateRule validates a single rule
func validateRule(index int, rule *Rule) error {
	hasRedirect := false
	for _, action := range rule.Actions {
		if action.Type == ActionTypeRedirect {
			hasRedirect = true
			break
		}
	}

	// If no redirect action, backendRefs is required
	if !hasRedirect && len(rule.BackendRefs) == 0 {
		return fmt.Errorf("rules[%d]: backendRefs is required when no redirect action is specified", index)
	}

	// Validate actions
	for j, action := range rule.Actions {
		if err := validateAction(index, j, &action); err != nil {
			return err
		}
	}

	// Validate regex patterns with {prefix} placeholder
	for j, match := range rule.Matches {
		if match.Type == MatchTypeRegex && strings.Contains(match.Path, "{prefix}") {
			testPattern := strings.ReplaceAll(match.Path, "{prefix}", "(test)")
			if _, err := regexp.Compile(testPattern); err != nil {
				return fmt.Errorf("rules[%d].matches[%d]: regex with {prefix} placeholder produces invalid pattern: %s → %s: %v",
					index, j, match.Path, testPattern, err)
			}
		}
	}

	// Validate preservePrefix is not used with Regex match types
	if ruleHasPreservePrefix(rule) && ruleHasRegexMatch(rule) {
		return fmt.Errorf("rules[%d]: preservePrefix is not supported with Regex match type", index)
	}

	// Validate redirect.replacePrefixMatch is only used with PathPrefix match types
	if ruleHasRedirectReplacePrefixMatch(rule) && ruleHasRegexMatch(rule) {
		return fmt.Errorf("rules[%d]: redirect.replacePrefixMatch is not supported with Regex match type", index)
	}

	return nil
}

// ruleHasRedirectReplacePrefixMatch returns true if any redirect action in the rule has replacePrefixMatch enabled
func ruleHasRedirectReplacePrefixMatch(rule *Rule) bool {
	for _, action := range rule.Actions {
		if action.Redirect != nil && action.Redirect.ReplacePrefixMatch != nil && *action.Redirect.ReplacePrefixMatch {
			return true
		}
	}
	return false
}

// ruleHasPreservePrefix returns true if any action in the rule has preservePrefix enabled
func ruleHasPreservePrefix(rule *Rule) bool {
	for _, action := range rule.Actions {
		if action.Rewrite != nil && action.Rewrite.PreservePrefix != nil && *action.Rewrite.PreservePrefix {
			return true
		}
		if action.Redirect != nil && action.Redirect.PreservePrefix != nil && *action.Redirect.PreservePrefix {
			return true
		}
	}
	return false
}

// ruleHasRegexMatch returns true if any match in the rule uses Regex type
func ruleHasRegexMatch(rule *Rule) bool {
	for _, match := range rule.Matches {
		if match.Type == MatchTypeRegex {
			return true
		}
	}
	return false
}

// validateAction validates a single action
func validateAction(ruleIndex, actionIndex int, action *Action) error {
	prefix := fmt.Sprintf("rules[%d].actions[%d]", ruleIndex, actionIndex)

	switch action.Type {
	case ActionTypeRedirect:
		return validateRedirectAction(prefix, action)
	case ActionTypeRewrite:
		return validateRewriteAction(prefix, action)
	case ActionTypeHeaderSet, ActionTypeHeaderAdd,
		ActionTypeResponseHeaderSet, ActionTypeResponseHeaderAdd:
		return validateHeaderAction(prefix, action)
	case ActionTypeHeaderRemove, ActionTypeResponseHeaderRemove:
		return validateHeaderRemoveAction(prefix, action)
	case ActionTypeRequestMirror:
		return validateMirrorAction(prefix, action)
	case ActionTypeCORS:
		return validateCORSAction(prefix, action)
	default:
		return fmt.Errorf("%s: unknown action type '%s'", prefix, action.Type)
	}
}

func validateRedirectAction(prefix string, action *Action) error {
	if action.Redirect == nil {
		return fmt.Errorf("%s: redirect config is required when type is 'redirect'", prefix)
	}
	if action.Redirect.Scheme == "" && action.Redirect.Hostname == "" &&
		action.Redirect.Path == "" && action.Redirect.Port == nil {
		return fmt.Errorf("%s: at least one redirect field (scheme, hostname, path, or port) must be specified", prefix)
	}
	return nil
}

func validateRewriteAction(prefix string, action *Action) error {
	if action.Rewrite == nil {
		return fmt.Errorf("%s: rewrite config is required when type is 'rewrite'", prefix)
	}
	if action.Rewrite.Path == "" && action.Rewrite.Hostname == "" {
		return fmt.Errorf("%s: at least one rewrite field (path or hostname) must be specified", prefix)
	}
	return nil
}

func validateHeaderAction(prefix string, action *Action) error {
	if action.Header == nil {
		return fmt.Errorf("%s: header config is required when type is '%s'", prefix, action.Type)
	}
	if action.Header.Name == "" {
		return fmt.Errorf("%s: header.name is required", prefix)
	}
	return nil
}

func validateHeaderRemoveAction(prefix string, action *Action) error {
	if action.HeaderName == "" {
		return fmt.Errorf("%s: headerName is required when type is '%s'", prefix, action.Type)
	}
	return nil
}

func validateMirrorAction(prefix string, action *Action) error {
	if action.Mirror == nil {
		return fmt.Errorf("%s: mirror config is required when type is 'request-mirror'", prefix)
	}
	if action.Mirror.BackendRef.Name == "" {
		return fmt.Errorf("%s: mirror.backendRef.name is required", prefix)
	}
	if action.Mirror.BackendRef.Namespace == "" {
		return fmt.Errorf("%s: mirror.backendRef.namespace is required", prefix)
	}
	if action.Mirror.BackendRef.Port <= 0 || action.Mirror.BackendRef.Port > 65535 {
		return fmt.Errorf("%s: mirror.backendRef.port must be in [1, 65535]", prefix)
	}
	if action.Mirror.Percent != nil && (*action.Mirror.Percent < 0 || *action.Mirror.Percent > 100) {
		return fmt.Errorf("%s: mirror.percent must be in [0, 100]", prefix)
	}
	return nil
}

func validateCORSAction(prefix string, action *Action) error {
	if action.CORS == nil {
		return fmt.Errorf("%s: cors config is required when type is 'cors'", prefix)
	}
	if len(action.CORS.AllowOrigins) == 0 {
		return fmt.Errorf("%s: cors.allowOrigins must contain at least one entry", prefix)
	}
	hasWildcardOrigin := false
	for i, origin := range action.CORS.AllowOrigins {
		if origin == "*" {
			hasWildcardOrigin = true
			continue
		}
		if !isValidCORSOrigin(origin) {
			return fmt.Errorf("%s: cors.allowOrigins[%d]: %q is not a valid origin (expected \"*\" or an absolute URI with scheme and host)", prefix, i, origin)
		}
	}
	if hasWildcardOrigin && action.CORS.AllowCredentials {
		return fmt.Errorf("%s: cors.allowOrigins cannot contain \"*\" when allowCredentials is true (browsers reject that combination)", prefix)
	}
	return nil
}

// isValidCORSOrigin returns true when the given string is an absolute URI
// suitable as a CORS origin: a scheme (http/https) followed by "://" and a
// non-empty host. No path, query, or fragment is permitted — browsers never
// send those in the Origin header.
func isValidCORSOrigin(origin string) bool {
	var scheme string
	switch {
	case strings.HasPrefix(origin, "https://"):
		scheme = "https://"
	case strings.HasPrefix(origin, "http://"):
		scheme = "http://"
	default:
		return false
	}
	rest := origin[len(scheme):]
	if rest == "" {
		return false
	}
	if strings.ContainsAny(rest, "/?#") {
		return false
	}
	return true
}

// HasRedirectAction returns true if any rule has a redirect action
func (r *Rule) HasRedirectAction() bool {
	for _, action := range r.Actions {
		if action.Type == ActionTypeRedirect {
			return true
		}
	}
	return false
}
