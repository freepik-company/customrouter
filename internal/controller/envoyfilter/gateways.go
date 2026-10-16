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
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

// OwnedGateways returns the Gateways served by the attachment's gateway workload, keyed
// by namespace/name. With virtualHosts.gateways set it is exactly that list. Otherwise a
// Gateway is owned when a Service behind it selects the attachment's pods: the Service is
// the one named by a Hostname address "<svc>.<ns>.svc[.cluster.local]" or, without such
// an address, the one Istio creates for it ("<gateway>-<gatewayClassName>" in the
// Gateway's namespace); it matches when gatewayRef.selector is a subset of its selector.
// A Gateway whose Service cannot be found counts as owned (see below).
func OwnedGateways(
	epa *v1alpha1.ExternalProcessorAttachment,
	gatewayList *gatewayv1.GatewayList,
	serviceList *corev1.ServiceList,
) map[types.NamespacedName]*gatewayv1.Gateway {
	owned := map[types.NamespacedName]*gatewayv1.Gateway{}
	byKey := map[types.NamespacedName]*gatewayv1.Gateway{}
	if gatewayList != nil {
		for i := range gatewayList.Items {
			gw := &gatewayList.Items[i]
			byKey[types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}] = gw
		}
	}

	if epa.Spec.VirtualHosts != nil && len(epa.Spec.VirtualHosts.Gateways) > 0 {
		for _, ref := range epa.Spec.VirtualHosts.Gateways {
			key := types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}
			// Kept even if the Gateway is not listed yet: its HTTPRoutes still claim
			// their own hostnames (only listener hostnames are unknown).
			owned[key] = byKey[key]
		}
		return owned
	}

	services := map[types.NamespacedName]*corev1.Service{}
	if serviceList != nil {
		for i := range serviceList.Items {
			svc := &serviceList.Items[i]
			services[types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}] = svc
		}
	}
	for key, gw := range byKey {
		// Conservative on purpose: a Gateway is foreign only when a Service behind it is
		// found and selects other pods. One that cannot be resolved (address is an IP, its
		// Service is missing) counts as owned, so its HTTPRoutes keep their hostnames: a
		// wrongly skipped hostname only costs a 404, a wrongly added virtual host duplicates
		// a domain and invalidates the whole gateway route configuration.
		resolved := false
		for _, svcKey := range gatewayServices(gw) {
			svc, ok := services[svcKey]
			if !ok {
				continue
			}
			resolved = true
			if selectorSubset(epa.Spec.GatewayRef.Selector, svc.Spec.Selector) {
				owned[key] = gw
				break
			}
		}
		if !resolved {
			owned[key] = gw
		}
	}
	return owned
}

// IsGatewayService reports whether the Service may front one of the Gateways (see
// gatewayServices), so that a change to it can change Gateway ownership.
func IsGatewayService(gatewayList *gatewayv1.GatewayList, service types.NamespacedName) bool {
	if gatewayList == nil {
		return false
	}
	for i := range gatewayList.Items {
		for _, candidate := range gatewayServices(&gatewayList.Items[i]) {
			if candidate == service {
				return true
			}
		}
	}
	return false
}

// gatewayServices lists the Services that may front a Gateway: those named by its
// Hostname addresses, or Istio's default Service when it has none.
func gatewayServices(gw *gatewayv1.Gateway) []types.NamespacedName {
	var out []types.NamespacedName
	for _, addr := range gw.Spec.Addresses {
		if addr.Type == nil || *addr.Type != gatewayv1.HostnameAddressType {
			continue
		}
		host := strings.TrimSuffix(strings.TrimSuffix(addr.Value, "."), ".cluster.local")
		parts := strings.Split(host, ".")
		if len(parts) == 3 && parts[2] == "svc" {
			out = append(out, types.NamespacedName{Namespace: parts[1], Name: parts[0]})
		}
	}
	if len(out) == 0 {
		out = append(out, types.NamespacedName{
			Namespace: gw.Namespace,
			Name:      gw.Name + "-" + string(gw.Spec.GatewayClassName),
		})
	}
	return out
}

// selectorSubset reports whether every label of want is in got (an empty want never
// matches: an attachment always selects by at least one label).
func selectorSubset(want, got map[string]string) bool {
	if len(want) == 0 {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

// ClaimedHTTPRouteHostnames returns the hostnames HTTPRoutes attached to an owned
// Gateway give a virtual host on the attachment's workload, so no automatic one may be
// added for them. Only parentRefs to an owned Gateway count (group and kind default to
// the Gateway API Gateway, namespace to the route's).
//
// An HTTPRoute with hostnames claims those (restricted, when the referenced listener has
// a hostname, to the ones it covers). One without hostnames inherits the hostname of
// the listener it attaches to; when that listener has no hostname it claims nothing
// concrete: Istio serves it from a "*" virtual host, and Envoy prefers an exact domain
// over "*", so an automatic virtual host for a specific hostname does not collide with it.
func ClaimedHTTPRouteHostnames(
	httpRouteList *gatewayv1.HTTPRouteList,
	owned map[types.NamespacedName]*gatewayv1.Gateway,
) []string {
	if httpRouteList == nil || len(owned) == 0 {
		return nil
	}
	var claimed []string
	for i := range httpRouteList.Items {
		hr := &httpRouteList.Items[i]
		for _, parent := range hr.Spec.ParentRefs {
			if parent.Group != nil && *parent.Group != gatewayv1.GroupName {
				continue
			}
			if parent.Kind != nil && *parent.Kind != "Gateway" {
				continue
			}
			ns := hr.Namespace
			if parent.Namespace != nil {
				ns = string(*parent.Namespace)
			}
			gw, ok := owned[types.NamespacedName{Namespace: ns, Name: string(parent.Name)}]
			if !ok {
				continue
			}
			claimed = append(claimed, routeHostnamesOnGateway(hr, gw, parent.SectionName)...)
		}
	}
	return claimed
}

func routeHostnamesOnGateway(hr *gatewayv1.HTTPRoute, gw *gatewayv1.Gateway, section *gatewayv1.SectionName) []string {
	routeHostnames := make([]string, 0, len(hr.Spec.Hostnames))
	for _, h := range hr.Spec.Hostnames {
		routeHostnames = append(routeHostnames, string(h))
	}
	if gw == nil {
		// Override to a Gateway not found: listener hostnames are unknown.
		return routeHostnames
	}

	var claimed []string
	for i := range gw.Spec.Listeners {
		l := &gw.Spec.Listeners[i]
		if section != nil && l.Name != *section {
			continue
		}
		// An HTTPRoute only attaches to HTTP/HTTPS listeners that allow its kind; a
		// sectionName pointing elsewhere (TCP, TLS…) attaches to nothing.
		if !listenerAcceptsHTTPRoute(l) {
			continue
		}
		if l.Hostname == nil || *l.Hostname == "" {
			claimed = append(claimed, routeHostnames...)
			continue
		}
		listenerHostname := string(*l.Hostname)
		if len(routeHostnames) == 0 {
			claimed = append(claimed, listenerHostname)
			continue
		}
		for _, h := range routeHostnames {
			if effective, ok := intersectHostnames(listenerHostname, h); ok {
				claimed = append(claimed, effective)
			}
		}
	}
	return claimed
}

// listenerAcceptsHTTPRoute reports whether an HTTPRoute can attach to the listener:
// protocol HTTP or HTTPS and, when allowedRoutes.kinds is set, HTTPRoute among them
// (group defaulting to the Gateway API group).
func listenerAcceptsHTTPRoute(l *gatewayv1.Listener) bool {
	if l.Protocol != gatewayv1.HTTPProtocolType && l.Protocol != gatewayv1.HTTPSProtocolType {
		return false
	}
	if l.AllowedRoutes == nil || len(l.AllowedRoutes.Kinds) == 0 {
		return true
	}
	for _, k := range l.AllowedRoutes.Kinds {
		if k.Kind == "HTTPRoute" && (k.Group == nil || *k.Group == gatewayv1.GroupName) {
			return true
		}
	}
	return false
}

// intersectHostnames is the hostname a route hostname and a listener hostname serve
// together, per Gateway API v1 (HTTPRoute.spec.hostnames): the more specific of the
// two when one covers the other, nothing otherwise. A listener "foo.example.com" with a
// route "*.example.com" serves only "foo.example.com" (that is the virtual host domain
// Istio programs), not the whole suffix.
func intersectHostnames(listener, route string) (string, bool) {
	switch {
	case hostnameCovers(listener, route):
		return route, true
	case hostnameCovers(route, listener):
		return listener, true
	default:
		return "", false
	}
}
