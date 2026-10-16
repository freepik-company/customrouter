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
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

const (
	// VirtualHostsFilterSuffix names the EnvoyFilter holding the automatic virtual hosts.
	VirtualHostsFilterSuffix = "-vhosts"

	// DefaultVirtualHostStatusCode answers requests no CustomHTTPRoute matches.
	DefaultVirtualHostStatusCode int32 = 404

	// Reasons a CustomHTTPRoute hostname gets no automatic virtual host.
	VirtualHostSkipCatchAll  = "CatchAllRoute"
	VirtualHostSkipHTTPRoute = "HTTPRoute"
	VirtualHostSkipExcluded  = "Excluded"
)

// VirtualHostsPlan is the outcome of deciding which CustomHTTPRoute hostnames get a
// virtual host: Managed (sorted) and, for the rest, the reason they were skipped.
type VirtualHostsPlan struct {
	Managed []string
	Skipped map[string]string
}

// VirtualHostsEnabled reports whether the attachment asks for automatic virtual hosts.
func VirtualHostsEnabled(epa *v1alpha1.ExternalProcessorAttachment) bool {
	return epa.Spec.VirtualHosts != nil && epa.Spec.VirtualHosts.Mode == v1alpha1.VirtualHostsModeAuto
}

// hostnameCovers reports whether pattern (an exact hostname or a "*." wildcard) covers
// hostname. A wildcard is a suffix match over one or more labels: "*.example.com" covers
// "test.example.com" and "foo.test.example.com" but not "example.com". That is both the
// Gateway API v1 Hostname semantics (Listener.hostname, HTTPRoute.spec.hostnames) and
// how Envoy matches a "*.example.com" virtual host domain.
func hostnameCovers(pattern, hostname string) bool {
	if pattern == hostname {
		return true
	}
	if suffix, ok := strings.CutPrefix(pattern, "*"); ok && strings.HasPrefix(suffix, ".") {
		return strings.HasSuffix(hostname, suffix) && len(hostname) > len(suffix)
	}
	return false
}

func coveredBy(patterns []string, hostname string) bool {
	for _, p := range patterns {
		if hostnameCovers(p, hostname) {
			return true
		}
	}
	return false
}

// PlanVirtualHosts decides, for every hostname declared by a live CustomHTTPRoute,
// whether the attachment gives it a virtual host. A hostname is skipped when another
// owner already has its domain: a catchAllRoute hostname, an HTTPRoute of this
// attachment's gateway (httpRouteHostnames, see ClaimedHTTPRouteHostnames; its virtual
// host already carries the dynamic route of the "-routes" EnvoyFilter) or the explicit
// exclusion list. Two virtual hosts with the same domain would invalidate the gateway's
// route configuration, so the plan errs on the side of skipping.
func PlanVirtualHosts(
	epa *v1alpha1.ExternalProcessorAttachment,
	routeList *v1alpha1.CustomHTTPRouteList,
	catchAllHostnames []string,
	httpRouteHostnames []string,
) VirtualHostsPlan {
	plan := VirtualHostsPlan{Skipped: map[string]string{}}
	if !VirtualHostsEnabled(epa) || routeList == nil {
		return plan
	}

	excluded := epa.Spec.VirtualHosts.ExcludeHostnames

	seen := map[string]struct{}{}
	for i := range routeList.Items {
		route := &routeList.Items[i]
		if route.DeletionTimestamp != nil && !route.DeletionTimestamp.IsZero() {
			continue
		}
		for _, hostname := range route.Spec.Hostnames {
			if _, ok := seen[hostname]; ok {
				continue
			}
			seen[hostname] = struct{}{}
			switch {
			case coveredBy(excluded, hostname):
				plan.Skipped[hostname] = VirtualHostSkipExcluded
			case coveredBy(catchAllHostnames, hostname):
				plan.Skipped[hostname] = VirtualHostSkipCatchAll
			case coveredBy(httpRouteHostnames, hostname):
				plan.Skipped[hostname] = VirtualHostSkipHTTPRoute
			default:
				plan.Managed = append(plan.Managed, hostname)
			}
		}
	}
	sort.Strings(plan.Managed)
	return plan
}

// BuildVirtualHostsEnvoyFilter builds the "<attachment>-vhosts" EnvoyFilter: one
// VIRTUAL_HOST ADD per managed hostname with the header-gated dynamic route (set by the
// ext_proc when a CustomHTTPRoute matches) and a direct response for everything else.
func BuildVirtualHostsEnvoyFilter(
	epa *v1alpha1.ExternalProcessorAttachment,
	hostnames []string,
) (*unstructured.Unstructured, error) {
	envoyFilter := &unstructured.Unstructured{}
	envoyFilter.SetGroupVersionKind(GVK)
	envoyFilter.SetName(epa.Name + VirtualHostsFilterSuffix)
	envoyFilter.SetNamespace(epa.Namespace)
	envoyFilter.SetLabels(StandardLabels(epa.Name))
	envoyFilter.SetOwnerReferences([]metav1.OwnerReference{NewOwnerReference(epa)})

	status := DefaultVirtualHostStatusCode
	if epa.Spec.VirtualHosts != nil && epa.Spec.VirtualHosts.DefaultStatusCode != 0 {
		status = epa.Spec.VirtualHosts.DefaultStatusCode
	}

	patches := make([]interface{}, 0, len(hostnames))
	for _, hostname := range hostnames {
		dynamicRoute := map[string]interface{}{
			"cluster_header": "x-customrouter-cluster",
			"timeout":        GetRouteTimeout(epa),
		}
		ApplyRetryPolicy(dynamicRoute, epa)

		patches = append(patches, map[string]interface{}{
			"applyTo": "VIRTUAL_HOST",
			"match":   map[string]interface{}{"context": "GATEWAY"},
			"patch": map[string]interface{}{
				"operation": "ADD",
				"value": map[string]interface{}{
					"name":    fmt.Sprintf("customrouter-vhost-%s", hostname),
					"domains": []interface{}{hostname},
					"routes": []interface{}{
						map[string]interface{}{
							"name": "customrouter-dynamic-route",
							"match": map[string]interface{}{
								"prefix": "/",
								"headers": []interface{}{
									map[string]interface{}{
										"name":          "x-customrouter-cluster",
										"present_match": true,
									},
								},
							},
							"route": dynamicRoute,
						},
						map[string]interface{}{
							"name":            "customrouter-no-route",
							"match":           map[string]interface{}{"prefix": "/"},
							"direct_response": map[string]interface{}{"status": int64(status)},
						},
					},
				},
			},
		})
	}

	spec := map[string]interface{}{
		"workloadSelector": map[string]interface{}{
			"labels": SelectorToInterface(epa.Spec.GatewayRef.Selector),
		},
		"configPatches": patches,
	}
	if err := unstructured.SetNestedField(envoyFilter.Object, spec, "spec"); err != nil {
		return nil, fmt.Errorf("failed to set spec: %w", err)
	}
	return envoyFilter, nil
}
