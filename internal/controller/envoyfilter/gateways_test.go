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
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

func ptr[T any](v T) *T { return &v }

// gateway builds a Gateway of class "istio" in the "istio" namespace.
func gateway(name, address string, listeners ...gatewayv1.Listener) gatewayv1.Gateway {
	gw := gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "istio", Name: name},
		Spec:       gatewayv1.GatewaySpec{GatewayClassName: "istio", Listeners: listeners},
	}
	if address != "" {
		gw.Spec.Addresses = []gatewayv1.GatewaySpecAddress{{Type: ptr(gatewayv1.HostnameAddressType), Value: address}}
	}
	return gw
}

func listener(name, hostname string) gatewayv1.Listener {
	l := gatewayv1.Listener{Name: gatewayv1.SectionName(name), Port: 80, Protocol: gatewayv1.HTTPProtocolType}
	if hostname != "" {
		l.Hostname = ptr(gatewayv1.Hostname(hostname))
	}
	return l
}

func service(ns, name string, selector map[string]string) corev1.Service {
	return corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, Spec: corev1.ServiceSpec{Selector: selector}}
}

func httpRoute(ns, name string, hostnames []string, parents ...gatewayv1.ParentReference) gatewayv1.HTTPRoute {
	hr := gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	for _, h := range hostnames {
		hr.Spec.Hostnames = append(hr.Spec.Hostnames, gatewayv1.Hostname(h))
	}
	hr.Spec.ParentRefs = parents
	return hr
}

func parent(ns, name, section string) gatewayv1.ParentReference {
	p := gatewayv1.ParentReference{Name: gatewayv1.ObjectName(name)}
	if ns != "" {
		p.Namespace = ptr(gatewayv1.Namespace(ns))
	}
	if section != "" {
		p.SectionName = ptr(gatewayv1.SectionName(section))
	}
	return p
}

func gatewayEPA(gateways ...v1alpha1.GatewayReference) *v1alpha1.ExternalProcessorAttachment {
	epa := vhostEPA(&v1alpha1.VirtualHostsConfig{Mode: v1alpha1.VirtualHostsModeAuto, Gateways: gateways})
	epa.Spec.GatewayRef.Selector = map[string]string{"istio": "gateway-stable-standalone"}
	return epa
}

func ownedKeys(owned map[types.NamespacedName]*gatewayv1.Gateway) []string {
	out := make([]string, 0, len(owned))
	for k := range owned {
		out = append(out, k.String())
	}
	sort.Strings(out)
	return out
}

func TestOwnedGateways(t *testing.T) {
	gateways := &gatewayv1.GatewayList{Items: []gatewayv1.Gateway{
		// gke-enterprise: Gateway istio/stable fronted by the standalone gateway Service.
		gateway("stable", "istio-gateway-stable-standalone.istio.svc.cluster.local", listener("proxy", "")),
		// No address: Istio's default Service "<gateway>-<class>".
		gateway("internal", "", listener("http", "")),
		// Another workload.
		gateway("other", "other-gw.istio.svc", listener("http", "")),
		// Service not found: cannot be told apart, counts as owned (never risk a duplicate domain).
		gateway("unresolved", "missing.istio.svc", listener("http", "")),
	}}
	services := &corev1.ServiceList{Items: []corev1.Service{
		service("istio", "istio-gateway-stable-standalone", map[string]string{"istio": "gateway-stable-standalone", "app": "x"}),
		service("istio", "internal-istio", map[string]string{"istio": "gateway-stable-standalone"}),
		service("istio", "other-gw", map[string]string{"istio": "other"}),
	}}

	got := ownedKeys(OwnedGateways(gatewayEPA(), gateways, services))
	if want := []string{"istio/internal", "istio/stable", "istio/unresolved"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("owned = %v, want %v", got, want)
	}

	override := gatewayEPA(v1alpha1.GatewayReference{Namespace: "istio", Name: "other"})
	if got := ownedKeys(OwnedGateways(override, gateways, services)); !reflect.DeepEqual(got, []string{"istio/other"}) {
		t.Fatalf("explicit gateways must replace the automatic resolution, got %v", got)
	}
}

func TestClaimedHTTPRouteHostnames(t *testing.T) {
	stable := gateway("stable", "",
		listener("proxy", ""), listener("auth", "auth.example.com"), listener("wild", "*.apps.example.com"),
		listener("foo", "foo.example.com"))
	owned := map[types.NamespacedName]*gatewayv1.Gateway{{Namespace: "istio", Name: "stable"}: &stable}

	routes := &gatewayv1.HTTPRouteList{Items: []gatewayv1.HTTPRoute{
		// Own gateway, explicit hostnames, namespace given.
		httpRoute("keycloak", "console", []string{"console.example.com"}, parent("istio", "stable", "proxy")),
		// Another gateway: must not claim.
		httpRoute("app", "elsewhere", []string{"id.example.com"}, parent("istio", "other", "")),
		// Namespace defaults to the route's: istio/stable.
		httpRoute("istio", "local", []string{"local.example.com"}, parent("", "stable", "proxy")),
		// Namespace defaults to the route's: app/stable, not owned.
		httpRoute("app", "not-local", []string{"nope.example.com"}, parent("", "stable", "proxy")),
		// No hostnames on a listener with hostname: inherits it.
		httpRoute("keycloak", "auth", nil, parent("istio", "stable", "auth")),
		// No hostnames on a listener without hostname: claims nothing concrete.
		httpRoute("app", "catch", nil, parent("istio", "stable", "proxy")),
		// Hostnames restricted to those the wildcard listener covers.
		httpRoute("app", "apps", []string{"a.apps.example.com", "z.other.com"}, parent("istio", "stable", "wild")),
		// Route wildcard on a precise listener: only the intersection (the listener's
		// hostname), never the whole "*.example.com" suffix.
		httpRoute("app", "narrow", []string{"*.example.com"}, parent("istio", "stable", "foo")),
		// Route wildcard narrower than the listener wildcard: the route's.
		httpRoute("app", "deeper", []string{"*.eu.apps.example.com"}, parent("istio", "stable", "wild")),
		// Non-Gateway parent: ignored.
		httpRoute("app", "svc", []string{"svc.example.com"}, gatewayv1.ParentReference{
			Kind: ptr(gatewayv1.Kind("Service")), Name: "stable", Namespace: ptr(gatewayv1.Namespace("istio")),
		}),
	}}

	got := ClaimedHTTPRouteHostnames(routes, owned)
	sort.Strings(got)
	want := []string{"*.eu.apps.example.com", "a.apps.example.com", "auth.example.com", "console.example.com", "foo.example.com", "local.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claimed = %v, want %v", got, want)
	}
}

func TestIntersectHostnames(t *testing.T) {
	cases := []struct {
		listener, route, want string
		ok                    bool
	}{
		{"foo.example.com", "*.example.com", "foo.example.com", true},
		{"*.example.com", "foo.example.com", "foo.example.com", true},
		{"*.example.com", "a.b.example.com", "a.b.example.com", true},
		{"*.example.com", "*.a.example.com", "*.a.example.com", true},
		{"*.a.example.com", "*.example.com", "*.a.example.com", true},
		{"foo.example.com", "bar.example.com", "", false},
		{"*.example.com", "example.com", "", false},
		{"*.example.com", "test.example.net", "", false},
	}
	for _, c := range cases {
		got, ok := intersectHostnames(c.listener, c.route)
		if got != c.want || ok != c.ok {
			t.Errorf("intersectHostnames(%q, %q) = %q, %v; want %q, %v", c.listener, c.route, got, ok, c.want, c.ok)
		}
	}
}

func TestIsGatewayService(t *testing.T) {
	gateways := &gatewayv1.GatewayList{Items: []gatewayv1.Gateway{
		gateway("stable", "istio-gateway-stable-standalone.istio.svc.cluster.local", listener("proxy", "")),
		gateway("internal", "", listener("http", "")),
	}}
	for svc, want := range map[types.NamespacedName]bool{
		{Namespace: "istio", Name: "istio-gateway-stable-standalone"}: true,
		{Namespace: "istio", Name: "internal-istio"}:                  true,
		{Namespace: "istio", Name: "stable-istio"}:                    false, // has an address: default Service not used
		{Namespace: "app", Name: "istio-gateway-stable-standalone"}:   false,
	} {
		if got := IsGatewayService(gateways, svc); got != want {
			t.Errorf("IsGatewayService(%s) = %v, want %v", svc, got, want)
		}
	}
}

func TestClaimedHTTPRouteHostnames_OnlyHTTPRouteListeners(t *testing.T) {
	tcp := listener("tcp", "tcp.example.com")
	tcp.Protocol = gatewayv1.TCPProtocolType
	tls := listener("tls", "tls.example.com")
	tls.Protocol = gatewayv1.TLSProtocolType
	grpcOnly := listener("grpc", "grpc.example.com")
	grpcOnly.AllowedRoutes = &gatewayv1.AllowedRoutes{Kinds: []gatewayv1.RouteGroupKind{{Kind: "GRPCRoute"}}}
	httpsAllowed := listener("https", "https.example.com")
	httpsAllowed.Protocol = gatewayv1.HTTPSProtocolType
	httpsAllowed.AllowedRoutes = &gatewayv1.AllowedRoutes{Kinds: []gatewayv1.RouteGroupKind{{Kind: "HTTPRoute"}}}

	gw := gateway("stable", "", tcp, tls, grpcOnly, httpsAllowed)
	owned := map[types.NamespacedName]*gatewayv1.Gateway{{Namespace: "istio", Name: "stable"}: &gw}
	routes := &gatewayv1.HTTPRouteList{Items: []gatewayv1.HTTPRoute{
		// No sectionName: only the HTTPS listener that allows HTTPRoute counts.
		httpRoute("app", "all", nil, parent("istio", "stable", "")),
		// sectionName to a TLS listener: attaches to nothing.
		httpRoute("app", "tls", []string{"tls.example.com"}, parent("istio", "stable", "tls")),
	}}
	got := ClaimedHTTPRouteHostnames(routes, owned)
	if want := []string{"https.example.com"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("claimed = %v, want %v", got, want)
	}
}
