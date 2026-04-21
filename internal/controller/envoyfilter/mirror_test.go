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

func int32Ptr(v int32) *int32 { return &v }

const (
	testHostA            = "a.example.com"
	testHostB            = "b.example.com"
	testClusterHeaderKey = "x-customrouter-cluster"
)

func TestHostnameToAuthorityRegex(t *testing.T) {
	tests := []struct {
		hostname string
		want     string
	}{
		{"example.com", `^example\.com(:[0-9]+)?$`},
		{"api.freepik.com", `^api\.freepik\.com(:[0-9]+)?$`},
		{"*.example.com", `^[^.]+\.example\.com(:[0-9]+)?$`},
		{"sub.*.example.com", `^sub\.\*\.example\.com(:[0-9]+)?$`},
	}
	for _, tt := range tests {
		t.Run(tt.hostname, func(t *testing.T) {
			got := hostnameToAuthorityRegex(tt.hostname)
			if got != tt.want {
				t.Errorf("hostnameToAuthorityRegex(%q) = %q, want %q", tt.hostname, got, tt.want)
			}
		})
	}
}

func TestCollectMirrorEntriesSkipsDeletedAndNonMirror(t *testing.T) {
	now := metav1.Now()
	list := &v1alpha1.CustomHTTPRouteList{
		Items: []v1alpha1.CustomHTTPRoute{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "no-mirror"},
				Spec: v1alpha1.CustomHTTPRouteSpec{
					Hostnames: []string{testHostA},
					Rules: []v1alpha1.Rule{{
						Matches: []v1alpha1.PathMatch{{Path: "/"}},
						Actions: []v1alpha1.Action{
							{Type: v1alpha1.ActionTypeHeaderSet, Header: &v1alpha1.HeaderConfig{Name: "x", Value: "y"}},
						},
						BackendRefs: []v1alpha1.BackendRef{{Name: "api", Namespace: "default", Port: 80}},
					}},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "deleted", DeletionTimestamp: &now},
				Spec: v1alpha1.CustomHTTPRouteSpec{
					Hostnames: []string{testHostB},
					Rules: []v1alpha1.Rule{{
						Matches: []v1alpha1.PathMatch{{Path: "/"}},
						Actions: []v1alpha1.Action{
							{Type: v1alpha1.ActionTypeRequestMirror, Mirror: &v1alpha1.MirrorConfig{
								BackendRef: v1alpha1.BackendRef{Name: "shadow", Namespace: "default", Port: 80},
							}},
						},
						BackendRefs: []v1alpha1.BackendRef{{Name: "api", Namespace: "default", Port: 80}},
					}},
				},
			},
		},
	}
	got := CollectMirrorEntries(list)
	if len(got) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(got))
	}
}

func TestCollectMirrorEntriesExpandsPerHostnameAndMirror(t *testing.T) {
	list := &v1alpha1.CustomHTTPRouteList{
		Items: []v1alpha1.CustomHTTPRoute{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "with-mirror"},
				Spec: v1alpha1.CustomHTTPRouteSpec{
					Hostnames: []string{testHostA, testHostB},
					Rules: []v1alpha1.Rule{{
						Matches: []v1alpha1.PathMatch{{Path: "/api", Type: v1alpha1.MatchTypeExact}},
						Actions: []v1alpha1.Action{
							{Type: v1alpha1.ActionTypeRequestMirror, Mirror: &v1alpha1.MirrorConfig{
								BackendRef: v1alpha1.BackendRef{Name: "shadow1", Namespace: "default", Port: 80},
							}},
							{Type: v1alpha1.ActionTypeRequestMirror, Mirror: &v1alpha1.MirrorConfig{
								BackendRef: v1alpha1.BackendRef{Name: "shadow2", Namespace: "default", Port: 80},
								Percent:    int32Ptr(25),
							}},
						},
						BackendRefs: []v1alpha1.BackendRef{{Name: "api", Namespace: "default", Port: 80}},
					}},
				},
			},
		},
	}
	got := CollectMirrorEntries(list)
	// 2 hostnames × 2 mirror targets = 4 entries
	if len(got) != 4 {
		t.Fatalf("expected 4 entries, got %d: %+v", len(got), got)
	}
	// Deterministic sort: testHostA entries first
	if got[0].Hostname != testHostA || got[1].Hostname != testHostA {
		t.Errorf("expected first two entries on a.example.com, got %s and %s", got[0].Hostname, got[1].Hostname)
	}
	if got[2].Hostname != testHostB || got[3].Hostname != testHostB {
		t.Errorf("expected last two entries on b.example.com, got %s and %s", got[2].Hostname, got[3].Hostname)
	}
}

func TestBuildMirrorPolicyIncludesRuntimeFraction(t *testing.T) {
	p := buildMirrorPolicy(&routes.RouteMirror{
		BackendRef: v1alpha1.BackendRef{Name: "shadow", Namespace: "default", Port: 80},
		Percent:    int32Ptr(25),
	})
	rf, ok := p["runtime_fraction"].(map[string]interface{})
	if !ok {
		t.Fatalf("runtime_fraction missing or wrong type: %+v", p)
	}
	dv := rf["default_value"].(map[string]interface{})
	if dv["numerator"].(int64) != 25 || dv["denominator"].(string) != "HUNDRED" {
		t.Errorf("unexpected runtime_fraction: %+v", rf)
	}
}

func TestBuildMirrorPolicyOmitsRuntimeFractionFor100Percent(t *testing.T) {
	p := buildMirrorPolicy(&routes.RouteMirror{
		BackendRef: v1alpha1.BackendRef{Name: "shadow", Namespace: "default", Port: 80},
		Percent:    int32Ptr(100),
	})
	if _, ok := p["runtime_fraction"]; ok {
		t.Errorf("runtime_fraction should be omitted for 100%%")
	}
}

func TestBuildMirrorPolicyOmitsRuntimeFractionWhenNil(t *testing.T) {
	p := buildMirrorPolicy(&routes.RouteMirror{
		BackendRef: v1alpha1.BackendRef{Name: "shadow", Namespace: "default", Port: 80},
	})
	if _, ok := p["runtime_fraction"]; ok {
		t.Errorf("runtime_fraction should be omitted when Percent is nil")
	}
	if p["cluster"].(string) != "outbound|80||shadow.default.svc.cluster.local" {
		t.Errorf("unexpected cluster: %v", p["cluster"])
	}
}

func TestBuildMirrorPatchIncludesAuthorityAndClusterHeaderMatchers(t *testing.T) {
	entry := &MirrorEntry{
		Hostname: "api.example.com",
		Route: routes.Route{
			Path: "/v1",
			Type: routes.RouteTypeExact,
		},
		Mirror: routes.RouteMirror{
			BackendRef: v1alpha1.BackendRef{Name: "shadow", Namespace: "default", Port: 80},
		},
	}
	patch := buildMirrorPatch(entry)

	value := patch["patch"].(map[string]interface{})["value"].(map[string]interface{})
	match := value["match"].(map[string]interface{})
	headers := match["headers"].([]interface{})

	var hasAuthority, hasClusterHeader bool
	for _, h := range headers {
		hm := h.(map[string]interface{})
		if hm["name"] == ":authority" {
			hasAuthority = true
		}
		if hm["name"] == testClusterHeaderKey {
			hasClusterHeader = true
		}
	}
	if !hasAuthority {
		t.Errorf(":authority header matcher missing: %+v", headers)
	}
	if !hasClusterHeader {
		t.Errorf("x-customrouter-cluster header matcher missing: %+v", headers)
	}

	route := value["route"].(map[string]interface{})
	if route["cluster_header"] != testClusterHeaderKey {
		t.Errorf("primary route must still use cluster_header, got %v", route["cluster_header"])
	}
	mp := route["request_mirror_policies"].([]interface{})
	if len(mp) != 1 {
		t.Errorf("expected 1 mirror policy, got %d", len(mp))
	}
}

func TestBuildMirrorEnvoyFilterProducesStableName(t *testing.T) {
	epa := &v1alpha1.ExternalProcessorAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: "epa1", Namespace: "istio-system"},
		Spec: v1alpha1.ExternalProcessorAttachmentSpec{
			GatewayRef: v1alpha1.GatewayRef{Selector: map[string]string{"app": "gw"}},
		},
	}
	entries := []MirrorEntry{{
		Hostname: testHostA,
		Route:    routes.Route{Path: "/x", Type: routes.RouteTypeExact},
		Mirror: routes.RouteMirror{
			BackendRef: v1alpha1.BackendRef{Name: "shadow", Namespace: "default", Port: 80},
		},
	}}

	got1, err := BuildMirrorEnvoyFilter(epa, entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got2, err := BuildMirrorEnvoyFilter(epa, entries)
	if err != nil {
		t.Fatalf("unexpected error on second build: %v", err)
	}

	if got1.GetName() != "epa1"+MirrorFilterSuffix {
		t.Errorf("unexpected name: %q", got1.GetName())
	}
	if got1.GetName() != got2.GetName() {
		t.Errorf("name should be stable across rebuilds")
	}
}

func TestMirrorRouteNameIsDeterministic(t *testing.T) {
	entry := &MirrorEntry{
		Hostname: testHostA,
		Route:    routes.Route{Path: "/x", Type: routes.RouteTypeExact},
		Mirror: routes.RouteMirror{
			BackendRef: v1alpha1.BackendRef{Name: "shadow", Namespace: "default", Port: 80},
			Percent:    int32Ptr(50),
		},
	}
	a := mirrorRouteName(entry)
	b := mirrorRouteName(entry)
	if a != b {
		t.Errorf("mirrorRouteName not deterministic: %q vs %q", a, b)
	}

	entry.Mirror.Percent = int32Ptr(60)
	c := mirrorRouteName(entry)
	if a == c {
		t.Errorf("mirrorRouteName should differ when Percent changes")
	}
}
