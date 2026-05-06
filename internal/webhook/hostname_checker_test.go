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

package webhook

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	customrouterv1alpha1 "github.com/freepik-company/customrouter/api/v1alpha1"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = customrouterv1alpha1.AddToScheme(s)
	_ = gatewayv1.Install(s)
	return s
}

func newCustomHTTPRoute(name, namespace, target string, hostnames []string) *customrouterv1alpha1.CustomHTTPRoute {
	return newCustomHTTPRouteWithPaths(name, namespace, target, hostnames,
		[]customrouterv1alpha1.PathMatch{{Path: "/", Type: customrouterv1alpha1.MatchTypePathPrefix}},
	)
}

func newCustomHTTPRouteWithPaths(name, namespace, target string, hostnames []string, matches []customrouterv1alpha1.PathMatch) *customrouterv1alpha1.CustomHTTPRoute {
	return &customrouterv1alpha1.CustomHTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(namespace + "/" + name),
		},
		Spec: customrouterv1alpha1.CustomHTTPRouteSpec{
			TargetRef: customrouterv1alpha1.TargetRef{Name: target},
			Hostnames: hostnames,
			Rules: []customrouterv1alpha1.Rule{
				{
					Matches:     matches,
					BackendRefs: []customrouterv1alpha1.BackendRef{{Name: "svc", Namespace: "default", Port: 80}},
				},
			},
		},
	}
}

func newCustomHTTPRouteWithPathPrefixes(name string, hostnames []string, matches []customrouterv1alpha1.PathMatch, prefixes *customrouterv1alpha1.PathPrefixes) *customrouterv1alpha1.CustomHTTPRoute {
	cr := newCustomHTTPRouteWithPaths(name, "web", "default", hostnames, matches)
	cr.Spec.PathPrefixes = prefixes
	return cr
}

func newCustomHTTPRouteWithOverlap(hostnames []string, matches []customrouterv1alpha1.PathMatch, allowOverlap bool) *customrouterv1alpha1.CustomHTTPRoute {
	cr := newCustomHTTPRouteWithPaths("route-a", "default", "default", hostnames, matches)
	for i := range cr.Spec.Rules {
		cr.Spec.Rules[i].AllowOverlap = allowOverlap
	}
	return cr
}

func newHTTPRoute(hostnames []string) *gatewayv1.HTTPRoute {
	ghs := make([]gatewayv1.Hostname, len(hostnames))
	for i, h := range hostnames {
		ghs[i] = gatewayv1.Hostname(h)
	}
	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hr-a",
			Namespace: "default",
		},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: ghs,
		},
	}
}

func newHTTPRouteWithMatches(hostnames []string, matches []gatewayv1.HTTPRouteMatch) *gatewayv1.HTTPRoute {
	ghs := make([]gatewayv1.Hostname, len(hostnames))
	for i, h := range hostnames {
		ghs[i] = gatewayv1.Hostname(h)
	}
	hr := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hr-a",
			Namespace: "default",
		},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: ghs,
		},
	}
	if len(matches) > 0 {
		hr.Spec.Rules = []gatewayv1.HTTPRouteRule{
			{Matches: matches},
		}
	}
	return hr
}

func ptrTo[T any](v T) *T {
	return &v
}

func TestCheckCustomHTTPRouteHostnames(t *testing.T) {
	tests := []struct {
		name            string
		route           *customrouterv1alpha1.CustomHTTPRoute
		existingCR      []customrouterv1alpha1.CustomHTTPRoute
		existingHR      []gatewayv1.HTTPRoute
		wantErr         bool
		errContains     string
		errNotContains  string
		wantWarnings    bool
		warningContains string
	}{
		{
			name:    "no conflict — empty cluster",
			route:   newCustomHTTPRoute("route-a", "default", "default", []string{"example.com"}),
			wantErr: false,
		},
		{
			name:  "no conflict — different hostnames same target",
			route: newCustomHTTPRoute("route-a", "default", "default", []string{"a.example.com"}),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRoute("route-b", "default", "default", []string{"b.example.com"}),
			},
			wantErr: false,
		},
		{
			name:  "no conflict — same hostnames different target",
			route: newCustomHTTPRoute("route-a", "default", "target-1", []string{"example.com"}),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRoute("route-b", "default", "target-2", []string{"example.com"}),
			},
			wantErr: false,
		},
		{
			name:  "conflict — same target, same hostname, same path",
			route: newCustomHTTPRoute("route-a", "default", "default", []string{"example.com"}),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRoute("route-b", "default", "default", []string{"example.com"}),
			},
			wantErr:     true,
			errContains: "route conflict",
		},
		{
			name: "no conflict — same target, same hostname, different paths",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/web", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				),
			},
			wantErr: false,
		},
		{
			name: "no conflict — same hostname, same path string, different match type",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypeExact}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				),
			},
			wantErr: false,
		},
		{
			name: "no conflict — same path, different method",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix, Method: "GET"}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix, Method: "POST"}},
				),
			},
			wantErr: false,
		},
		{
			name: "conflict — same path, same method",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix, Method: "GET"}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix, Method: "GET"}},
				),
			},
			wantErr:     true,
			errContains: "route conflict",
		},
		{
			name: "no conflict — method-restricted existing is strictly more specific",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix, Method: "GET"}},
				),
			},
			wantErr: false,
		},
		{
			name: "no conflict — header-restricted new route is strictly more specific",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{
					Path: "/checkout", Type: customrouterv1alpha1.MatchTypePathPrefix,
					Headers: []customrouterv1alpha1.HeaderMatch{{Name: "env", Value: "staging"}},
				}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/checkout", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				),
			},
			wantErr: false,
		},
		{
			name: "no conflict — header-restricted existing is strictly more specific",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/checkout", Type: customrouterv1alpha1.MatchTypePathPrefix}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{
						Path: "/checkout", Type: customrouterv1alpha1.MatchTypePathPrefix,
						Headers: []customrouterv1alpha1.HeaderMatch{{Name: "env", Value: "staging"}},
					}},
				),
			},
			wantErr: false,
		},
		{
			name: "conflict — regex header counted in specificity (same count as exact, no tie-break)",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{
					Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix,
					Headers: []customrouterv1alpha1.HeaderMatch{
						{Name: "X-Tenant", Value: ".*", Type: customrouterv1alpha1.HeaderMatchTypeRegularExpression},
					},
				}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{
						Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix,
						Headers: []customrouterv1alpha1.HeaderMatch{{Name: "X-Env", Value: "prod"}},
					}},
				),
			},
			wantErr:     true,
			errContains: "route conflict",
		},
		{
			name: "conflict — less-specific has higher priority and shadows the more-specific rule",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{
					Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix,
					Headers:  []customrouterv1alpha1.HeaderMatch{{Name: "env", Value: "staging"}},
					Priority: 500,
				}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix, Priority: 2000}},
				),
			},
			wantErr:     true,
			errContains: "route conflict",
		},
		{
			name: "no conflict — same path+method, different required header",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{
					Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix,
					Headers: []customrouterv1alpha1.HeaderMatch{{Name: "X-Tenant", Value: "acme"}},
				}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{
						Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix,
						Headers: []customrouterv1alpha1.HeaderMatch{{Name: "X-Tenant", Value: "widgets"}},
					}},
				),
			},
			wantErr: false,
		},
		{
			name: "conflict — same path+method, same required header",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{
					Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix,
					Headers: []customrouterv1alpha1.HeaderMatch{{Name: "X-Tenant", Value: "acme"}},
				}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{
						Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix,
						Headers: []customrouterv1alpha1.HeaderMatch{{Name: "X-Tenant", Value: "acme"}},
					}},
				),
			},
			wantErr:     true,
			errContains: "route conflict",
		},
		{
			name:  "self-update allowed",
			route: newCustomHTTPRoute("route-a", "default", "default", []string{"example.com"}),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRoute("route-a", "default", "default", []string{"example.com"}),
			},
			wantErr: false,
		},
		{
			name:  "conflict — with HTTPRoute (catch-all)",
			route: newCustomHTTPRoute("route-a", "default", "default", []string{"example.com"}),
			existingHR: []gatewayv1.HTTPRoute{
				*newHTTPRoute([]string{"example.com"}),
			},
			wantErr:     true,
			errContains: "HTTPRoute",
		},
		{
			name:  "no conflict — HTTPRoute without hostnames (inherits from Gateway)",
			route: newCustomHTTPRoute("route-a", "default", "default", []string{"example.com"}),
			existingHR: []gatewayv1.HTTPRoute{
				*newHTTPRoute(nil),
			},
			wantErr: false,
		},
		{
			name:  "partial hostname overlap — same path conflicts",
			route: newCustomHTTPRoute("route-a", "default", "default", []string{"a.com", "b.com", "c.com"}),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRoute("route-b", "default", "default", []string{"b.com", "d.com"}),
			},
			wantErr:     true,
			errContains: "b.com",
		},
		{
			name: "partial hostname overlap — different paths no conflict",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"a.com", "b.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"b.com", "d.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/web", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				),
			},
			wantErr: false,
		},
		{
			name:  "conflict across namespaces — same target same path",
			route: newCustomHTTPRoute("route-a", "ns1", "default", []string{"example.com"}),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRoute("route-b", "ns2", "default", []string{"example.com"}),
			},
			wantErr:     true,
			errContains: "example.com",
		},
		// --- Path-aware HTTPRoute conflict detection ---
		{
			name: "no conflict — HTTPRoute with different path",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
			),
			existingHR: []gatewayv1.HTTPRoute{
				*newHTTPRouteWithMatches([]string{"example.com"}, []gatewayv1.HTTPRouteMatch{
					{Path: &gatewayv1.HTTPPathMatch{
						Type:  ptrTo(gatewayv1.PathMatchPathPrefix),
						Value: ptrTo("/webhooks"),
					}},
				}),
			},
			wantErr: false,
		},
		{
			name: "conflict — HTTPRoute with same path",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
			),
			existingHR: []gatewayv1.HTTPRoute{
				*newHTTPRouteWithMatches([]string{"example.com"}, []gatewayv1.HTTPRouteMatch{
					{Path: &gatewayv1.HTTPPathMatch{
						Type:  ptrTo(gatewayv1.PathMatchPathPrefix),
						Value: ptrTo("/api"),
					}},
				}),
			},
			wantErr:     true,
			errContains: "route conflict",
		},
		{
			name: "no conflict — HTTPRoute with same path and headers is strictly more specific",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
			),
			existingHR: []gatewayv1.HTTPRoute{
				*newHTTPRouteWithMatches([]string{"example.com"}, []gatewayv1.HTTPRouteMatch{
					{
						Path: &gatewayv1.HTTPPathMatch{
							Type:  ptrTo(gatewayv1.PathMatchPathPrefix),
							Value: ptrTo("/api"),
						},
						Headers: []gatewayv1.HTTPHeaderMatch{
							{Name: "X-Version", Value: "v1"},
						},
					},
				}),
			},
			wantErr: false,
		},
		// --- PathPrefixes expansion ---
		{
			name: "no conflict — regex with {prefix} and disjoint prefix sets (Required policy)",
			route: newCustomHTTPRouteWithPathPrefixes("route-a",
				[]string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "^/_next/data/[^/]+/{prefix}([/.]|$)", Type: customrouterv1alpha1.MatchTypeRegex}},
				&customrouterv1alpha1.PathPrefixes{Policy: customrouterv1alpha1.PathPrefixPolicyRequired, Values: []string{"so", "sw", "za", "zu"}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPathPrefixes("route-b",
					[]string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "^/_next/data/[^/]+/{prefix}([/.]|$)", Type: customrouterv1alpha1.MatchTypeRegex}},
					&customrouterv1alpha1.PathPrefixes{Policy: customrouterv1alpha1.PathPrefixPolicyRequired, Values: []string{"ceb", "jp", "ph"}},
				),
			},
			wantErr: false,
		},
		{
			name: "conflict — regex with {prefix} and same prefix sets",
			route: newCustomHTTPRouteWithPathPrefixes("route-a",
				[]string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "^/_next/data/[^/]+/{prefix}([/.]|$)", Type: customrouterv1alpha1.MatchTypeRegex}},
				&customrouterv1alpha1.PathPrefixes{Policy: customrouterv1alpha1.PathPrefixPolicyRequired, Values: []string{"es", "fr"}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPathPrefixes("route-b",
					[]string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "^/_next/data/[^/]+/{prefix}([/.]|$)", Type: customrouterv1alpha1.MatchTypeRegex}},
					&customrouterv1alpha1.PathPrefixes{Policy: customrouterv1alpha1.PathPrefixPolicyRequired, Values: []string{"es", "fr"}},
				),
			},
			wantErr:     true,
			errContains: "route conflict",
		},
		{
			name: "no conflict — PathPrefix with Required policy and disjoint prefixes",
			route: newCustomHTTPRouteWithPathPrefixes("route-a",
				[]string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/user/me", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				&customrouterv1alpha1.PathPrefixes{Policy: customrouterv1alpha1.PathPrefixPolicyRequired, Values: []string{"es", "fr"}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPathPrefixes("route-b",
					[]string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/user/me", Type: customrouterv1alpha1.MatchTypePathPrefix}},
					&customrouterv1alpha1.PathPrefixes{Policy: customrouterv1alpha1.PathPrefixPolicyRequired, Values: []string{"de", "it"}},
				),
			},
			wantErr: false,
		},
		{
			name: "conflict — PathPrefix with Optional policy shares unprefixed path",
			route: newCustomHTTPRouteWithPathPrefixes("route-a",
				[]string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/user/me", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				&customrouterv1alpha1.PathPrefixes{Policy: customrouterv1alpha1.PathPrefixPolicyOptional, Values: []string{"es", "fr"}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPathPrefixes("route-b",
					[]string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/user/me", Type: customrouterv1alpha1.MatchTypePathPrefix}},
					&customrouterv1alpha1.PathPrefixes{Policy: customrouterv1alpha1.PathPrefixPolicyOptional, Values: []string{"de", "it"}},
				),
			},
			wantErr:     true,
			errContains: "route conflict",
		},
		// --- Trailing slash normalization ---
		{
			name: "conflict — trailing slash normalized (CustomHTTPRoute /api/ vs CustomHTTPRoute /api)",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/api/", Type: customrouterv1alpha1.MatchTypePathPrefix}},
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				),
			},
			wantErr:     true,
			errContains: "route conflict",
		},
		// --- Method-aware HTTPRoute conflict detection ---
		{
			name: "no conflict — HTTPRoute method-restricted matches are strictly more specific",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
			),
			existingHR: []gatewayv1.HTTPRoute{
				*newHTTPRouteWithMatches([]string{"example.com"}, []gatewayv1.HTTPRouteMatch{
					{
						Path: &gatewayv1.HTTPPathMatch{
							Type:  ptrTo(gatewayv1.PathMatchPathPrefix),
							Value: ptrTo("/api"),
						},
						Method: ptrTo(gatewayv1.HTTPMethodGet),
					},
					{
						Path: &gatewayv1.HTTPPathMatch{
							Type:  ptrTo(gatewayv1.PathMatchPathPrefix),
							Value: ptrTo("/api"),
						},
						Method: ptrTo(gatewayv1.HTTPMethodPost),
					},
				}),
			},
			wantErr: false,
		},
		// --- QueryParam-aware HTTPRoute conflict detection ---
		{
			name: "no conflict — HTTPRoute with query params is strictly more specific",
			route: newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
			),
			existingHR: []gatewayv1.HTTPRoute{
				*newHTTPRouteWithMatches([]string{"example.com"}, []gatewayv1.HTTPRouteMatch{
					{
						Path: &gatewayv1.HTTPPathMatch{
							Type:  ptrTo(gatewayv1.PathMatchPathPrefix),
							Value: ptrTo("/api"),
						},
						QueryParams: []gatewayv1.HTTPQueryParamMatch{
							{Name: "version", Value: "v1"},
						},
					},
				}),
			},
			wantErr: false,
		},
		// --- allowOverlap tests ---
		{
			name: "allowOverlap=true + CustomHTTPRoute conflict — warning, no error",
			route: newCustomHTTPRouteWithOverlap([]string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}}, true,
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				),
			},
			wantErr:         false,
			wantWarnings:    true,
			warningContains: "route conflict",
		},
		{
			name: "allowOverlap=false + CustomHTTPRoute conflict — error (default behavior)",
			route: newCustomHTTPRouteWithOverlap([]string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}}, false,
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				),
			},
			wantErr:     true,
			errContains: "route conflict",
		},
		{
			name: "allowOverlap=true + HTTPRoute conflict — always error",
			route: newCustomHTTPRouteWithOverlap([]string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/", Type: customrouterv1alpha1.MatchTypePathPrefix}}, true,
			),
			existingHR: []gatewayv1.HTTPRoute{
				*newHTTPRoute([]string{"example.com"}),
			},
			wantErr:     true,
			errContains: "HTTPRoute",
		},
		{
			name: "mixed rules — one allowOverlap=true, one false, same path — error (conservative wins)",
			route: func() *customrouterv1alpha1.CustomHTTPRoute {
				cr := newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				)
				cr.Spec.Rules = append(cr.Spec.Rules, customrouterv1alpha1.Rule{
					Matches:      []customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
					BackendRefs:  []customrouterv1alpha1.BackendRef{{Name: "svc", Namespace: "default", Port: 80}},
					AllowOverlap: true,
				})
				return cr
			}(),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				),
			},
			wantErr:     true,
			errContains: "route conflict",
		},
		{
			name: "allowOverlap=true + multiple existing overlaps — multiple warnings",
			route: newCustomHTTPRouteWithOverlap([]string{"example.com"},
				[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}}, true,
			),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-b", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				),
				*newCustomHTTPRouteWithPaths("route-c", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				),
			},
			wantErr:         false,
			wantWarnings:    true,
			warningContains: "route conflict",
		},
		{
			name: "allowOverlap=true + pathPrefixes expansion — warning on expanded path",
			route: func() *customrouterv1alpha1.CustomHTTPRoute {
				cr := newCustomHTTPRouteWithPathPrefixes("route-a",
					[]string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/user/me", Type: customrouterv1alpha1.MatchTypePathPrefix}},
					&customrouterv1alpha1.PathPrefixes{Policy: customrouterv1alpha1.PathPrefixPolicyOptional, Values: []string{"es", "fr"}},
				)
				for i := range cr.Spec.Rules {
					cr.Spec.Rules[i].AllowOverlap = true
				}
				return cr
			}(),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPathPrefixes("route-b",
					[]string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/user/me", Type: customrouterv1alpha1.MatchTypePathPrefix}},
					&customrouterv1alpha1.PathPrefixes{Policy: customrouterv1alpha1.PathPrefixPolicyOptional, Values: []string{"de", "it"}},
				),
			},
			wantErr:         false,
			wantWarnings:    true,
			warningContains: "route conflict",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newScheme()
			var objs []runtime.Object
			for i := range tt.existingCR {
				objs = append(objs, &tt.existingCR[i])
			}
			for i := range tt.existingHR {
				objs = append(objs, &tt.existingHR[i])
			}

			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objs...).
				Build()

			checker := &HostnameChecker{Client: cl}
			warnings, err := checker.CheckCustomHTTPRouteHostnames(context.Background(), tt.route)

			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.errContains != "" && err != nil && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
			}
			if tt.errNotContains != "" && err != nil && strings.Contains(err.Error(), tt.errNotContains) {
				t.Errorf("error %q should not contain %q", err.Error(), tt.errNotContains)
			}
			if tt.wantWarnings && len(warnings) == 0 {
				t.Fatalf("expected warnings, got none")
			}
			if !tt.wantWarnings && len(warnings) > 0 {
				t.Fatalf("unexpected warnings: %v", warnings)
			}
			if tt.warningContains != "" && len(warnings) > 0 {
				joined := strings.Join(warnings, "; ")
				if !strings.Contains(joined, tt.warningContains) {
					t.Errorf("warnings %q should contain %q", joined, tt.warningContains)
				}
			}
		})
	}
}

func TestCheckHTTPRouteHostnames(t *testing.T) {
	tests := []struct {
		name        string
		httpRoute   *gatewayv1.HTTPRoute
		existingCR  []customrouterv1alpha1.CustomHTTPRoute
		wantErr     bool
		errContains string
	}{
		{
			name:      "no conflict — empty cluster",
			httpRoute: newHTTPRoute([]string{"example.com"}),
			wantErr:   false,
		},
		{
			name:      "no conflict — different hostnames",
			httpRoute: newHTTPRoute([]string{"a.example.com"}),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRoute("route-a", "default", "default", []string{"b.example.com"}),
			},
			wantErr: false,
		},
		{
			name:      "conflict — same hostname (catch-all)",
			httpRoute: newHTTPRoute([]string{"example.com"}),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRoute("route-a", "default", "default", []string{"example.com"}),
			},
			wantErr:     true,
			errContains: "CustomHTTPRoute",
		},
		{
			name:      "no hostnames on HTTPRoute — skip",
			httpRoute: newHTTPRoute(nil),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRoute("route-a", "default", "default", []string{"example.com"}),
			},
			wantErr: false,
		},
		{
			name:      "conflict — multiple CustomHTTPRoutes, one conflicts",
			httpRoute: newHTTPRoute([]string{"conflict.com"}),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRoute("route-a", "default", "default", []string{"other.com"}),
				*newCustomHTTPRoute("route-b", "other", "default", []string{"conflict.com"}),
			},
			wantErr:     true,
			errContains: "conflict.com",
		},
		// --- Path-aware conflict detection ---
		{
			name: "no conflict — same hostname, different paths",
			httpRoute: newHTTPRouteWithMatches([]string{"example.com"}, []gatewayv1.HTTPRouteMatch{
				{Path: &gatewayv1.HTTPPathMatch{
					Type:  ptrTo(gatewayv1.PathMatchPathPrefix),
					Value: ptrTo("/webhooks"),
				}},
			}),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				),
			},
			wantErr: false,
		},
		{
			name: "conflict — same hostname, same path",
			httpRoute: newHTTPRouteWithMatches([]string{"example.com"}, []gatewayv1.HTTPRouteMatch{
				{Path: &gatewayv1.HTTPPathMatch{
					Type:  ptrTo(gatewayv1.PathMatchPathPrefix),
					Value: ptrTo("/api"),
				}},
			}),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				),
			},
			wantErr:     true,
			errContains: "route conflict",
		},
		// --- Method-aware conflict detection ---
		{
			name: "no conflict — method-restricted HTTPRoute is strictly more specific",
			httpRoute: newHTTPRouteWithMatches([]string{"example.com"}, []gatewayv1.HTTPRouteMatch{
				{
					Path: &gatewayv1.HTTPPathMatch{
						Type:  ptrTo(gatewayv1.PathMatchPathPrefix),
						Value: ptrTo("/api"),
					},
					Method: ptrTo(gatewayv1.HTTPMethodGet),
				},
			}),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				),
			},
			wantErr: false,
		},
		// --- QueryParam-aware conflict detection ---
		{
			name: "no conflict — query-param-restricted HTTPRoute is strictly more specific",
			httpRoute: newHTTPRouteWithMatches([]string{"example.com"}, []gatewayv1.HTTPRouteMatch{
				{
					Path: &gatewayv1.HTTPPathMatch{
						Type:  ptrTo(gatewayv1.PathMatchPathPrefix),
						Value: ptrTo("/api"),
					},
					QueryParams: []gatewayv1.HTTPQueryParamMatch{
						{Name: "version", Value: "v1"},
					},
				},
			}),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRouteWithPaths("route-a", "default", "default", []string{"example.com"},
					[]customrouterv1alpha1.PathMatch{{Path: "/api", Type: customrouterv1alpha1.MatchTypePathPrefix}},
				),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newScheme()
			var objs []runtime.Object
			for i := range tt.existingCR {
				objs = append(objs, &tt.existingCR[i])
			}

			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objs...).
				Build()

			checker := &HostnameChecker{Client: cl}
			err := checker.CheckHTTPRouteHostnames(context.Background(), tt.httpRoute)

			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.errContains != "" && err != nil && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
			}
		})
	}
}

func TestHeadersCompatible(t *testing.T) {
	tests := []struct {
		name string
		a, b []headerMatch
		want bool
	}{
		{
			name: "both empty — compatible",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "one empty — compatible (empty matches all)",
			a:    []headerMatch{{Name: "X-Version", Value: "v1"}},
			b:    nil,
			want: true,
		},
		{
			name: "same headers — compatible",
			a:    []headerMatch{{Name: "X-Version", Value: "v1"}},
			b:    []headerMatch{{Name: "X-Version", Value: "v1"}},
			want: true,
		},
		{
			name: "different values for same header — incompatible",
			a:    []headerMatch{{Name: "X-Version", Value: "v1"}},
			b:    []headerMatch{{Name: "X-Version", Value: "v2"}},
			want: false,
		},
		{
			name: "different header names — compatible (no contradiction)",
			a:    []headerMatch{{Name: "X-Version", Value: "v1"}},
			b:    []headerMatch{{Name: "X-Env", Value: "prod"}},
			want: true,
		},
		{
			name: "case insensitive header name — incompatible",
			a:    []headerMatch{{Name: "X-Version", Value: "v1"}},
			b:    []headerMatch{{Name: "x-version", Value: "v2"}},
			want: false,
		},
		{
			name: "superset with same values — compatible",
			a:    []headerMatch{{Name: "X-Version", Value: "v1"}},
			b:    []headerMatch{{Name: "X-Version", Value: "v1"}, {Name: "X-Env", Value: "prod"}},
			want: true,
		},
		{
			name: "superset with different values — incompatible",
			a:    []headerMatch{{Name: "X-Version", Value: "v1"}},
			b:    []headerMatch{{Name: "X-Version", Value: "v2"}, {Name: "X-Env", Value: "prod"}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := headersCompatible(tt.a, tt.b); got != tt.want {
				t.Errorf("headersCompatible() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMethodsCompatible(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "both empty — compatible", a: "", b: "", want: true},
		{name: "one empty — compatible (empty matches all)", a: "GET", b: "", want: true},
		{name: "same method — compatible", a: "GET", b: "GET", want: true},
		{name: "different methods — incompatible", a: "GET", b: "POST", want: false},
		{name: "case insensitive — compatible", a: "get", b: "GET", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := methodsCompatible(tt.a, tt.b); got != tt.want {
				t.Errorf("methodsCompatible() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryParamsCompatible(t *testing.T) {
	tests := []struct {
		name string
		a, b []queryParamMatch
		want bool
	}{
		{
			name: "both empty — compatible",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "one empty — compatible (empty matches all)",
			a:    []queryParamMatch{{Name: "version", Value: "v1"}},
			b:    nil,
			want: true,
		},
		{
			name: "same params — compatible",
			a:    []queryParamMatch{{Name: "version", Value: "v1"}},
			b:    []queryParamMatch{{Name: "version", Value: "v1"}},
			want: true,
		},
		{
			name: "different values for same param — incompatible",
			a:    []queryParamMatch{{Name: "version", Value: "v1"}},
			b:    []queryParamMatch{{Name: "version", Value: "v2"}},
			want: false,
		},
		{
			name: "different param names — compatible (no contradiction)",
			a:    []queryParamMatch{{Name: "version", Value: "v1"}},
			b:    []queryParamMatch{{Name: "env", Value: "prod"}},
			want: true,
		},
		{
			name: "case sensitive param name — different names are compatible",
			a:    []queryParamMatch{{Name: "Version", Value: "v1"}},
			b:    []queryParamMatch{{Name: "version", Value: "v2"}},
			want: true, // RFC 3986: query param names are case-sensitive
		},
		{
			name: "superset with same values — compatible",
			a:    []queryParamMatch{{Name: "version", Value: "v1"}},
			b:    []queryParamMatch{{Name: "version", Value: "v1"}, {Name: "env", Value: "prod"}},
			want: true,
		},
		{
			name: "superset with different values — incompatible",
			a:    []queryParamMatch{{Name: "version", Value: "v1"}},
			b:    []queryParamMatch{{Name: "version", Value: "v2"}, {Name: "env", Value: "prod"}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := queryParamsCompatible(tt.a, tt.b); got != tt.want {
				t.Errorf("queryParamsCompatible() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "/", want: "/"},
		{input: "/api", want: "/api"},
		{input: "/api/", want: "/api"},
		{input: "/api/v1/", want: "/api/v1"},
		{input: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := normalizePath(tt.input); got != tt.want {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFindRouteMatchOverlap(t *testing.T) {
	tests := []struct {
		name string
		a, b []routeMatch
		want int // expected number of overlaps
	}{
		{
			name: "same path, no method/headers/params — overlap",
			a:    []routeMatch{{PathType: "PathPrefix", Path: "/api"}},
			b:    []routeMatch{{PathType: "PathPrefix", Path: "/api"}},
			want: 1,
		},
		{
			name: "same path, different methods — no overlap",
			a:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Method: "GET"}},
			b:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Method: "POST"}},
			want: 0,
		},
		{
			name: "same path, one empty method — no overlap (specificity tie-break)",
			a:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Method: "GET"}},
			b:    []routeMatch{{PathType: "PathPrefix", Path: "/api"}},
			want: 0,
		},
		{
			name: "same path, different query params — no overlap",
			a:    []routeMatch{{PathType: "PathPrefix", Path: "/api", QueryParams: []queryParamMatch{{Name: "v", Value: "1"}}}},
			b:    []routeMatch{{PathType: "PathPrefix", Path: "/api", QueryParams: []queryParamMatch{{Name: "v", Value: "2"}}}},
			want: 0,
		},
		{
			name: "same path, one empty query params — no overlap (specificity tie-break)",
			a:    []routeMatch{{PathType: "PathPrefix", Path: "/api", QueryParams: []queryParamMatch{{Name: "v", Value: "1"}}}},
			b:    []routeMatch{{PathType: "PathPrefix", Path: "/api"}},
			want: 0,
		},
		{
			name: "different paths — no overlap",
			a:    []routeMatch{{PathType: "PathPrefix", Path: "/api"}},
			b:    []routeMatch{{PathType: "PathPrefix", Path: "/web"}},
			want: 0,
		},
		{
			name: "same path, different headers — no overlap",
			a:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Headers: []headerMatch{{Name: "X-V", Value: "1"}}}},
			b:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Headers: []headerMatch{{Name: "X-V", Value: "2"}}}},
			want: 0,
		},
		{
			name: "all axes match — overlap",
			a: []routeMatch{{
				PathType: "PathPrefix", Path: "/api", Method: "GET",
				Headers:     []headerMatch{{Name: "X-V", Value: "1"}},
				QueryParams: []queryParamMatch{{Name: "env", Value: "prod"}},
			}},
			b: []routeMatch{{
				PathType: "PathPrefix", Path: "/api", Method: "GET",
				Headers:     []headerMatch{{Name: "X-V", Value: "1"}},
				QueryParams: []queryParamMatch{{Name: "env", Value: "prod"}},
			}},
			want: 1,
		},
		{
			name: "same path, one side has more headers — no overlap (specificity tie-break)",
			a:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Headers: []headerMatch{{Name: "X-V", Value: "1"}}}},
			b:    []routeMatch{{PathType: "PathPrefix", Path: "/api"}},
			want: 0,
		},
		{
			name: "same path, one side has more headers (compatible) — no overlap",
			a:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Headers: []headerMatch{{Name: "X-V", Value: "1"}}}},
			b: []routeMatch{{PathType: "PathPrefix", Path: "/api", Headers: []headerMatch{
				{Name: "X-V", Value: "1"}, {Name: "X-Env", Value: "prod"},
			}}},
			want: 0,
		},
		{
			name: "same path, same header count, different names — overlap",
			a:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Headers: []headerMatch{{Name: "X-V", Value: "1"}}}},
			b:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Headers: []headerMatch{{Name: "X-Env", Value: "prod"}}}},
			want: 1,
		},
		{
			name: "same path, one side has more query params — no overlap (specificity tie-break)",
			a:    []routeMatch{{PathType: "PathPrefix", Path: "/api", QueryParams: []queryParamMatch{{Name: "v", Value: "1"}}}},
			b:    []routeMatch{{PathType: "PathPrefix", Path: "/api", QueryParams: []queryParamMatch{{Name: "v", Value: "1"}, {Name: "env", Value: "prod"}}}},
			want: 0,
		},
		{
			name: "same path, regex header vs exact header — overlap (regex counted, no specificity ordering)",
			a:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Headers: []headerMatch{{Name: "X-V", Value: ".*", IsRegex: true}}}},
			b:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Headers: []headerMatch{{Name: "X-Env", Value: "prod"}}}},
			want: 1,
		},
		{
			name: "same path, more-specific has lower priority — overlap (shadowed by less-specific higher priority)",
			a:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Headers: []headerMatch{{Name: "X-V", Value: "1"}}, Priority: 500}},
			b:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Priority: 2000}},
			want: 1,
		},
		{
			name: "same path, more-specific has higher priority — no overlap",
			a:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Headers: []headerMatch{{Name: "X-V", Value: "1"}}, Priority: 2000}},
			b:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Priority: 500}},
			want: 0,
		},
		{
			name: "same path, more-specific has equal priority — no overlap",
			a:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Headers: []headerMatch{{Name: "X-V", Value: "1"}}, Priority: 1000}},
			b:    []routeMatch{{PathType: "PathPrefix", Path: "/api", Priority: 1000}},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findRouteMatchOverlap(tt.a, tt.b)
			if len(got) != tt.want {
				t.Errorf("findRouteMatchOverlap() returned %d overlaps, want %d", len(got), tt.want)
			}
		})
	}
}

func TestClassifyOverlaps(t *testing.T) {
	hosts := []string{"example.com"}
	ctx := "CustomHTTPRoute default/route-b (target \"default\")"

	tests := []struct {
		name         string
		newMatches   []routeMatch
		existing     []routeMatch
		wantWarnings int
		wantErrors   int
	}{
		{
			name:         "allowOverlap=true — warning",
			newMatches:   []routeMatch{{PathType: "PathPrefix", Path: "/api", AllowOverlap: true}},
			existing:     []routeMatch{{PathType: "PathPrefix", Path: "/api"}},
			wantWarnings: 1,
			wantErrors:   0,
		},
		{
			name:         "allowOverlap=false — error",
			newMatches:   []routeMatch{{PathType: "PathPrefix", Path: "/api", AllowOverlap: false}},
			existing:     []routeMatch{{PathType: "PathPrefix", Path: "/api"}},
			wantWarnings: 0,
			wantErrors:   1,
		},
		{
			name:         "no overlap — neither warnings nor errors",
			newMatches:   []routeMatch{{PathType: "PathPrefix", Path: "/api", AllowOverlap: true}},
			existing:     []routeMatch{{PathType: "PathPrefix", Path: "/web"}},
			wantWarnings: 0,
			wantErrors:   0,
		},
		{
			name: "multiple overlaps with allowOverlap=true — multiple warnings",
			newMatches: []routeMatch{
				{PathType: "PathPrefix", Path: "/api", AllowOverlap: true},
				{PathType: "PathPrefix", Path: "/web", AllowOverlap: true},
			},
			existing: []routeMatch{
				{PathType: "PathPrefix", Path: "/api"},
				{PathType: "PathPrefix", Path: "/web"},
			},
			wantWarnings: 2,
			wantErrors:   0,
		},
		{
			name: "mixed — one warning, one error",
			newMatches: []routeMatch{
				{PathType: "PathPrefix", Path: "/api", AllowOverlap: true},
				{PathType: "PathPrefix", Path: "/web", AllowOverlap: false},
			},
			existing: []routeMatch{
				{PathType: "PathPrefix", Path: "/api"},
				{PathType: "PathPrefix", Path: "/web"},
			},
			wantWarnings: 1,
			wantErrors:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyOverlaps(tt.newMatches, tt.existing, hosts, ctx)
			if len(result.Warnings) != tt.wantWarnings {
				t.Errorf("got %d warnings, want %d: %v", len(result.Warnings), tt.wantWarnings, result.Warnings)
			}
			if len(result.Errors) != tt.wantErrors {
				t.Errorf("got %d errors, want %d: %v", len(result.Errors), tt.wantErrors, result.Errors)
			}
		})
	}
}
