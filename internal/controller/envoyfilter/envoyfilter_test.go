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
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/pkg/routes"
)

func epaWithRetryPolicy(rp *v1alpha1.RetryPolicyConfig) *v1alpha1.ExternalProcessorAttachment {
	return &v1alpha1.ExternalProcessorAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: "epa", Namespace: "istio-system"},
		Spec: v1alpha1.ExternalProcessorAttachmentSpec{
			GatewayRef:  v1alpha1.GatewayRef{Selector: map[string]string{"app": "gw"}},
			RetryPolicy: rp,
		},
	}
}

func epaWithNumRetries(n int64) *v1alpha1.ExternalProcessorAttachment {
	return epaWithRetryPolicy(&v1alpha1.RetryPolicyConfig{NumRetries: n})
}

// TestBuildRetryPolicy_NilReturnsNil locks in the contract that an EPA without
// a retryPolicy field produces no retry_policy block at all. This is the
// post-0.7.0 behaviour and prevents a silent regression to the pre-0.7.0
// "num_retries:2 hardcoded" world.
func TestBuildRetryPolicy_NilReturnsNil(t *testing.T) {
	if got := BuildRetryPolicy(nil); got != nil {
		t.Errorf("expected nil retry_policy when RetryPolicy is nil, got: %+v", got)
	}
}

// TestBuildRetryPolicy_EmptyConfigEmitsZeroPolicy locks in that an explicit
// empty RetryPolicy{} still emits a policy (with num_retries:0) — the user
// expressing intent to pin retries at zero is meaningful and should be
// surfaced in the generated EnvoyFilter for auditing.
func TestBuildRetryPolicy_EmptyConfigEmitsZeroPolicy(t *testing.T) {
	got := BuildRetryPolicy(&v1alpha1.RetryPolicyConfig{})
	if got == nil {
		t.Fatal("expected non-nil retry_policy for explicit empty config")
	}
	if got["num_retries"].(int64) != 0 {
		t.Errorf("num_retries: want 0, got %v", got["num_retries"])
	}
	if got["retry_on"].(string) != defaultRetryOn {
		t.Errorf("retry_on default mismatch: got %v", got["retry_on"])
	}
	codes := got["retriable_status_codes"].([]interface{})
	if len(codes) != 1 || codes[0].(int64) != 503 {
		t.Errorf("retriable_status_codes default mismatch: got %v", codes)
	}
}

func TestBuildRetryPolicy_AllFieldsRespected(t *testing.T) {
	rp := &v1alpha1.RetryPolicyConfig{
		NumRetries:           4,
		RetryOn:              "5xx",
		RetriableStatusCodes: []int32{500, 502, 504},
		PerTryTimeout:        "2s",
	}
	got := BuildRetryPolicy(rp)
	if got["retry_on"].(string) != "5xx" {
		t.Errorf("retry_on: got %v", got["retry_on"])
	}
	if got["num_retries"].(int64) != 4 {
		t.Errorf("num_retries: got %v", got["num_retries"])
	}
	if got["per_try_timeout"].(string) != "2s" {
		t.Errorf("per_try_timeout: got %v", got["per_try_timeout"])
	}
	want := []interface{}{int64(500), int64(502), int64(504)}
	if !reflect.DeepEqual(got["retriable_status_codes"], want) {
		t.Errorf("retriable_status_codes: got %v, want %v", got["retriable_status_codes"], want)
	}
}

// TestBuildRetryPolicy_PerTryTimeoutOmittedWhenEmpty ensures we do not emit
// an empty per_try_timeout that would otherwise be interpreted as 0s by Envoy
// (immediate timeout, dataplane breaker).
func TestBuildRetryPolicy_PerTryTimeoutOmittedWhenEmpty(t *testing.T) {
	got := BuildRetryPolicy(&v1alpha1.RetryPolicyConfig{NumRetries: 1})
	if _, present := got["per_try_timeout"]; present {
		t.Errorf("per_try_timeout should be absent when not configured, got %v", got["per_try_timeout"])
	}
}

func TestGetRouteTimeoutDefault(t *testing.T) {
	epa := &v1alpha1.ExternalProcessorAttachment{}
	if got := GetRouteTimeout(epa); got != DefaultRouteTimeout {
		t.Errorf("expected %s, got %s", DefaultRouteTimeout, got)
	}
}

func TestGetRouteTimeoutOverride(t *testing.T) {
	epa := &v1alpha1.ExternalProcessorAttachment{
		Spec: v1alpha1.ExternalProcessorAttachmentSpec{RouteTimeout: "5s"},
	}
	if got := GetRouteTimeout(epa); got != "5s" {
		t.Errorf("expected 5s, got %s", got)
	}
}

func retryPolicyFromPatch(t *testing.T, patch map[string]interface{}) (map[string]interface{}, bool) {
	t.Helper()
	value := patch["patch"].(map[string]interface{})["value"].(map[string]interface{})
	route := value["route"].(map[string]interface{})
	rp, ok := route["retry_policy"].(map[string]interface{})
	return rp, ok
}

func TestBuildMirrorPatch_RespectsRetryPolicy(t *testing.T) {
	entry := &MirrorEntry{
		Hostname: "api.example.com",
		Route:    routes.Route{Path: "/v1", Type: routes.RouteTypeExact},
		Mirror: routes.RouteMirror{
			BackendRef: v1alpha1.BackendRef{Name: "shadow", Namespace: "default", Port: 80},
		},
	}

	// EPA without RetryPolicy: no retry_policy emitted.
	patch := buildMirrorPatch(epaWithRetryPolicy(nil), entry)
	if _, ok := retryPolicyFromPatch(t, patch); ok {
		t.Error("retry_policy should be absent when EPA has no retryPolicy configured")
	}

	// EPA with NumRetries=3: retry_policy emitted with the configured value.
	patch = buildMirrorPatch(epaWithNumRetries(3), entry)
	rp, ok := retryPolicyFromPatch(t, patch)
	if !ok {
		t.Fatal("retry_policy missing")
	}
	if rp["num_retries"].(int64) != 3 {
		t.Errorf("num_retries: got %v", rp["num_retries"])
	}
}

func TestBuildCORSPatch_RespectsRetryPolicy(t *testing.T) {
	entry := &CORSEntry{
		Hostname: "api.example.com",
		Route:    routes.Route{Path: "/v1", Type: routes.RouteTypeExact},
		Policy:   routes.RouteCORS{AllowOrigins: []string{"https://app.example.com"}},
	}

	patch := buildCORSPatch(epaWithRetryPolicy(nil), entry)
	if _, ok := retryPolicyFromPatch(t, patch); ok {
		t.Error("retry_policy should be absent when EPA has no retryPolicy configured")
	}

	patch = buildCORSPatch(epaWithNumRetries(2), entry)
	rp, ok := retryPolicyFromPatch(t, patch)
	if !ok {
		t.Fatal("retry_policy missing")
	}
	if rp["num_retries"].(int64) != 2 {
		t.Errorf("num_retries: got %v", rp["num_retries"])
	}
}

func TestBuildCatchAllVirtualHostPatch_RespectsRetryPolicy(t *testing.T) {
	entry := CatchAllEntry{
		Hostname:   "example.com",
		BackendRef: v1alpha1.BackendRef{Name: "default-backend", Namespace: "default", Port: 80},
	}

	patch := buildCatchAllVirtualHostPatch(epaWithRetryPolicy(nil), entry)
	routeSlice := patch["patch"].(map[string]interface{})["value"].(map[string]interface{})["routes"].([]interface{})
	dynRoute := routeSlice[0].(map[string]interface{})["route"].(map[string]interface{})
	if _, present := dynRoute["retry_policy"]; present {
		t.Error("retry_policy should be absent when EPA has no retryPolicy configured")
	}

	patch = buildCatchAllVirtualHostPatch(epaWithNumRetries(3), entry)
	routeSlice = patch["patch"].(map[string]interface{})["value"].(map[string]interface{})["routes"].([]interface{})
	dynRoute = routeSlice[0].(map[string]interface{})["route"].(map[string]interface{})
	rp, ok := dynRoute["retry_policy"].(map[string]interface{})
	if !ok {
		t.Fatal("retry_policy missing from dynamic route")
	}
	if rp["num_retries"].(int64) != 3 {
		t.Errorf("num_retries: got %v", rp["num_retries"])
	}
}

func TestBuildCatchAllVirtualHostPatch_TimeoutOverride(t *testing.T) {
	entry := CatchAllEntry{
		Hostname:   "example.com",
		BackendRef: v1alpha1.BackendRef{Name: "default-backend", Namespace: "default", Port: 80},
	}
	epa := &v1alpha1.ExternalProcessorAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: "epa"},
		Spec: v1alpha1.ExternalProcessorAttachmentSpec{
			GatewayRef:   v1alpha1.GatewayRef{Selector: map[string]string{"app": "gw"}},
			RouteTimeout: "10s",
		},
	}
	patch := buildCatchAllVirtualHostPatch(epa, entry)
	routeSlice := patch["patch"].(map[string]interface{})["value"].(map[string]interface{})["routes"].([]interface{})
	for i, r := range routeSlice {
		route := r.(map[string]interface{})["route"].(map[string]interface{})
		if route["timeout"] != "10s" {
			t.Errorf("routes[%d] timeout: got %v, want 10s", i, route["timeout"])
		}
	}
}

func TestBuildMirrorEnvoyFilter_RespectsRetryPolicy(t *testing.T) {
	entries := []MirrorEntry{{
		Hostname: testHostA,
		Route:    routes.Route{Path: "/x", Type: routes.RouteTypeExact},
		Mirror: routes.RouteMirror{
			BackendRef: v1alpha1.BackendRef{Name: "shadow", Namespace: "default", Port: 80},
		},
	}}

	// nil retryPolicy → no retry_policy emitted
	ef, err := BuildMirrorEnvoyFilter(epaWithRetryPolicy(nil), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	patches := getNestedSlice(ef.Object, "spec", "configPatches")
	if _, ok := retryPolicyFromPatch(t, patches[0].(map[string]interface{})); ok {
		t.Error("retry_policy should be absent")
	}

	// configured numRetries → retry_policy emitted with that value
	ef, err = BuildMirrorEnvoyFilter(epaWithNumRetries(5), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	patches = getNestedSlice(ef.Object, "spec", "configPatches")
	rp, ok := retryPolicyFromPatch(t, patches[0].(map[string]interface{}))
	if !ok || rp["num_retries"].(int64) != 5 {
		t.Errorf("expected num_retries=5, got rp=%v", rp)
	}
}

func TestBuildCORSEnvoyFilter_RespectsRetryPolicy(t *testing.T) {
	entries := []CORSEntry{{
		Hostname: "a.example.com",
		Route:    routes.Route{Path: "/x", Type: routes.RouteTypeExact},
		Policy:   routes.RouteCORS{AllowOrigins: []string{"https://app.example.com"}},
	}}

	ef, err := BuildCORSEnvoyFilter(epaWithRetryPolicy(nil), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	patches := getNestedSlice(ef.Object, "spec", "configPatches")
	if _, ok := retryPolicyFromPatch(t, patches[0].(map[string]interface{})); ok {
		t.Error("retry_policy should be absent")
	}

	ef, err = BuildCORSEnvoyFilter(epaWithNumRetries(7), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	patches = getNestedSlice(ef.Object, "spec", "configPatches")
	rp, ok := retryPolicyFromPatch(t, patches[0].(map[string]interface{}))
	if !ok || rp["num_retries"].(int64) != 7 {
		t.Errorf("expected num_retries=7, got rp=%v", rp)
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
