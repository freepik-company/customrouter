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

const (
	rbacFilterName      = "envoy.filters.http.rbac"
	rbacPerRouteTypeURL = "type.googleapis.com/envoy.extensions.filters.http.rbac.v3.RBACPerRoute"
)

// RBACPerRouteConfig returns the typed_per_filter_config entry that makes the
// RBAC filter apply its filter-level shadow_rules to routes injected via
// EnvoyFilter. Without this entry, Istio's ext_authz filter_enabled_metadata
// mechanism does not activate for non-Istio-managed routes, bypassing external
// authorization (e.g. oauth2-proxy).
func RBACPerRouteConfig() map[string]interface{} {
	return map[string]interface{}{
		rbacFilterName: map[string]interface{}{
			"@type": rbacPerRouteTypeURL,
		},
	}
}
