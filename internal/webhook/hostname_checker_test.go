/*
Copyright 2026.

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

func TestCheckCustomHTTPRouteHostnames(t *testing.T) {
	tests := []struct {
		name           string
		route          *customrouterv1alpha1.CustomHTTPRoute
		existingCR     []customrouterv1alpha1.CustomHTTPRoute
		existingHR     []gatewayv1.HTTPRoute
		wantErr        bool
		errContains    string
		errNotContains string
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
			errContains: "path conflict",
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
			name:  "self-update allowed",
			route: newCustomHTTPRoute("route-a", "default", "default", []string{"example.com"}),
			existingCR: []customrouterv1alpha1.CustomHTTPRoute{
				*newCustomHTTPRoute("route-a", "default", "default", []string{"example.com"}),
			},
			wantErr: false,
		},
		{
			name:  "conflict — with HTTPRoute",
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
			err := checker.CheckCustomHTTPRouteHostnames(context.Background(), tt.route)

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
			name:      "conflict — same hostname",
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
