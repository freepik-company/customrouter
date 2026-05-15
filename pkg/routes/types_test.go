package routes

import (
	"bytes"
	"encoding/json"
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
			got := tt.route.Match(RequestMatch{Path: tt.path})
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

func TestRouteMatchQueryParams(t *testing.T) {
	tests := []struct {
		name      string
		route     Route
		req       RequestMatch
		wantMatch bool
	}{
		{
			name:      "no query constraint matches any request",
			route:     Route{Path: "/api", Type: RouteTypePrefix},
			req:       RequestMatch{Path: "/api", QueryParams: map[string]string{"debug": "1"}},
			wantMatch: true,
		},
		{
			name: "exact query match succeeds",
			route: Route{Path: "/api", Type: RouteTypePrefix, QueryParams: []RouteQueryParamMatch{
				{Name: "version", Value: "2"},
			}},
			req:       RequestMatch{Path: "/api", QueryParams: map[string]string{"version": "2"}},
			wantMatch: true,
		},
		{
			name: "exact query value mismatch",
			route: Route{Path: "/api", Type: RouteTypePrefix, QueryParams: []RouteQueryParamMatch{
				{Name: "version", Value: "2"},
			}},
			req:       RequestMatch{Path: "/api", QueryParams: map[string]string{"version": "1"}},
			wantMatch: false,
		},
		{
			name: "query name is case-sensitive (miss)",
			route: Route{Path: "/api", Type: RouteTypePrefix, QueryParams: []RouteQueryParamMatch{
				{Name: "Version", Value: "2"},
			}},
			req:       RequestMatch{Path: "/api", QueryParams: map[string]string{"version": "2"}},
			wantMatch: false,
		},
		{
			name: "regex query match",
			route: Route{Path: "/api", Type: RouteTypePrefix, QueryParams: []RouteQueryParamMatch{
				{Name: "token", Value: "^[a-f0-9]+$", Type: HeaderMatchRegex},
			}},
			req:       RequestMatch{Path: "/api", QueryParams: map[string]string{"token": "deadbeef"}},
			wantMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.route.Match(tt.req)
			if got != tt.wantMatch {
				t.Errorf("Match(%+v) on Route{QueryParams:%+v} = %v, want %v",
					tt.req, tt.route.QueryParams, got, tt.wantMatch)
			}
		})
	}
}

func TestRouteMatchHeaders(t *testing.T) {
	tests := []struct {
		name      string
		route     Route
		req       RequestMatch
		wantMatch bool
	}{
		{
			name:      "no header constraint matches any request",
			route:     Route{Path: "/api", Type: RouteTypePrefix},
			req:       RequestMatch{Path: "/api", Headers: map[string]string{"x-tenant": "acme"}},
			wantMatch: true,
		},
		{
			name: "exact header match succeeds",
			route: Route{Path: "/api", Type: RouteTypePrefix, Headers: []RouteHeaderMatch{
				{Name: "X-Tenant", Value: "acme"},
			}},
			req:       RequestMatch{Path: "/api", Headers: map[string]string{"x-tenant": "acme"}},
			wantMatch: true,
		},
		{
			name: "exact header value mismatch",
			route: Route{Path: "/api", Type: RouteTypePrefix, Headers: []RouteHeaderMatch{
				{Name: "X-Tenant", Value: "acme"},
			}},
			req:       RequestMatch{Path: "/api", Headers: map[string]string{"x-tenant": "widgets"}},
			wantMatch: false,
		},
		{
			name: "missing required header does not match",
			route: Route{Path: "/api", Type: RouteTypePrefix, Headers: []RouteHeaderMatch{
				{Name: "X-Tenant", Value: "acme"},
			}},
			req:       RequestMatch{Path: "/api", Headers: map[string]string{}},
			wantMatch: false,
		},
		{
			name: "multiple headers AND'd — all match",
			route: Route{Path: "/api", Type: RouteTypePrefix, Headers: []RouteHeaderMatch{
				{Name: "X-Tenant", Value: "acme"},
				{Name: "X-Env", Value: "prod"},
			}},
			req:       RequestMatch{Path: "/api", Headers: map[string]string{"x-tenant": "acme", "x-env": "prod"}},
			wantMatch: true,
		},
		{
			name: "multiple headers AND'd — one missing",
			route: Route{Path: "/api", Type: RouteTypePrefix, Headers: []RouteHeaderMatch{
				{Name: "X-Tenant", Value: "acme"},
				{Name: "X-Env", Value: "prod"},
			}},
			req:       RequestMatch{Path: "/api", Headers: map[string]string{"x-tenant": "acme"}},
			wantMatch: false,
		},
		{
			name: "regex header match",
			route: Route{Path: "/api", Type: RouteTypePrefix, Headers: []RouteHeaderMatch{
				{Name: "User-Agent", Value: "^Mozilla/5\\.", Type: HeaderMatchRegex},
			}},
			req:       RequestMatch{Path: "/api", Headers: map[string]string{"user-agent": "Mozilla/5.0 (X11; Linux)"}},
			wantMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.route.Match(tt.req)
			if got != tt.wantMatch {
				t.Errorf("Match(%+v) on Route{Headers:%+v} = %v, want %v",
					tt.req, tt.route.Headers, got, tt.wantMatch)
			}
		})
	}
}

func TestRouteMatchMethod(t *testing.T) {
	tests := []struct {
		name      string
		route     Route
		req       RequestMatch
		wantMatch bool
	}{
		{
			name:      "empty route method matches any request method",
			route:     Route{Path: "/api", Type: RouteTypePrefix},
			req:       RequestMatch{Path: "/api", Method: "POST"},
			wantMatch: true,
		},
		{
			name:      "method matches case-insensitively",
			route:     Route{Path: "/api", Type: RouteTypePrefix, Method: "GET"},
			req:       RequestMatch{Path: "/api", Method: "get"},
			wantMatch: true,
		},
		{
			name:      "different method does not match",
			route:     Route{Path: "/api", Type: RouteTypePrefix, Method: "GET"},
			req:       RequestMatch{Path: "/api", Method: "POST"},
			wantMatch: false,
		},
		{
			name:      "method match but path does not",
			route:     Route{Path: "/api", Type: RouteTypeExact, Method: "GET"},
			req:       RequestMatch{Path: "/other", Method: "GET"},
			wantMatch: false,
		},
		{
			name:      "method restriction with empty request method fails",
			route:     Route{Path: "/api", Type: RouteTypePrefix, Method: "GET"},
			req:       RequestMatch{Path: "/api"},
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.route.Match(tt.req)
			if got != tt.wantMatch {
				t.Errorf("Match(%+v) on Route{Method:%q} = %v, want %v",
					tt.req, tt.route.Method, got, tt.wantMatch)
			}
		})
	}
}

// TestToJSON_StableBytesAcrossSpecialChars is a tripwire for the
// partitionHashes dedup in the controller: ToJSON must emit bytes that are
// identical to a plain json.Marshal call, in particular for routes whose
// Path / headers / query params contain '&', '<' or '>'. Switching the
// underlying encoder to SetEscapeHTML(false) (or any other formatting tweak)
// would silently invalidate every cached partition hash on the next reconcile
// after an upgrade and force a one-time mass rewrite of managed ConfigMaps.
func TestToJSON_StableBytesAcrossSpecialChars(t *testing.T) {
	rc := &RoutesConfig{Hosts: map[string][]Route{
		"x": {{Path: "/api?a=1&b=2&c=<x>", Type: RouteTypePrefix, Backend: "svc&backend"}},
	}}

	got, err := rc.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON returned error: %v", err)
	}

	want, err := json.Marshal(rc)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("ToJSON output drifted from json.Marshal:\n got: %s\nwant: %s", got, want)
	}

	// Belt-and-suspenders: lock in the actual escaping behaviour rather than
	// just byte-equality, so any future change to either side is caught.
	for _, esc := range []string{`\u0026`, `\u003c`, `\u003e`} {
		if !bytes.Contains(got, []byte(esc)) {
			t.Errorf("ToJSON output missing expected escape %q: %s", esc, got)
		}
	}

	// Two consecutive invocations must produce identical bytes; protects
	// against pooled-buffer leakage corrupting the second call.
	got2, err := rc.ToJSON()
	if err != nil {
		t.Fatalf("second ToJSON returned error: %v", err)
	}
	if !bytes.Equal(got, got2) {
		t.Fatalf("ToJSON not deterministic across calls:\nfirst:  %s\nsecond: %s", got, got2)
	}
}
