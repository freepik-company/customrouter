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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

func vhostEPA(cfg *v1alpha1.VirtualHostsConfig) *v1alpha1.ExternalProcessorAttachment {
	return &v1alpha1.ExternalProcessorAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: "gw", Namespace: "istio", UID: "uid-1"},
		Spec: v1alpha1.ExternalProcessorAttachmentSpec{
			GatewayRef:   v1alpha1.GatewayRef{Selector: map[string]string{"istio": "gateway"}},
			VirtualHosts: cfg,
		},
	}
}

func routesWithHostnames(hostnames ...[]string) *v1alpha1.CustomHTTPRouteList {
	list := &v1alpha1.CustomHTTPRouteList{}
	for i, hs := range hostnames {
		list.Items = append(list.Items, v1alpha1.CustomHTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "r" + string(rune('a'+i)), Namespace: "app"},
			Spec:       v1alpha1.CustomHTTPRouteSpec{Hostnames: hs},
		})
	}
	return list
}

func TestPlanVirtualHosts_DisabledByDefault(t *testing.T) {
	routes := routesWithHostnames([]string{"id.example.com"})
	for _, epa := range []*v1alpha1.ExternalProcessorAttachment{
		vhostEPA(nil),
		vhostEPA(&v1alpha1.VirtualHostsConfig{}),
		vhostEPA(&v1alpha1.VirtualHostsConfig{Mode: v1alpha1.VirtualHostsModeDisabled}),
	} {
		if plan := PlanVirtualHosts(epa, routes, nil, nil); len(plan.Managed) != 0 {
			t.Fatalf("virtual hosts must be opt-in, got %v", plan.Managed)
		}
	}
}

func TestPlanVirtualHosts_DedupesAndSkipsOtherOwners(t *testing.T) {
	epa := vhostEPA(&v1alpha1.VirtualHostsConfig{
		Mode:             v1alpha1.VirtualHostsModeAuto,
		ExcludeHostnames: []string{"manual.example.com", "*.legacy.example.com"},
	})
	deleting := metav1.Now()
	routes := routesWithHostnames(
		[]string{"id.example.com", "auth.example.com"},
		[]string{"id.example.com", "catch.example.com", "a.wild.example.com"},
		[]string{"manual.example.com", "x.legacy.example.com", "console.example.com"},
	)
	routes.Items = append(routes.Items, v1alpha1.CustomHTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "gone", Namespace: "app", DeletionTimestamp: &deleting, Finalizers: []string{"f"}},
		Spec:       v1alpha1.CustomHTTPRouteSpec{Hostnames: []string{"gone.example.com"}},
	})
	httpRoutes := &gatewayv1.HTTPRouteList{Items: []gatewayv1.HTTPRoute{{
		Spec: gatewayv1.HTTPRouteSpec{Hostnames: []gatewayv1.Hostname{"console.example.com", "*.wild.example.com"}},
	}}}

	plan := PlanVirtualHosts(epa, routes, []string{"catch.example.com"}, httpRoutes)

	if want := []string{"auth.example.com", "id.example.com"}; !reflect.DeepEqual(plan.Managed, want) {
		t.Fatalf("managed = %v, want %v", plan.Managed, want)
	}
	wantSkipped := map[string]string{
		"catch.example.com":    VirtualHostSkipCatchAll,
		"a.wild.example.com":   VirtualHostSkipHTTPRoute,
		"console.example.com":  VirtualHostSkipHTTPRoute,
		"manual.example.com":   VirtualHostSkipExcluded,
		"x.legacy.example.com": VirtualHostSkipExcluded,
	}
	if !reflect.DeepEqual(plan.Skipped, wantSkipped) {
		t.Fatalf("skipped = %v, want %v", plan.Skipped, wantSkipped)
	}
}

func TestHostnameCovers(t *testing.T) {
	cases := []struct {
		pattern, host string
		want          bool
	}{
		{"a.example.com", "a.example.com", true},
		{"*.example.com", "a.example.com", true},
		{"*.example.com", "a.b.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "badexample.com", false},
		{"example.com", "a.example.com", false},
	}
	for _, c := range cases {
		if got := hostnameCovers(c.pattern, c.host); got != c.want {
			t.Errorf("hostnameCovers(%q, %q) = %v, want %v", c.pattern, c.host, got, c.want)
		}
	}
}

func TestBuildVirtualHostsEnvoyFilter(t *testing.T) {
	epa := vhostEPA(&v1alpha1.VirtualHostsConfig{Mode: v1alpha1.VirtualHostsModeAuto})
	epa.Spec.RouteTimeout = "15s"

	obj, err := BuildVirtualHostsEnvoyFilter(epa, []string{"id.example.com"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if obj.GetName() != "gw-vhosts" || obj.GetNamespace() != "istio" {
		t.Fatalf("unexpected name %s/%s", obj.GetNamespace(), obj.GetName())
	}
	if refs := obj.GetOwnerReferences(); len(refs) != 1 || refs[0].UID != "uid-1" {
		t.Fatalf("must be owned by the EPA, got %v", refs)
	}
	patches, _, _ := unstructured.NestedSlice(obj.Object, "spec", "configPatches")
	if len(patches) != 1 {
		t.Fatalf("want 1 patch, got %d", len(patches))
	}
	patch := patches[0].(map[string]interface{})
	if patch["applyTo"] != "VIRTUAL_HOST" {
		t.Fatalf("applyTo = %v", patch["applyTo"])
	}
	value, _, _ := unstructured.NestedMap(patch, "patch", "value")
	if !reflect.DeepEqual(value["domains"], []interface{}{"id.example.com"}) {
		t.Fatalf("domains = %v", value["domains"])
	}
	routes := value["routes"].([]interface{})
	dynamic := routes[0].(map[string]interface{})["route"].(map[string]interface{})
	if dynamic["cluster_header"] != "x-customrouter-cluster" || dynamic["timeout"] != "15s" {
		t.Fatalf("dynamic route = %v", dynamic)
	}
	fallback := routes[1].(map[string]interface{})["direct_response"].(map[string]interface{})
	if fallback["status"] != int64(404) {
		t.Fatalf("default status = %v, want 404", fallback["status"])
	}

	epa.Spec.VirtualHosts.DefaultStatusCode = 421
	obj, _ = BuildVirtualHostsEnvoyFilter(epa, []string{"id.example.com"})
	patches, _, _ = unstructured.NestedSlice(obj.Object, "spec", "configPatches")
	routes, _, _ = unstructured.NestedSlice(patches[0].(map[string]interface{}), "patch", "value", "routes")
	if got := routes[1].(map[string]interface{})["direct_response"].(map[string]interface{})["status"]; got != int64(421) {
		t.Fatalf("custom status = %v, want 421", got)
	}
}
