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
		{
			name:      "exact match hyphenated path",
			route:     Route{Path: "/foo-bar-baz", Type: RouteTypeExact},
			path:      "/foo-bar-baz",
			wantMatch: true,
		},
		{
			name:      "exact no match hyphenated path subpath",
			route:     Route{Path: "/foo-bar-baz", Type: RouteTypeExact},
			path:      "/foo-bar-baz/child",
			wantMatch: false,
		},
		{
			name:      "exact match path with extension",
			route:     Route{Path: "/data-export.xml", Type: RouteTypeExact},
			path:      "/data-export.xml",
			wantMatch: true,
		},
		{
			name:      "exact no match path with different extension",
			route:     Route{Path: "/data-export.xml", Type: RouteTypeExact},
			path:      "/data-export.json",
			wantMatch: false,
		},
		{
			name:      "exact match dotted path",
			route:     Route{Path: "/config.json", Type: RouteTypeExact},
			path:      "/config.json",
			wantMatch: true,
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

		// Segment boundary - prefix must not match partial segments
		{
			name:      "prefix no match partial segment hyphen",
			route:     Route{Path: "/app", Type: RouteTypePrefix},
			path:      "/app-settings",
			wantMatch: false,
		},
		{
			name:      "prefix no match partial multi-segment",
			route:     Route{Path: "/api/v1", Type: RouteTypePrefix},
			path:      "/api/v1extra",
			wantMatch: false,
		},
		{
			name:      "prefix no match partial segment with extension",
			route:     Route{Path: "/data", Type: RouteTypePrefix},
			path:      "/data-export.xml",
			wantMatch: false,
		},
		{
			name:      "prefix multi-segment no match partial leaf",
			route:     Route{Path: "/svc/internal", Type: RouteTypePrefix},
			path:      "/svc/internal-docs",
			wantMatch: false,
		},
		{
			name:      "prefix multi-segment matches subpath",
			route:     Route{Path: "/svc/internal", Type: RouteTypePrefix},
			path:      "/svc/internal/v1",
			wantMatch: true,
		},
		{
			name:      "prefix multi-segment matches exact",
			route:     Route{Path: "/svc/internal", Type: RouteTypePrefix},
			path:      "/svc/internal",
			wantMatch: true,
		},
		{
			name:      "prefix root matches everything",
			route:     Route{Path: "/", Type: RouteTypePrefix},
			path:      "/anything/here",
			wantMatch: true,
		},
		{
			name:      "prefix no match suffix hyphenated word",
			route:     Route{Path: "/item", Type: RouteTypePrefix},
			path:      "/item-detail-page",
			wantMatch: false,
		},
		{
			name:      "prefix no match suffix dot extension",
			route:     Route{Path: "/config", Type: RouteTypePrefix},
			path:      "/config.json",
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
