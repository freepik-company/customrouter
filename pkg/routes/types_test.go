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
			name:      "exact match blog post title",
			route:     Route{Path: "/blog-post-title", Type: RouteTypeExact},
			path:      "/blog-post-title",
			wantMatch: true,
		},
		{
			name:      "exact no match blog post title subpath",
			route:     Route{Path: "/blog-post-title", Type: RouteTypeExact},
			path:      "/blog-post-title/comments",
			wantMatch: false,
		},
		{
			name:      "exact match audio sitemap",
			route:     Route{Path: "/audio-sitemap.xml", Type: RouteTypeExact},
			path:      "/audio-sitemap.xml",
			wantMatch: true,
		},
		{
			name:      "exact no match audio sitemap different extension",
			route:     Route{Path: "/audio-sitemap.xml", Type: RouteTypeExact},
			path:      "/audio-sitemap.json",
			wantMatch: false,
		},
		{
			name:      "exact match assets json",
			route:     Route{Path: "/assets.json", Type: RouteTypeExact},
			path:      "/assets.json",
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
			name:      "prefix no match partial segment",
			route:     Route{Path: "/academy", Type: RouteTypePrefix},
			path:      "/academy-test",
			wantMatch: false,
		},
		{
			name:      "prefix no match partial multi-segment",
			route:     Route{Path: "/api/v1", Type: RouteTypePrefix},
			path:      "/api/v1extra",
			wantMatch: false,
		},
		{
			name:      "prefix no match sitemap with shared start",
			route:     Route{Path: "/audio", Type: RouteTypePrefix},
			path:      "/audio-sitemap.xml",
			wantMatch: false,
		},
		{
			name:      "prefix multi-segment no match different leaf",
			route:     Route{Path: "/audio/api", Type: RouteTypePrefix},
			path:      "/audio/api-docs",
			wantMatch: false,
		},
		{
			name:      "prefix multi-segment matches subpath",
			route:     Route{Path: "/audio/api", Type: RouteTypePrefix},
			path:      "/audio/api/v1",
			wantMatch: true,
		},
		{
			name:      "prefix multi-segment matches exact",
			route:     Route{Path: "/audio/api", Type: RouteTypePrefix},
			path:      "/audio/api",
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
			route:     Route{Path: "/blog", Type: RouteTypePrefix},
			path:      "/blog-post-title",
			wantMatch: false,
		},
		{
			name:      "prefix no match suffix dot extension",
			route:     Route{Path: "/assets", Type: RouteTypePrefix},
			path:      "/assets.json",
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
