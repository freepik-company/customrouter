package routes

import (
	"testing"
)

func TestRouteMatch(t *testing.T) {
	tests := []struct {
		name      string
		route     Route
		path      string
		wantMatch bool
	}{
		// Exact match
		{
			name:      "exact match",
			route:     Route{Path: "/foo", Type: RouteTypeExact},
			path:      "/foo",
			wantMatch: true,
		},
		{
			name:      "exact no match",
			route:     Route{Path: "/foo", Type: RouteTypeExact},
			path:      "/foo/bar",
			wantMatch: false,
		},

		// Prefix match basics
		{
			name:      "prefix match exact",
			route:     Route{Path: "/api/v1", Type: RouteTypePrefix},
			path:      "/api/v1",
			wantMatch: true,
		},
		{
			name:      "prefix match subpath",
			route:     Route{Path: "/api/v1", Type: RouteTypePrefix},
			path:      "/api/v1/users",
			wantMatch: true,
		},
		{
			name:      "prefix match query string",
			route:     Route{Path: "/api/v1", Type: RouteTypePrefix},
			path:      "/api/v1?key=123",
			wantMatch: true,
		},
		{
			name:      "prefix no match",
			route:     Route{Path: "/api/v1", Type: RouteTypePrefix},
			path:      "/api/v2",
			wantMatch: false,
		},

		// Prefix with trailing slash - Gateway API HTTPRoute behavior
		{
			name: "trailing slash prefix matches without slash",
			route: Route{
				Path: "/api/v1/", Type: RouteTypePrefix,
			},
			path:      "/api/v1",
			wantMatch: true,
		},
		{
			name: "trailing slash prefix matches with slash",
			route: Route{
				Path: "/api/v1/", Type: RouteTypePrefix,
			},
			path:      "/api/v1/",
			wantMatch: true,
		},
		{
			name: "trailing slash prefix matches subpath",
			route: Route{
				Path: "/api/v1/", Type: RouteTypePrefix,
			},
			path:      "/api/v1/users",
			wantMatch: true,
		},
		{
			name: "trailing slash prefix no match different path",
			route: Route{
				Path: "/api/v1/", Type: RouteTypePrefix,
			},
			path:      "/api/v2",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.route.Match(tt.path)
			if got != tt.wantMatch {
				t.Errorf(
					"Route{Path: %q, Type: %q}.Match(%q) = %v, want %v",
					tt.route.Path, tt.route.Type, tt.path,
					got, tt.wantMatch,
				)
			}
		})
	}
}
