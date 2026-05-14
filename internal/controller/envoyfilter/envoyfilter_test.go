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

func epaWithNumRetries(n int64) *v1alpha1.ExternalProcessorAttachment {
	return &v1alpha1.ExternalProcessorAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: "epa", Namespace: "istio-system"},
		Spec: v1alpha1.ExternalProcessorAttachmentSpec{
			GatewayRef: v1alpha1.GatewayRef{Selector: map[string]string{"app": "gw"}},
			RetryPolicy: &v1alpha1.RetryPolicyConfig{
				NumRetries: n,
			},
		},
	}
}

func TestGetNumRetriesDefaultsToZero(t *testing.T) {
	epa := &v1alpha1.ExternalProcessorAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: "epa"},
		Spec: v1alpha1.ExternalProcessorAttachmentSpec{
			GatewayRef: v1alpha1.GatewayRef{Selector: map[string]string{"app": "gw"}},
		},
	}
	if got := GetNumRetries(epa); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestGetNumRetriesReturnsConfiguredValue(t *testing.T) {
	epa := epaWithNumRetries(3)
	if got := GetNumRetries(epa); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
}

func retryPolicyFromPatch(t *testing.T, patch map[string]interface{}) map[string]interface{} {
	t.Helper()
	value := patch["patch"].(map[string]interface{})["value"].(map[string]interface{})
	route := value["route"].(map[string]interface{})
	rp, ok := route["retry_policy"].(map[string]interface{})
	if !ok {
		t.Fatalf("retry_policy missing or wrong type: %+v", route)
	}
	return rp
}

func TestBuildMirrorPatchUsesNumRetries(t *testing.T) {
	entry := &MirrorEntry{
		Hostname: "api.example.com",
		Route:    routes.Route{Path: "/v1", Type: routes.RouteTypeExact},
		Mirror: routes.RouteMirror{
			BackendRef: v1alpha1.BackendRef{Name: "shadow", Namespace: "default", Port: 80},
		},
	}

	for _, tc := range []struct{ numRetries int64 }{
		{0},
		{1},
		{5},
	} {
		patch := buildMirrorPatch(entry, tc.numRetries)
		rp := retryPolicyFromPatch(t, patch)
		if got, ok := rp["num_retries"].(int64); !ok || got != tc.numRetries {
			t.Errorf("numRetries=%d: got num_retries=%v", tc.numRetries, rp["num_retries"])
		}
	}
}

func TestBuildCORSPatchUsesNumRetries(t *testing.T) {
	entry := &CORSEntry{
		Hostname: "api.example.com",
		Route:    routes.Route{Path: "/v1", Type: routes.RouteTypeExact},
		Policy:   routes.RouteCORS{AllowOrigins: []string{"https://app.example.com"}},
	}

	for _, tc := range []struct{ numRetries int64 }{
		{0},
		{2},
	} {
		patch := buildCORSPatch(entry, tc.numRetries)
		rp := retryPolicyFromPatch(t, patch)
		if got, ok := rp["num_retries"].(int64); !ok || got != tc.numRetries {
			t.Errorf("numRetries=%d: got num_retries=%v", tc.numRetries, rp["num_retries"])
		}
	}
}

func TestBuildCatchAllVirtualHostPatchUsesNumRetries(t *testing.T) {
	entry := CatchAllEntry{
		Hostname:   "example.com",
		BackendRef: v1alpha1.BackendRef{Name: "default-backend", Namespace: "default", Port: 80},
	}

	for _, tc := range []struct{ numRetries int64 }{
		{0},
		{3},
	} {
		patch := buildCatchAllVirtualHostPatch(entry, tc.numRetries)
		routeSlice := patch["patch"].(map[string]interface{})["value"].(map[string]interface{})["routes"].([]interface{})
		// The first route is the dynamic route with a retry_policy
		dynRoute := routeSlice[0].(map[string]interface{})
		rp, ok := dynRoute["route"].(map[string]interface{})["retry_policy"].(map[string]interface{})
		if !ok {
			t.Fatalf("numRetries=%d: retry_policy missing from dynamic route", tc.numRetries)
		}
		if got, ok := rp["num_retries"].(int64); !ok || got != tc.numRetries {
			t.Errorf("numRetries=%d: got num_retries=%v", tc.numRetries, rp["num_retries"])
		}
	}
}

func TestBuildMirrorEnvoyFilterUsesNumRetriesFromEPA(t *testing.T) {
	epa := epaWithNumRetries(5)
	entries := []MirrorEntry{{
		Hostname: testHostA,
		Route:    routes.Route{Path: "/x", Type: routes.RouteTypeExact},
		Mirror: routes.RouteMirror{
			BackendRef: v1alpha1.BackendRef{Name: "shadow", Namespace: "default", Port: 80},
		},
	}}

	ef, err := BuildMirrorEnvoyFilter(epa, entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	patches := getNestedSlice(ef.Object, "spec", "configPatches")
	if len(patches) == 0 {
		t.Fatal("no configPatches in EnvoyFilter")
	}
	patch := patches[0].(map[string]interface{})
	rp := retryPolicyFromPatch(t, patch)
	if got, ok := rp["num_retries"].(int64); !ok || got != 5 {
		t.Errorf("expected num_retries=5, got %v", rp["num_retries"])
	}
}

func TestBuildCORSEnvoyFilterUsesNumRetriesFromEPA(t *testing.T) {
	epa := epaWithNumRetries(7)
	entries := []CORSEntry{{
		Hostname: "a.example.com",
		Route:    routes.Route{Path: "/x", Type: routes.RouteTypeExact},
		Policy:   routes.RouteCORS{AllowOrigins: []string{"https://app.example.com"}},
	}}

	ef, err := BuildCORSEnvoyFilter(epa, entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	patches := getNestedSlice(ef.Object, "spec", "configPatches")
	if len(patches) == 0 {
		t.Fatal("no configPatches in EnvoyFilter")
	}
	patch := patches[0].(map[string]interface{})
	rp := retryPolicyFromPatch(t, patch)
	if got, ok := rp["num_retries"].(int64); !ok || got != 7 {
		t.Errorf("expected num_retries=7, got %v", rp["num_retries"])
	}
}

// getNestedSlice is a helper to retrieve a nested slice from an unstructured map.
func getNestedSlice(obj map[string]interface{}, fields ...string) []interface{} {
	cur := obj
	for i, f := range fields {
		if i == len(fields)-1 {
			v, ok := cur[f]
			if !ok {
				return nil
			}
			s, _ := v.([]interface{})
			return s
		}
		next, ok := cur[f].(map[string]interface{})
		if !ok {
			return nil
		}
		cur = next
	}
	return nil
}
