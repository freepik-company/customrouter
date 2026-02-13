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

	return nil
}

// validateAction validates a single action
func validateAction(ruleIndex, actionIndex int, action *Action) error {
	prefix := fmt.Sprintf("rules[%d].actions[%d]", ruleIndex, actionIndex)

	switch action.Type {
	case ActionTypeRedirect:
		if action.Redirect == nil {
			return fmt.Errorf("%s: redirect config is required when type is 'redirect'", prefix)
		}
		// At least one redirect field should be set
		if action.Redirect.Scheme == "" && action.Redirect.Hostname == "" &&
			action.Redirect.Path == "" && action.Redirect.Port == nil {
			return fmt.Errorf("%s: at least one redirect field (scheme, hostname, path, or port) must be specified", prefix)
		}

	case ActionTypeRewrite:
		if action.Rewrite == nil {
			return fmt.Errorf("%s: rewrite config is required when type is 'rewrite'", prefix)
		}
		// At least one rewrite field should be set
		if action.Rewrite.Path == "" && action.Rewrite.Hostname == "" {
			return fmt.Errorf("%s: at least one rewrite field (path or hostname) must be specified", prefix)
		}

	case ActionTypeHeaderSet, ActionTypeHeaderAdd:
		if action.Header == nil {
			return fmt.Errorf("%s: header config is required when type is '%s'", prefix, action.Type)
		}
		if action.Header.Name == "" {
			return fmt.Errorf("%s: header.name is required", prefix)
		}

	case ActionTypeHeaderRemove:
		if action.HeaderName == "" {
			return fmt.Errorf("%s: headerName is required when type is 'header-remove'", prefix)
		}

	default:
		return fmt.Errorf("%s: unknown action type '%s'", prefix, action.Type)
	}

	return nil
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
