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

package envoyfilter

import (
	"github.com/freepik-company/customrouter/pkg/routes"
)

// BuildRouteMatch translates a runtime Route's match criteria (path, method,
// headers, queryParams) into an Envoy RouteMatch expressed as the untyped
// map form that Istio EnvoyFilter value patches consume.
//
// Semantics:
//   - Exact path     → "path": r.Path
//   - PathPrefix     → "path_separated_prefix": r.Path (Gateway API segment boundary)
//   - Regex          → "safe_regex": {"regex": r.Path} (RE2 engine, the default)
//   - Method         → header matcher on ":method" with exact_match
//   - Header Exact   → header matcher with exact_match
//   - Header Regex   → header matcher with safe_regex_match
//   - QueryParam     → query_parameters entry with string_match
//
// Prefix matches on "/" degrade to a plain "prefix": "/" because Envoy
// rejects path_separated_prefix values that end with "/" or are exactly "/".
func BuildRouteMatch(r *routes.Route) map[string]interface{} {
	match := map[string]interface{}{}

	switch r.Type {
	case routes.RouteTypeExact:
		match["path"] = r.Path
	case routes.RouteTypeRegex:
		match["safe_regex"] = map[string]interface{}{
			"regex": r.Path,
		}
	default:
		if r.Path == "" || r.Path == "/" {
			match["prefix"] = "/"
		} else {
			match["path_separated_prefix"] = trimTrailingSlash(r.Path)
		}
	}

	headerMatchers := make([]interface{}, 0, 1+len(r.Headers))
	if r.Method != "" {
		headerMatchers = append(headerMatchers, map[string]interface{}{
			"name":        ":method",
			"exact_match": r.Method,
		})
	}
	for i := range r.Headers {
		h := &r.Headers[i]
		headerMatchers = append(headerMatchers, buildHeaderMatcher(h))
	}
	if len(headerMatchers) > 0 {
		match["headers"] = headerMatchers
	}

	if len(r.QueryParams) > 0 {
		qps := make([]interface{}, 0, len(r.QueryParams))
		for i := range r.QueryParams {
			q := &r.QueryParams[i]
			qps = append(qps, buildQueryParamMatcher(q))
		}
		match["query_parameters"] = qps
	}

	return match
}

func buildHeaderMatcher(h *routes.RouteHeaderMatch) map[string]interface{} {
	m := map[string]interface{}{
		"name": h.Name,
	}
	if h.Type == routes.HeaderMatchRegex {
		m["safe_regex_match"] = map[string]interface{}{
			"regex": h.Value,
		}
	} else {
		m["exact_match"] = h.Value
	}
	return m
}

func buildQueryParamMatcher(q *routes.RouteQueryParamMatch) map[string]interface{} {
	sm := map[string]interface{}{}
	if q.Type == routes.HeaderMatchRegex {
		sm["safe_regex"] = map[string]interface{}{
			"regex": q.Value,
		}
	} else {
		sm["exact"] = q.Value
	}
	return map[string]interface{}{
		"name":         q.Name,
		"string_match": sm,
	}
}

// trimTrailingSlash strips a single trailing slash from a path. Envoy's
// path_separated_prefix treats "/foo" and "/foo/" equivalently for matching,
// but rejects values that end with "/", so we normalise before emitting.
func trimTrailingSlash(p string) string {
	if len(p) > 1 && p[len(p)-1] == '/' {
		return p[:len(p)-1]
	}
	return p
}
