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

	"github.com/freepik-company/customrouter/pkg/routes"
)

func TestBuildRouteMatch(t *testing.T) {
	tests := []struct {
		name  string
		route routes.Route
		want  map[string]interface{}
	}{
		{
			name:  "exact path",
			route: routes.Route{Path: "/foo", Type: routes.RouteTypeExact},
			want: map[string]interface{}{
				"path": "/foo",
			},
		},
		{
			name:  "prefix path uses path_separated_prefix",
			route: routes.Route{Path: "/api", Type: routes.RouteTypePrefix},
			want: map[string]interface{}{
				"path_separated_prefix": "/api",
			},
		},
		{
			name:  "prefix path trims trailing slash",
			route: routes.Route{Path: "/api/", Type: routes.RouteTypePrefix},
			want: map[string]interface{}{
				"path_separated_prefix": "/api",
			},
		},
		{
			name:  "root prefix degrades to plain prefix",
			route: routes.Route{Path: "/", Type: routes.RouteTypePrefix},
			want: map[string]interface{}{
				"prefix": "/",
			},
		},
		{
			name:  "regex path uses safe_regex",
			route: routes.Route{Path: "^/users/[0-9]+$", Type: routes.RouteTypeRegex},
			want: map[string]interface{}{
				"safe_regex": map[string]interface{}{
					"regex": "^/users/[0-9]+$",
				},
			},
		},
		{
			name: "method emitted as :method pseudo-header",
			route: routes.Route{
				Path:   "/foo",
				Type:   routes.RouteTypeExact,
				Method: "POST",
			},
			want: map[string]interface{}{
				"path": "/foo",
				"headers": []interface{}{
					map[string]interface{}{
						"name":        ":method",
						"exact_match": "POST",
					},
				},
			},
		},
		{
			name: "exact header",
			route: routes.Route{
				Path: "/foo",
				Type: routes.RouteTypeExact,
				Headers: []routes.RouteHeaderMatch{
					{Name: "x-env", Value: "prod"},
				},
			},
			want: map[string]interface{}{
				"path": "/foo",
				"headers": []interface{}{
					map[string]interface{}{
						"name":        "x-env",
						"exact_match": "prod",
					},
				},
			},
		},
		{
			name: "regex header",
			route: routes.Route{
				Path: "/foo",
				Type: routes.RouteTypeExact,
				Headers: []routes.RouteHeaderMatch{
					{Name: "x-tier", Value: "^gold|platinum$", Type: routes.HeaderMatchRegex},
				},
			},
			want: map[string]interface{}{
				"path": "/foo",
				"headers": []interface{}{
					map[string]interface{}{
						"name": "x-tier",
						"safe_regex_match": map[string]interface{}{
							"regex": "^gold|platinum$",
						},
					},
				},
			},
		},
		{
			name: "method comes before header matchers",
			route: routes.Route{
				Path:   "/foo",
				Type:   routes.RouteTypeExact,
				Method: "GET",
				Headers: []routes.RouteHeaderMatch{
					{Name: "x-env", Value: "prod"},
				},
			},
			want: map[string]interface{}{
				"path": "/foo",
				"headers": []interface{}{
					map[string]interface{}{
						"name":        ":method",
						"exact_match": "GET",
					},
					map[string]interface{}{
						"name":        "x-env",
						"exact_match": "prod",
					},
				},
			},
		},
		{
			name: "exact query parameter",
			route: routes.Route{
				Path: "/foo",
				Type: routes.RouteTypeExact,
				QueryParams: []routes.RouteQueryParamMatch{
					{Name: "v", Value: "2"},
				},
			},
			want: map[string]interface{}{
				"path": "/foo",
				"query_parameters": []interface{}{
					map[string]interface{}{
						"name": "v",
						"string_match": map[string]interface{}{
							"exact": "2",
						},
					},
				},
			},
		},
		{
			name: "regex query parameter",
			route: routes.Route{
				Path: "/foo",
				Type: routes.RouteTypeExact,
				QueryParams: []routes.RouteQueryParamMatch{
					{Name: "v", Value: "^[0-9]+$", Type: routes.HeaderMatchRegex},
				},
			},
			want: map[string]interface{}{
				"path": "/foo",
				"query_parameters": []interface{}{
					map[string]interface{}{
						"name": "v",
						"string_match": map[string]interface{}{
							"safe_regex": map[string]interface{}{
								"regex": "^[0-9]+$",
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildRouteMatch(&tt.route)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BuildRouteMatch() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
