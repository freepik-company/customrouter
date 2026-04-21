package extproc

import (
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/freepik-company/customrouter/pkg/routes"
	"go.uber.org/zap"
)

func boolPtr(v bool) *bool { return &v }

func TestShouldReplacePrefixMatch(t *testing.T) {
	tests := []struct {
		name          string
		action        routes.RouteAction
		routeType     string
		rewrittenBase string
		want          bool
	}{
		{
			name:      "prefix route, no variables -> prefix rewrite",
			action:    routes.RouteAction{RewritePath: "/api/v1"},
			routeType: routes.RouteTypePrefix,
			want:      true,
		},
		{
			name:      "prefix route, with variables -> full rewrite",
			action:    routes.RouteAction{RewritePath: "/api/${path.segment.1}"},
			routeType: routes.RouteTypePrefix,
			want:      false,
		},
		{
			name:      "exact route, no variables -> full rewrite",
			action:    routes.RouteAction{RewritePath: "/api/v1"},
			routeType: routes.RouteTypeExact,
			want:      false,
		},
		{
			name:      "regex route, no variables -> full rewrite",
			action:    routes.RouteAction{RewritePath: "/api/v1"},
			routeType: routes.RouteTypeRegex,
			want:      false,
		},
		{
			name:      "explicit true overrides convention on exact route",
			action:    routes.RouteAction{RewritePath: "/api/v1", RewriteReplacePrefixMatch: boolPtr(true)},
			routeType: routes.RouteTypeExact,
			want:      true,
		},
		{
			name:      "explicit false overrides convention on prefix route",
			action:    routes.RouteAction{RewritePath: "/api/v1", RewriteReplacePrefixMatch: boolPtr(false)},
			routeType: routes.RouteTypePrefix,
			want:      false,
		},
		{
			name:      "explicit true on prefix route with variables",
			action:    routes.RouteAction{RewritePath: "/api/${host}", RewriteReplacePrefixMatch: boolPtr(true)},
			routeType: routes.RouteTypePrefix,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := &routes.Route{Type: tt.routeType}
			got := shouldReplacePrefixMatch(tt.action, route, tt.rewrittenBase)
			if got != tt.want {
				t.Errorf("shouldReplacePrefixMatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJoinRedirectPath(t *testing.T) {
	tests := []struct {
		basePath string
		suffix   string
		want     string
	}{
		{"/app", "", "/app"},
		{"/app", "/foo", "/app/foo"},
		{"/app/", "/foo", "/app/foo"},
		{"/app/", "foo", "/app/foo"},
		{"/app", "foo", "/app/foo"},
		{"/app", "?q=1", "/app?q=1"},
		{"/app/", "?q=1", "/app/?q=1"},
		{"/app", "#frag", "/app#frag"},
		{"/app", "/foo?q=1", "/app/foo?q=1"},
		{"/app", "/foo#frag", "/app/foo#frag"},
	}

	for _, tt := range tests {
		t.Run(tt.basePath+"+"+tt.suffix, func(t *testing.T) {
			got := joinRedirectPath(tt.basePath, tt.suffix)
			if got != tt.want {
				t.Errorf("joinRedirectPath(%q, %q) = %q, want %q", tt.basePath, tt.suffix, got, tt.want)
			}
		})
	}
}

func TestShouldReplacePrefixMatchForRedirect(t *testing.T) {
	tests := []struct {
		name      string
		action    routes.RouteAction
		routeType string
		want      bool
	}{
		{
			name:      "nil flag on prefix route -> off (backwards compatible)",
			action:    routes.RouteAction{RedirectPath: "/app"},
			routeType: routes.RouteTypePrefix,
			want:      false,
		},
		{
			name:      "explicit false on prefix route -> off",
			action:    routes.RouteAction{RedirectPath: "/app", RedirectReplacePrefixMatch: boolPtr(false)},
			routeType: routes.RouteTypePrefix,
			want:      false,
		},
		{
			name:      "explicit true on prefix route -> on",
			action:    routes.RouteAction{RedirectPath: "/app", RedirectReplacePrefixMatch: boolPtr(true)},
			routeType: routes.RouteTypePrefix,
			want:      true,
		},
		{
			name:      "explicit true on exact route -> off (only prefix supported)",
			action:    routes.RouteAction{RedirectPath: "/app", RedirectReplacePrefixMatch: boolPtr(true)},
			routeType: routes.RouteTypeExact,
			want:      false,
		},
		{
			name:      "explicit true on regex route -> off",
			action:    routes.RouteAction{RedirectPath: "/app", RedirectReplacePrefixMatch: boolPtr(true)},
			routeType: routes.RouteTypeRegex,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := &routes.Route{Type: tt.routeType}
			got := shouldReplacePrefixMatchForRedirect(tt.action, route)
			if got != tt.want {
				t.Errorf("shouldReplacePrefixMatchForRedirect() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSplitPath(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{"/foo/bar", []string{"foo", "bar"}},
		{"/foo/bar?q=1", []string{"foo", "bar"}},
		{"/", nil},
		{"/a/b/c/d", []string{"a", "b", "c", "d"}},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := splitPath(tt.path)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("splitPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitPath(%q)[%d] = %q, want %q", tt.path, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestStripQueryString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/example", "/example"},
		{"/example?key=value", "/example"},
		{"/example?", "/example"},
		{"/example?key=value&other=test", "/example"},
		{"/path/to/resource?q=search+term", "/path/to/resource"},
		{"/", "/"},
		{"/?q=1", "/"},
		{"", ""},
		// RFC 3986 §3.3: path is also terminated by '#'
		{"/example#section", "/example"},
		{"/example#", "/example"},
		{"/path/to/resource#top", "/path/to/resource"},
		// '?' before '#': query terminates the path first
		{"/example?q=1#frag", "/example"},
		// '#' before '?': fragment terminates the path first
		{"/example#frag?notquery", "/example"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripQueryString(tt.input)
			if got != tt.want {
				t.Errorf("stripQueryString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractQueryParams(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{"no query", "/api", map[string]string{}},
		{"empty query", "/api?", map[string]string{}},
		{"single param", "/api?version=2", map[string]string{"version": "2"}},
		{"multiple params", "/api?a=1&b=two", map[string]string{"a": "1", "b": "two"}},
		{"url-encoded value", "/api?q=hello%20world", map[string]string{"q": "hello world"}},
		{"repeated param keeps first", "/api?x=1&x=2", map[string]string{"x": "1"}},
		{"fragment stripped before parsing", "/api?q=1#frag", map[string]string{"q": "1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractQueryParams(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("extractQueryParams(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("extractQueryParams(%q)[%q] = %q, want %q", tt.input, k, got[k], v)
				}
			}
		})
	}
}

func TestBuildForwardResponse_OriginalPathHeader(t *testing.T) {
	logger := zap.NewNop()
	p := NewProcessor(nil, logger, false)

	tests := []struct {
		name             string
		route            *routes.Route
		varsPath         string
		wantOriginalPath string // empty means header should NOT be present
	}{
		{
			name: "rewrite sets x-envoy-original-path",
			route: &routes.Route{
				Path:    "/old",
				Type:    routes.RouteTypePrefix,
				Backend: "backend.ns.svc.cluster.local:80",
				Actions: []routes.RouteAction{
					{Type: routes.ActionTypeRewrite, RewritePath: "/new"},
				},
			},
			varsPath:         "/old/sub?q=1",
			wantOriginalPath: "/old/sub?q=1",
		},
		{
			name: "no rewrite omits x-envoy-original-path",
			route: &routes.Route{
				Path:    "/keep",
				Type:    routes.RouteTypePrefix,
				Backend: "backend.ns.svc.cluster.local:80",
			},
			varsPath:         "/keep/foo",
			wantOriginalPath: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := &requestVars{
				path:         tt.varsPath,
				host:         "example.com",
				pathSegments: splitPath(tt.varsPath),
			}
			reqCtx := &requestContext{authority: "example.com"}

			resp, _, err := p.buildForwardResponse(tt.route, vars, reqCtx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			headers := resp.GetRequestHeaders().GetResponse().GetHeaderMutation().GetSetHeaders()
			var got string
			for _, h := range headers {
				if h.GetHeader().GetKey() == "x-envoy-original-path" {
					got = string(h.GetHeader().GetRawValue())
					break
				}
			}

			if tt.wantOriginalPath == "" && got != "" {
				t.Errorf("expected no x-envoy-original-path header, got %q", got)
			}
			if tt.wantOriginalPath != "" && got != tt.wantOriginalPath {
				t.Errorf("x-envoy-original-path = %q, want %q", got, tt.wantOriginalPath)
			}
		})
	}
}

func TestProcessResponseHeaders(t *testing.T) {
	logger := zap.NewNop()
	p := NewProcessor(nil, logger, false)

	t.Run("no matched route → empty mutation", func(t *testing.T) {
		resp := p.processResponseHeaders(&streamContext{})
		if resp.GetResponseHeaders().GetResponse() != nil {
			t.Errorf("expected no CommonResponse when no route matched")
		}
	})

	t.Run("route without response actions → empty mutation", func(t *testing.T) {
		resp := p.processResponseHeaders(&streamContext{
			matchedRoute: &routes.Route{
				Actions: []routes.RouteAction{
					{Type: routes.ActionTypeHeaderSet, HeaderName: "X-Request-Only", Value: "yes"},
				},
			},
		})
		if resp.GetResponseHeaders().GetResponse() != nil {
			t.Errorf("request-side actions should not produce response mutations")
		}
	})

	t.Run("response-header-set produces OVERWRITE mutation", func(t *testing.T) {
		resp := p.processResponseHeaders(&streamContext{
			matchedRoute: &routes.Route{
				Actions: []routes.RouteAction{
					{Type: routes.ActionTypeResponseHeaderSet, HeaderName: "X-Served-By", Value: "customrouter"},
				},
			},
		})
		mutation := resp.GetResponseHeaders().GetResponse().GetHeaderMutation()
		if mutation == nil || len(mutation.SetHeaders) != 1 {
			t.Fatalf("expected one set header, got %+v", mutation)
		}
		h := mutation.SetHeaders[0]
		if h.GetHeader().GetKey() != "X-Served-By" {
			t.Errorf("unexpected header key %q", h.GetHeader().GetKey())
		}
		if string(h.GetHeader().GetRawValue()) != "customrouter" {
			t.Errorf("unexpected header value %q", string(h.GetHeader().GetRawValue()))
		}
		if h.GetAppendAction() != corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD {
			t.Errorf("expected OVERWRITE_IF_EXISTS_OR_ADD, got %v", h.GetAppendAction())
		}
	})

	t.Run("response-header-add produces APPEND mutation", func(t *testing.T) {
		resp := p.processResponseHeaders(&streamContext{
			matchedRoute: &routes.Route{
				Actions: []routes.RouteAction{
					{Type: routes.ActionTypeResponseHeaderAdd, HeaderName: "Set-Cookie", Value: "a=1"},
				},
			},
		})
		h := resp.GetResponseHeaders().GetResponse().GetHeaderMutation().SetHeaders[0]
		if h.GetAppendAction() != corev3.HeaderValueOption_APPEND_IF_EXISTS_OR_ADD {
			t.Errorf("expected APPEND_IF_EXISTS_OR_ADD, got %v", h.GetAppendAction())
		}
	})

	t.Run("response-header-remove produces RemoveHeaders", func(t *testing.T) {
		resp := p.processResponseHeaders(&streamContext{
			matchedRoute: &routes.Route{
				Actions: []routes.RouteAction{
					{Type: routes.ActionTypeResponseHeaderRemove, HeaderName: "X-Internal"},
				},
			},
		})
		remove := resp.GetResponseHeaders().GetResponse().GetHeaderMutation().RemoveHeaders
		if len(remove) != 1 || remove[0] != "X-Internal" {
			t.Errorf("expected RemoveHeaders=[X-Internal], got %v", remove)
		}
	})

	t.Run("request-side header-set does not leak into response mutations", func(t *testing.T) {
		resp := p.processResponseHeaders(&streamContext{
			matchedRoute: &routes.Route{
				Actions: []routes.RouteAction{
					{Type: routes.ActionTypeHeaderSet, HeaderName: "X-Request-Side", Value: "req"},
					{Type: routes.ActionTypeResponseHeaderSet, HeaderName: "X-Response-Side", Value: "resp"},
				},
			},
		})
		set := resp.GetResponseHeaders().GetResponse().GetHeaderMutation().SetHeaders
		if len(set) != 1 || set[0].GetHeader().GetKey() != "X-Response-Side" {
			t.Fatalf("expected only X-Response-Side in response mutation, got %+v", set)
		}
	})
}

func TestSubstituteVariables(t *testing.T) {
	vars := &requestVars{
		clientIP:     "1.2.3.4",
		requestID:    "req-123",
		host:         "example.com",
		path:         "/foo/bar?q=1",
		method:       "GET",
		scheme:       "https",
		pathSegments: []string{"foo", "bar"},
	}

	tests := []struct {
		input string
		want  string
	}{
		{"/api/${path.segment.0}", "/api/foo"},
		{"${scheme}://${host}${path}", "https://example.com/foo/bar?q=1"},
		{"/static", "/static"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := substituteVariables(tt.input, vars)
			if got != tt.want {
				t.Errorf("substituteVariables(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
