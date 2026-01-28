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

package v1alpha1

import (
	"strings"
	"testing"
)

func TestValidateCustomHTTPRoute(t *testing.T) {
	tests := []struct {
		name        string
		route       *CustomHTTPRoute
		wantErr     bool
		errContains string
	}{
		{
			name: "valid route with backend",
			route: &CustomHTTPRoute{
				Spec: CustomHTTPRouteSpec{
					TargetRef: TargetRef{Name: "default"},
					Hostnames: []string{"example.com"},
					Rules: []Rule{
						{
							Matches: []PathMatch{{Path: "/api"}},
							BackendRefs: []BackendRef{
								{Name: "api", Namespace: "default", Port: 8080},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid redirect without backend",
			route: &CustomHTTPRoute{
				Spec: CustomHTTPRouteSpec{
					TargetRef: TargetRef{Name: "default"},
					Hostnames: []string{"example.com"},
					Rules: []Rule{
						{
							Matches: []PathMatch{{Path: "/old"}},
							Actions: []Action{
								{
									Type:     ActionTypeRedirect,
									Redirect: &RedirectConfig{Path: "/new", StatusCode: 301},
								},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid: no backend and no redirect",
			route: &CustomHTTPRoute{
				Spec: CustomHTTPRouteSpec{
					TargetRef: TargetRef{Name: "default"},
					Hostnames: []string{"example.com"},
					Rules: []Rule{
						{
							Matches: []PathMatch{{Path: "/api"}},
							Actions: []Action{
								{
									Type:    ActionTypeRewrite,
									Rewrite: &RewriteConfig{Path: "/v2/api"},
								},
							},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "backendRefs is required",
		},
		{
			name: "invalid: redirect without config",
			route: &CustomHTTPRoute{
				Spec: CustomHTTPRouteSpec{
					TargetRef: TargetRef{Name: "default"},
					Hostnames: []string{"example.com"},
					Rules: []Rule{
						{
							Matches: []PathMatch{{Path: "/old"}},
							Actions: []Action{
								{Type: ActionTypeRedirect},
							},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "redirect config is required",
		},
		{
			name: "invalid: redirect with empty config",
			route: &CustomHTTPRoute{
				Spec: CustomHTTPRouteSpec{
					TargetRef: TargetRef{Name: "default"},
					Hostnames: []string{"example.com"},
					Rules: []Rule{
						{
							Matches: []PathMatch{{Path: "/old"}},
							Actions: []Action{
								{
									Type:     ActionTypeRedirect,
									Redirect: &RedirectConfig{},
								},
							},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "at least one redirect field",
		},
		{
			name: "invalid: rewrite without config",
			route: &CustomHTTPRoute{
				Spec: CustomHTTPRouteSpec{
					TargetRef: TargetRef{Name: "default"},
					Hostnames: []string{"example.com"},
					Rules: []Rule{
						{
							Matches: []PathMatch{{Path: "/api"}},
							Actions: []Action{
								{Type: ActionTypeRewrite},
							},
							BackendRefs: []BackendRef{
								{Name: "api", Namespace: "default", Port: 8080},
							},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "rewrite config is required",
		},
		{
			name: "invalid: rewrite with empty config",
			route: &CustomHTTPRoute{
				Spec: CustomHTTPRouteSpec{
					TargetRef: TargetRef{Name: "default"},
					Hostnames: []string{"example.com"},
					Rules: []Rule{
						{
							Matches: []PathMatch{{Path: "/api"}},
							Actions: []Action{
								{
									Type:    ActionTypeRewrite,
									Rewrite: &RewriteConfig{},
								},
							},
							BackendRefs: []BackendRef{
								{Name: "api", Namespace: "default", Port: 8080},
							},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "at least one rewrite field",
		},
		{
			name: "invalid: header-set without config",
			route: &CustomHTTPRoute{
				Spec: CustomHTTPRouteSpec{
					TargetRef: TargetRef{Name: "default"},
					Hostnames: []string{"example.com"},
					Rules: []Rule{
						{
							Matches: []PathMatch{{Path: "/api"}},
							Actions: []Action{
								{Type: ActionTypeHeaderSet},
							},
							BackendRefs: []BackendRef{
								{Name: "api", Namespace: "default", Port: 8080},
							},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "header config is required",
		},
		{
			name: "invalid: header-set without name",
			route: &CustomHTTPRoute{
				Spec: CustomHTTPRouteSpec{
					TargetRef: TargetRef{Name: "default"},
					Hostnames: []string{"example.com"},
					Rules: []Rule{
						{
							Matches: []PathMatch{{Path: "/api"}},
							Actions: []Action{
								{
									Type:   ActionTypeHeaderSet,
									Header: &HeaderConfig{Value: "test"},
								},
							},
							BackendRefs: []BackendRef{
								{Name: "api", Namespace: "default", Port: 8080},
							},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "header.name is required",
		},
		{
			name: "invalid: header-remove without headerName",
			route: &CustomHTTPRoute{
				Spec: CustomHTTPRouteSpec{
					TargetRef: TargetRef{Name: "default"},
					Hostnames: []string{"example.com"},
					Rules: []Rule{
						{
							Matches: []PathMatch{{Path: "/api"}},
							Actions: []Action{
								{Type: ActionTypeHeaderRemove},
							},
							BackendRefs: []BackendRef{
								{Name: "api", Namespace: "default", Port: 8080},
							},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "headerName is required",
		},
		{
			name: "valid: multiple actions with redirect",
			route: &CustomHTTPRoute{
				Spec: CustomHTTPRouteSpec{
					TargetRef: TargetRef{Name: "default"},
					Hostnames: []string{"example.com"},
					Rules: []Rule{
						{
							Matches: []PathMatch{{Path: "/old"}},
							Actions: []Action{
								{
									Type:     ActionTypeRedirect,
									Redirect: &RedirectConfig{Path: "/new", StatusCode: 301},
								},
							},
						},
						{
							Matches: []PathMatch{{Path: "/api"}},
							Actions: []Action{
								{
									Type:    ActionTypeRewrite,
									Rewrite: &RewriteConfig{Path: "/v2/api"},
								},
								{
									Type:   ActionTypeHeaderSet,
									Header: &HeaderConfig{Name: "X-Test", Value: "value"},
								},
							},
							BackendRefs: []BackendRef{
								{Name: "api", Namespace: "default", Port: 8080},
							},
						},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.route.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errContains)
					return
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestHasRedirectAction(t *testing.T) {
	tests := []struct {
		name     string
		rule     Rule
		expected bool
	}{
		{
			name:     "no actions",
			rule:     Rule{},
			expected: false,
		},
		{
			name: "only rewrite action",
			rule: Rule{
				Actions: []Action{
					{Type: ActionTypeRewrite, Rewrite: &RewriteConfig{Path: "/new"}},
				},
			},
			expected: false,
		},
		{
			name: "has redirect action",
			rule: Rule{
				Actions: []Action{
					{Type: ActionTypeRedirect, Redirect: &RedirectConfig{Path: "/new"}},
				},
			},
			expected: true,
		},
		{
			name: "redirect among other actions",
			rule: Rule{
				Actions: []Action{
					{Type: ActionTypeHeaderSet, Header: &HeaderConfig{Name: "X-Test", Value: "v"}},
					{Type: ActionTypeRedirect, Redirect: &RedirectConfig{Path: "/new"}},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rule.HasRedirectAction()
			if got != tt.expected {
				t.Errorf("HasRedirectAction() = %v, want %v", got, tt.expected)
			}
		})
	}
}
