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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/pkg/routes"
)

func TestCollectCORSEntriesIgnoresRoutesWithoutCORS(t *testing.T) {
	list := &v1alpha1.CustomHTTPRouteList{
		Items: []v1alpha1.CustomHTTPRoute{{
			ObjectMeta: metav1.ObjectMeta{Name: "no-cors"},
			Spec: v1alpha1.CustomHTTPRouteSpec{
				Hostnames: []string{"a.example.com"},
				Rules: []v1alpha1.Rule{{
					Matches: []v1alpha1.PathMatch{{Path: "/api"}},
					Actions: []v1alpha1.Action{
						{Type: v1alpha1.ActionTypeHeaderSet, Header: &v1alpha1.HeaderConfig{Name: "x", Value: "y"}},
					},
					BackendRefs: []v1alpha1.BackendRef{{Name: "api", Namespace: "default", Port: 80}},
				}},
			},
		}},
	}
	got := CollectCORSEntries(list)
	if len(got) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(got))
	}
}

func TestCollectCORSEntriesPerHostname(t *testing.T) {
	list := &v1alpha1.CustomHTTPRouteList{
		Items: []v1alpha1.CustomHTTPRoute{{
			ObjectMeta: metav1.ObjectMeta{Name: "with-cors"},
			Spec: v1alpha1.CustomHTTPRouteSpec{
				Hostnames: []string{"a.example.com", "b.example.com"},
				Rules: []v1alpha1.Rule{{
					Matches: []v1alpha1.PathMatch{{Path: "/api", Type: v1alpha1.MatchTypeExact}},
					Actions: []v1alpha1.Action{{
						Type: v1alpha1.ActionTypeCORS,
						CORS: &v1alpha1.CORSConfig{
							AllowOrigins: []string{"https://app.example.com"},
							AllowMethods: []string{"GET", "POST"},
						},
					}},
					BackendRefs: []v1alpha1.BackendRef{{Name: "api", Namespace: "default", Port: 80}},
				}},
			},
		}},
	}
	got := CollectCORSEntries(list)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries (one per hostname), got %d", len(got))
	}
	if got[0].Hostname != "a.example.com" || got[1].Hostname != "b.example.com" {
		t.Errorf("entries not sorted by hostname: %+v", got)
	}
	if len(got[0].Policy.AllowOrigins) != 1 || got[0].Policy.AllowOrigins[0] != "https://app.example.com" {
		t.Errorf("unexpected origins: %+v", got[0].Policy.AllowOrigins)
	}
}

func TestBuildCORSPolicyTypedRendersAllFields(t *testing.T) {
	p := &routes.RouteCORS{
		AllowOrigins:     []string{"https://app.example.com", "*"},
		AllowMethods:     []string{"GET", "POST"},
		AllowHeaders:     []string{"x-custom"},
		ExposeHeaders:    []string{"x-trace"},
		AllowCredentials: false,
		MaxAge:           600,
	}
	got := buildCORSPolicyTyped(p)

	if got["@type"] != corsPolicyTypeURL {
		t.Errorf("missing/incorrect @type: %v", got["@type"])
	}
	if got["allow_methods"] != "GET,POST" {
		t.Errorf("unexpected allow_methods: %v", got["allow_methods"])
	}
	if got["allow_headers"] != "x-custom" {
		t.Errorf("unexpected allow_headers: %v", got["allow_headers"])
	}
	if got["expose_headers"] != "x-trace" {
		t.Errorf("unexpected expose_headers: %v", got["expose_headers"])
	}
	if got["max_age"] != "600" {
		t.Errorf("unexpected max_age: %v", got["max_age"])
	}

	origins := got["allow_origin_string_match"].([]interface{})
	if len(origins) != 2 {
		t.Fatalf("expected 2 origin matchers, got %d", len(origins))
	}
	if origins[0].(map[string]interface{})["exact"] != "https://app.example.com" {
		t.Errorf("first origin should be exact match: %+v", origins[0])
	}
	sr := origins[1].(map[string]interface{})["safe_regex"].(map[string]interface{})
	if sr["regex"] != ".*" {
		t.Errorf("wildcard origin should be .* safe_regex: %+v", sr)
	}
}

func TestBuildCORSPolicyTypedOmitsEmptyFields(t *testing.T) {
	p := &routes.RouteCORS{
		AllowOrigins: []string{"https://app.example.com"},
	}
	got := buildCORSPolicyTyped(p)
	for _, k := range []string{"allow_methods", "allow_headers", "expose_headers", "allow_credentials", "max_age"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s should be omitted when empty, got %v", k, got[k])
		}
	}
}

func TestBuildCORSPolicyTypedAllowCredentials(t *testing.T) {
	p := &routes.RouteCORS{
		AllowOrigins:     []string{"https://app.example.com"},
		AllowCredentials: true,
	}
	got := buildCORSPolicyTyped(p)
	if got["allow_credentials"] != true {
		t.Errorf("allow_credentials should be true, got %v", got["allow_credentials"])
	}
}

func TestBuildCORSPatchIncludesTypedPerFilterConfig(t *testing.T) {
	entry := &CORSEntry{
		Hostname: "api.example.com",
		Route:    routes.Route{Path: "/v1", Type: routes.RouteTypeExact},
		Policy: routes.RouteCORS{
			AllowOrigins: []string{"https://app.example.com"},
			AllowMethods: []string{"GET"},
		},
	}
	patch := buildCORSPatch(epaWithRetryPolicy(nil), entry)
	value := patch["patch"].(map[string]interface{})["value"].(map[string]interface{})

	tpf, ok := value["typed_per_filter_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("typed_per_filter_config missing")
	}
	if _, ok := tpf[corsFilterName]; !ok {
		t.Errorf("typed_per_filter_config missing %s entry: %+v", corsFilterName, tpf)
	}

	match := value["match"].(map[string]interface{})
	headers := match["headers"].([]interface{})
	var hasAuthority, hasCluster bool
	for _, h := range headers {
		hm := h.(map[string]interface{})
		if hm["name"] == ":authority" {
			hasAuthority = true
		}
		if hm["name"] == "x-customrouter-cluster" {
			hasCluster = true
		}
	}
	if !hasAuthority || !hasCluster {
		t.Errorf("missing :authority or x-customrouter-cluster matcher: %+v", headers)
	}
}

func TestBuildCORSEnvoyFilterStableNaming(t *testing.T) {
	epa := &v1alpha1.ExternalProcessorAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: "epa1", Namespace: "istio-system"},
		Spec: v1alpha1.ExternalProcessorAttachmentSpec{
			GatewayRef: v1alpha1.GatewayRef{Selector: map[string]string{"app": "gw"}},
		},
	}
	entries := []CORSEntry{{
		Hostname: "a.example.com",
		Route:    routes.Route{Path: "/x", Type: routes.RouteTypeExact},
		Policy:   routes.RouteCORS{AllowOrigins: []string{"https://app.example.com"}},
	}}
	got, err := BuildCORSEnvoyFilter(epa, entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.GetName() != "epa1"+CORSFilterSuffix {
		t.Errorf("unexpected name: %q", got.GetName())
	}

	n1 := corsRouteName(&entries[0])
	n2 := corsRouteName(&entries[0])
	if n1 != n2 {
		t.Errorf("corsRouteName not deterministic")
	}

	entries[0].Policy.MaxAge = 120
	if corsRouteName(&entries[0]) == n1 {
		t.Errorf("corsRouteName should change when policy changes")
	}
}
