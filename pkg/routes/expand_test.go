package routes

import (
	"regexp"
	"testing"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

// matchesRegex is a test helper that checks if a path matches a regex pattern
func matchesRegex(pattern, path string) bool {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(path)
}

func TestExpandRegexWithLangPrefixes(t *testing.T) {
	langPrefixes := []string{"es", "fr", "it"}

	tests := []struct {
		name     string
		input    string
		policy   v1alpha1.PathPrefixPolicy
		expected string
	}{
		// Basic cases with ^/
		{
			name:     "simple path with anchor",
			input:    "^/other/[0-9]+/path$",
			policy:   v1alpha1.PathPrefixPolicyOptional,
			expected: "^(?:/(es|fr|it))?/other/[0-9]+/path$",
		},
		{
			name:     "simple path required",
			input:    "^/other/[0-9]+/path$",
			policy:   v1alpha1.PathPrefixPolicyRequired,
			expected: "^/(es|fr|it)/other/[0-9]+/path$",
		},
		{
			name:     "simple path disabled",
			input:    "^/other/[0-9]+/path$",
			policy:   v1alpha1.PathPrefixPolicyDisabled,
			expected: "^/other/[0-9]+/path$",
		},

		// Without start anchor
		{
			name:     "no start anchor",
			input:    "/users/[0-9]+$",
			policy:   v1alpha1.PathPrefixPolicyOptional,
			expected: "(?:/(es|fr|it))?/users/[0-9]+$",
		},

		// Without any anchor
		{
			name:     "no anchors",
			input:    "/api/v[0-9]+/",
			policy:   v1alpha1.PathPrefixPolicyOptional,
			expected: "(?:/(es|fr|it))?/api/v[0-9]+/",
		},

		// Complex regex patterns
		{
			name:     "regex with groups",
			input:    "^/products/(?P<id>[a-z0-9-]+)/reviews$",
			policy:   v1alpha1.PathPrefixPolicyOptional,
			expected: "^(?:/(es|fr|it))?/products/(?P<id>[a-z0-9-]+)/reviews$",
		},
		{
			name:     "regex with alternation",
			input:    "^/(users|accounts)/[0-9]+$",
			policy:   v1alpha1.PathPrefixPolicyOptional,
			expected: "^(?:/(es|fr|it))?/(users|accounts)/[0-9]+$",
		},
		{
			name:     "regex with character class at start",
			input:    "^/[a-z]+/[0-9]+$",
			policy:   v1alpha1.PathPrefixPolicyOptional,
			expected: "^(?:/(es|fr|it))?/[a-z]+/[0-9]+$",
		},

		// Edge cases
		{
			name:     "root path regex",
			input:    "^/$",
			policy:   v1alpha1.PathPrefixPolicyOptional,
			expected: "^(?:/(es|fr|it))?/$",
		},
		{
			name:     "regex without leading slash",
			input:    "^users/[0-9]+$",
			policy:   v1alpha1.PathPrefixPolicyOptional,
			expected: "^users/[0-9]+$", // Don't modify - no leading slash
		},

		// File extensions
		{
			name:     "file extension pattern",
			input:    `^/.*\.(jpg|jpeg|png|gif)$`,
			policy:   v1alpha1.PathPrefixPolicyOptional,
			expected: `^(?:/(es|fr|it))?/.*\.(jpg|jpeg|png|gif)$`,
		},

		// UUID pattern
		{
			name:     "uuid pattern",
			input:    "^/sessions/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$",
			policy:   v1alpha1.PathPrefixPolicyOptional,
			expected: "^(?:/(es|fr|it))?/sessions/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$",
		},

		// Quantifiers at start
		{
			name:     "optional segment at start",
			input:    "^/v[0-9]*/users$",
			policy:   v1alpha1.PathPrefixPolicyOptional,
			expected: "^(?:/(es|fr|it))?/v[0-9]*/users$",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := expandRegexWithPrefixes(tt.input, langPrefixes, tt.policy)
			if result != tt.expected {
				t.Errorf("\ninput:    %s\nexpected: %s\ngot:      %s", tt.input, tt.expected, result)
			}
		})
	}
}

func TestExpandRegexWithEmptyPrefixes(t *testing.T) {
	input := "^/other/[0-9]+/path$"
	result := expandRegexWithPrefixes(input, []string{}, v1alpha1.PathPrefixPolicyOptional)
	if result != input {
		t.Errorf("expected no change with empty prefixes, got: %s", result)
	}
}

func TestExpandedRegexCompiles(t *testing.T) {
	langPrefixes := []string{"es", "fr", "it", "de", "pt", "ja", "ko"}

	regexes := []string{
		"^/other/[0-9]+/path$",
		"^/products/(?P<id>[a-z0-9-]+)/reviews$",
		"^/(users|accounts)/[0-9]+$",
		"^/[a-z]+/[0-9]+$",
		"^/$",
		`^/.*\.(jpg|jpeg|png|gif)$`,
		"^/sessions/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$",
		"/api/v[0-9]+/",
	}

	for _, regex := range regexes {
		t.Run(regex, func(t *testing.T) {
			result := expandRegexWithPrefixes(regex, langPrefixes, v1alpha1.PathPrefixPolicyOptional)
			if !IsValidRegex(result) {
				t.Errorf("expanded regex does not compile:\ninput:  %s\noutput: %s", regex, result)
			}
		})
	}
}

func TestExpandedRegexMatches(t *testing.T) {
	langPrefixes := []string{"es", "fr", "it"}

	tests := []struct {
		name           string
		regex          string
		policy         v1alpha1.PathPrefixPolicy
		shouldMatch    []string
		shouldNotMatch []string
	}{
		{
			name:   "simple path optional",
			regex:  "^/users/[0-9]+$",
			policy: v1alpha1.PathPrefixPolicyOptional,
			shouldMatch: []string{
				"/users/123",
				"/es/users/123",
				"/fr/users/456",
				"/it/users/789",
			},
			shouldNotMatch: []string{
				"/de/users/123",    // de not in prefixes
				"/users/abc",       // not a number
				"/es/es/users/123", // double prefix
			},
		},
		{
			name:   "simple path required",
			regex:  "^/users/[0-9]+$",
			policy: v1alpha1.PathPrefixPolicyRequired,
			shouldMatch: []string{
				"/es/users/123",
				"/fr/users/456",
			},
			shouldNotMatch: []string{
				"/users/123",    // no prefix
				"/de/users/123", // de not in prefixes
			},
		},
		{
			name:   "alternation in path",
			regex:  "^/(users|accounts)/[0-9]+$",
			policy: v1alpha1.PathPrefixPolicyOptional,
			shouldMatch: []string{
				"/users/123",
				"/accounts/456",
				"/es/users/123",
				"/fr/accounts/456",
			},
			shouldNotMatch: []string{
				"/other/123",
				"/es/other/123",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expanded := expandRegexWithPrefixes(tt.regex, langPrefixes, tt.policy)

			for _, path := range tt.shouldMatch {
				if !matchesRegex(expanded, path) {
					t.Errorf("expected %q to match expanded regex %q (from %q)", path, expanded, tt.regex)
				}
			}

			for _, path := range tt.shouldNotMatch {
				if matchesRegex(expanded, path) {
					t.Errorf("expected %q NOT to match expanded regex %q (from %q)", path, expanded, tt.regex)
				}
			}
		})
	}
}

func TestConvertActions(t *testing.T) {
	port := int32(443)
	tests := []struct {
		name     string
		input    []v1alpha1.Action
		expected []RouteAction
	}{
		{
			name:     "nil actions",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty actions",
			input:    []v1alpha1.Action{},
			expected: nil,
		},
		{
			name: "redirect action",
			input: []v1alpha1.Action{
				{
					Type: v1alpha1.ActionTypeRedirect,
					Redirect: &v1alpha1.RedirectConfig{
						Scheme:     "https",
						Hostname:   "new.example.com",
						Path:       "/new-path",
						Port:       &port,
						StatusCode: 301,
					},
				},
			},
			expected: []RouteAction{
				{
					Type:               "redirect",
					RedirectScheme:     "https",
					RedirectHostname:   "new.example.com",
					RedirectPath:       "/new-path",
					RedirectPort:       443,
					RedirectStatusCode: 301,
				},
			},
		},
		{
			name: "redirect with default status code",
			input: []v1alpha1.Action{
				{
					Type: v1alpha1.ActionTypeRedirect,
					Redirect: &v1alpha1.RedirectConfig{
						Path: "/redirected",
					},
				},
			},
			expected: []RouteAction{
				{
					Type:               "redirect",
					RedirectPath:       "/redirected",
					RedirectStatusCode: 302,
				},
			},
		},
		{
			name: "rewrite action with path",
			input: []v1alpha1.Action{
				{
					Type:    v1alpha1.ActionTypeRewrite,
					Rewrite: &v1alpha1.RewriteConfig{Path: "/new/path"},
				},
			},
			expected: []RouteAction{
				{Type: "rewrite", RewritePath: "/new/path"},
			},
		},
		{
			name: "rewrite action with hostname",
			input: []v1alpha1.Action{
				{
					Type:    v1alpha1.ActionTypeRewrite,
					Rewrite: &v1alpha1.RewriteConfig{Hostname: "internal.svc.local"},
				},
			},
			expected: []RouteAction{
				{Type: "rewrite", RewriteHostname: "internal.svc.local"},
			},
		},
		{
			name: "rewrite action with path and hostname",
			input: []v1alpha1.Action{
				{
					Type: v1alpha1.ActionTypeRewrite,
					Rewrite: &v1alpha1.RewriteConfig{
						Path:     "/api/v2",
						Hostname: "api.internal.svc.local",
					},
				},
			},
			expected: []RouteAction{
				{
					Type:            "rewrite",
					RewritePath:     "/api/v2",
					RewriteHostname: "api.internal.svc.local",
				},
			},
		},
		{
			name: "header-set action",
			input: []v1alpha1.Action{
				{
					Type:   v1alpha1.ActionTypeHeaderSet,
					Header: &v1alpha1.HeaderConfig{Name: "X-Custom", Value: "value"},
				},
			},
			expected: []RouteAction{
				{Type: "header-set", HeaderName: "X-Custom", Value: "value"},
			},
		},
		{
			name: "header-add action",
			input: []v1alpha1.Action{
				{
					Type:   v1alpha1.ActionTypeHeaderAdd,
					Header: &v1alpha1.HeaderConfig{Name: "X-Request-ID", Value: "${request_id}"},
				},
			},
			expected: []RouteAction{
				{Type: "header-add", HeaderName: "X-Request-ID", Value: "${request_id}"},
			},
		},
		{
			name: "header-remove action",
			input: []v1alpha1.Action{
				{
					Type:       v1alpha1.ActionTypeHeaderRemove,
					HeaderName: "X-Internal",
				},
			},
			expected: []RouteAction{
				{Type: "header-remove", HeaderName: "X-Internal"},
			},
		},
		{
			name: "multiple actions",
			input: []v1alpha1.Action{
				{
					Type:    v1alpha1.ActionTypeRewrite,
					Rewrite: &v1alpha1.RewriteConfig{Path: "/cms/blog"},
				},
				{
					Type:   v1alpha1.ActionTypeHeaderSet,
					Header: &v1alpha1.HeaderConfig{Name: "X-Forwarded-Host", Value: "www.example.com"},
				},
				{
					Type:   v1alpha1.ActionTypeHeaderSet,
					Header: &v1alpha1.HeaderConfig{Name: "X-Real-IP", Value: "${client_ip}"},
				},
				{
					Type:       v1alpha1.ActionTypeHeaderRemove,
					HeaderName: "X-Internal-Only",
				},
			},
			expected: []RouteAction{
				{Type: "rewrite", RewritePath: "/cms/blog"},
				{Type: "header-set", HeaderName: "X-Forwarded-Host", Value: "www.example.com"},
				{Type: "header-set", HeaderName: "X-Real-IP", Value: "${client_ip}"},
				{Type: "header-remove", HeaderName: "X-Internal-Only"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertActions(tt.input)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d actions, got %d", len(tt.expected), len(result))
				return
			}

			for i, exp := range tt.expected {
				got := result[i]
				if got.Type != exp.Type {
					t.Errorf("action[%d].Type: expected %q, got %q", i, exp.Type, got.Type)
				}
				if got.RewritePath != exp.RewritePath {
					t.Errorf("action[%d].RewritePath: expected %q, got %q", i, exp.RewritePath, got.RewritePath)
				}
				if got.RewriteHostname != exp.RewriteHostname {
					t.Errorf("action[%d].RewriteHostname: expected %q, got %q", i, exp.RewriteHostname, got.RewriteHostname)
				}
				if got.RedirectScheme != exp.RedirectScheme {
					t.Errorf("action[%d].RedirectScheme: expected %q, got %q", i, exp.RedirectScheme, got.RedirectScheme)
				}
				if got.RedirectHostname != exp.RedirectHostname {
					t.Errorf("action[%d].RedirectHostname: expected %q, got %q", i, exp.RedirectHostname, got.RedirectHostname)
				}
				if got.RedirectPath != exp.RedirectPath {
					t.Errorf("action[%d].RedirectPath: expected %q, got %q", i, exp.RedirectPath, got.RedirectPath)
				}
				if got.RedirectPort != exp.RedirectPort {
					t.Errorf("action[%d].RedirectPort: expected %d, got %d", i, exp.RedirectPort, got.RedirectPort)
				}
				if got.RedirectStatusCode != exp.RedirectStatusCode {
					t.Errorf("action[%d].RedirectStatusCode: expected %d, got %d", i, exp.RedirectStatusCode, got.RedirectStatusCode)
				}
				if got.HeaderName != exp.HeaderName {
					t.Errorf("action[%d].HeaderName: expected %q, got %q", i, exp.HeaderName, got.HeaderName)
				}
				if got.Value != exp.Value {
					t.Errorf("action[%d].Value: expected %q, got %q", i, exp.Value, got.Value)
				}
			}
		})
	}
}

func TestExpandRoutesWithActions(t *testing.T) {
	cr := &v1alpha1.CustomHTTPRoute{
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Hostnames: []string{"example.com"},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: "/blog", Type: v1alpha1.MatchTypePathPrefix},
					},
					Actions: []v1alpha1.Action{
						{
							Type:    v1alpha1.ActionTypeRewrite,
							Rewrite: &v1alpha1.RewriteConfig{Path: "/cms/blog"},
						},
						{
							Type:   v1alpha1.ActionTypeHeaderSet,
							Header: &v1alpha1.HeaderConfig{Name: "X-Backend", Value: "cms"},
						},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "cms-service", Namespace: "default", Port: 8080},
					},
				},
			},
		},
	}

	result := ExpandRoutes(cr)

	routes, ok := result["example.com"]
	if !ok {
		t.Fatal("expected routes for example.com")
	}

	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	route := routes[0]
	if len(route.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(route.Actions))
	}

	if route.Actions[0].Type != "rewrite" || route.Actions[0].RewritePath != "/cms/blog" {
		t.Errorf("unexpected first action: %+v", route.Actions[0])
	}

	if route.Actions[1].Type != "header-set" || route.Actions[1].HeaderName != "X-Backend" {
		t.Errorf("unexpected second action: %+v", route.Actions[1])
	}
}

func TestExpandRoutesWithRedirect(t *testing.T) {
	cr := &v1alpha1.CustomHTTPRoute{
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Hostnames: []string{"example.com"},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: "/old-page", Type: v1alpha1.MatchTypeExact},
					},
					Actions: []v1alpha1.Action{
						{
							Type: v1alpha1.ActionTypeRedirect,
							Redirect: &v1alpha1.RedirectConfig{
								Path:       "/new-page",
								StatusCode: 301,
							},
						},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "dummy", Namespace: "default", Port: 80},
					},
				},
			},
		},
	}

	result := ExpandRoutes(cr)

	routes, ok := result["example.com"]
	if !ok {
		t.Fatal("expected routes for example.com")
	}

	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	route := routes[0]
	if len(route.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(route.Actions))
	}

	action := route.Actions[0]
	if action.Type != "redirect" {
		t.Errorf("expected redirect action, got %s", action.Type)
	}
	if action.RedirectPath != "/new-page" {
		t.Errorf("expected redirect path /new-page, got %s", action.RedirectPath)
	}
	if action.RedirectStatusCode != 301 {
		t.Errorf("expected status 301, got %d", action.RedirectStatusCode)
	}
}

func TestExpandExactWithPrefixesOptional(t *testing.T) {
	cr := &v1alpha1.CustomHTTPRoute{
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Hostnames: []string{"magnific.com"},
			PathPrefixes: &v1alpha1.PathPrefixes{
				Values: []string{"es", "fr", "de"},
				Policy: v1alpha1.PathPrefixPolicyOptional,
			},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: "/user/me", Type: v1alpha1.MatchTypeExact},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "user", Namespace: "user", Port: 80},
					},
				},
			},
		},
	}

	result := ExpandRoutes(cr)
	routes := result["magnific.com"]

	// 3 prefixed + 1 unprefixed = 4
	if len(routes) != 4 {
		t.Fatalf("expected 4 routes, got %d: %+v", len(routes), routes)
	}

	paths := make(map[string]bool)
	for _, r := range routes {
		paths[r.Path] = true
		if r.Type != RouteTypeExact {
			t.Errorf("expected exact type, got %s for path %s", r.Type, r.Path)
		}
	}

	for _, expected := range []string{"/user/me", "/es/user/me", "/fr/user/me", "/de/user/me"} {
		if !paths[expected] {
			t.Errorf("missing expected path %s", expected)
		}
	}
}

func TestExpandExactWithPrefixesRequired(t *testing.T) {
	cr := &v1alpha1.CustomHTTPRoute{
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Hostnames: []string{"magnific.com"},
			PathPrefixes: &v1alpha1.PathPrefixes{
				Values: []string{"es", "fr"},
				Policy: v1alpha1.PathPrefixPolicyRequired,
			},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: "/user/me", Type: v1alpha1.MatchTypeExact},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "user", Namespace: "user", Port: 80},
					},
				},
			},
		},
	}

	result := ExpandRoutes(cr)
	routes := result["magnific.com"]

	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d: %+v", len(routes), routes)
	}

	paths := make(map[string]bool)
	for _, r := range routes {
		paths[r.Path] = true
	}

	if paths["/user/me"] {
		t.Error("unprefixed /user/me should NOT be present with Required policy")
	}
	for _, expected := range []string{"/es/user/me", "/fr/user/me"} {
		if !paths[expected] {
			t.Errorf("missing expected path %s", expected)
		}
	}
}

func TestExpandExactWithPrefixesDisabled(t *testing.T) {
	cr := &v1alpha1.CustomHTTPRoute{
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Hostnames: []string{"magnific.com"},
			PathPrefixes: &v1alpha1.PathPrefixes{
				Values: []string{"es", "fr"},
				Policy: v1alpha1.PathPrefixPolicyDisabled,
			},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: "/user/me", Type: v1alpha1.MatchTypeExact},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "user", Namespace: "user", Port: 80},
					},
				},
			},
		},
	}

	result := ExpandRoutes(cr)
	routes := result["magnific.com"]

	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Path != "/user/me" {
		t.Errorf("expected /user/me, got %s", routes[0].Path)
	}
}

func TestExpandMatchTypesPathPrefixOnly(t *testing.T) {
	cr := &v1alpha1.CustomHTTPRoute{
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Hostnames: []string{"example.com"},
			PathPrefixes: &v1alpha1.PathPrefixes{
				Values:           []string{"es", "fr"},
				Policy:           v1alpha1.PathPrefixPolicyOptional,
				ExpandMatchTypes: []v1alpha1.MatchType{v1alpha1.MatchTypePathPrefix},
			},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: "/user/me", Type: v1alpha1.MatchTypeExact},
						{Path: "/app", Type: v1alpha1.MatchTypePathPrefix},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "svc", Namespace: "default", Port: 80},
					},
				},
			},
		},
	}

	result := ExpandRoutes(cr)
	routes := result["example.com"]

	// Exact: 1 (not expanded), PathPrefix: 3 (es + fr + unprefixed) = 4
	if len(routes) != 4 {
		t.Fatalf("expected 4 routes, got %d: %+v", len(routes), routes)
	}

	exactCount := 0
	prefixCount := 0
	for _, r := range routes {
		if r.Type == RouteTypeExact {
			exactCount++
			if r.Path != "/user/me" {
				t.Errorf("exact route should be /user/me, got %s", r.Path)
			}
		}
		if r.Type == RouteTypePrefix {
			prefixCount++
		}
	}
	if exactCount != 1 {
		t.Errorf("expected 1 exact route, got %d", exactCount)
	}
	if prefixCount != 3 {
		t.Errorf("expected 3 prefix routes, got %d", prefixCount)
	}
}

func TestExpandMatchTypesExactAndPathPrefix(t *testing.T) {
	cr := &v1alpha1.CustomHTTPRoute{
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Hostnames: []string{"example.com"},
			PathPrefixes: &v1alpha1.PathPrefixes{
				Values:           []string{"es"},
				Policy:           v1alpha1.PathPrefixPolicyOptional,
				ExpandMatchTypes: []v1alpha1.MatchType{v1alpha1.MatchTypeExact, v1alpha1.MatchTypePathPrefix},
			},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: "/user/me", Type: v1alpha1.MatchTypeExact},
						{Path: "/app", Type: v1alpha1.MatchTypePathPrefix},
						{Path: "^/api/[0-9]+$", Type: v1alpha1.MatchTypeRegex},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "svc", Namespace: "default", Port: 80},
					},
				},
			},
		},
	}

	result := ExpandRoutes(cr)
	routes := result["example.com"]

	// Exact: 2 (es + unprefixed), PathPrefix: 2 (es + unprefixed), Regex: 1 (not expanded) = 5
	if len(routes) != 5 {
		t.Fatalf("expected 5 routes, got %d: %+v", len(routes), routes)
	}

	var exactPaths, prefixPaths, regexPaths []string
	for _, r := range routes {
		switch r.Type {
		case RouteTypeExact:
			exactPaths = append(exactPaths, r.Path)
		case RouteTypePrefix:
			prefixPaths = append(prefixPaths, r.Path)
		case RouteTypeRegex:
			regexPaths = append(regexPaths, r.Path)
		}
	}

	if len(exactPaths) != 2 {
		t.Errorf("expected 2 exact routes, got %d: %v", len(exactPaths), exactPaths)
	}
	if len(prefixPaths) != 2 {
		t.Errorf("expected 2 prefix routes, got %d: %v", len(prefixPaths), prefixPaths)
	}
	if len(regexPaths) != 1 {
		t.Errorf("expected 1 regex route (not expanded), got %d: %v", len(regexPaths), regexPaths)
	}
	if len(regexPaths) == 1 && regexPaths[0] != "^/api/[0-9]+$" {
		t.Errorf("regex should not be modified, got %s", regexPaths[0])
	}
}

func TestExpandMatchTypesRuleLevelOverride(t *testing.T) {
	cr := &v1alpha1.CustomHTTPRoute{
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Hostnames: []string{"example.com"},
			PathPrefixes: &v1alpha1.PathPrefixes{
				Values: []string{"es", "fr"},
				Policy: v1alpha1.PathPrefixPolicyOptional,
			},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: "/user/me", Type: v1alpha1.MatchTypeExact},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "svc", Namespace: "default", Port: 80},
					},
					PathPrefixes: &v1alpha1.RulePathPrefixes{
						Policy:           v1alpha1.PathPrefixPolicyOptional,
						ExpandMatchTypes: []v1alpha1.MatchType{v1alpha1.MatchTypePathPrefix},
					},
				},
			},
		},
	}

	result := ExpandRoutes(cr)
	routes := result["example.com"]

	// Rule overrides to PathPrefixOnly, so Exact should NOT expand = 1 route
	if len(routes) != 1 {
		t.Fatalf("expected 1 route (rule override to PathPrefixOnly), got %d: %+v", len(routes), routes)
	}
	if routes[0].Path != "/user/me" {
		t.Errorf("expected /user/me, got %s", routes[0].Path)
	}
}

func TestExpandMatchTypesDefaultExpandsAll(t *testing.T) {
	cr := &v1alpha1.CustomHTTPRoute{
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Hostnames: []string{"example.com"},
			PathPrefixes: &v1alpha1.PathPrefixes{
				Values: []string{"es"},
				Policy: v1alpha1.PathPrefixPolicyOptional,
			},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: "/exact", Type: v1alpha1.MatchTypeExact},
						{Path: "/prefix", Type: v1alpha1.MatchTypePathPrefix},
						{Path: "^/regex$", Type: v1alpha1.MatchTypeRegex},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "svc", Namespace: "default", Port: 80},
					},
				},
			},
		},
	}

	result := ExpandRoutes(cr)
	routes := result["example.com"]

	// Default (no expandMatchTypes): all types expanded
	// Exact: /es/exact + /exact = 2
	// PathPrefix: /es/prefix + /prefix = 2
	// Regex: 1 (modified regex with optional prefix)
	if len(routes) != 5 {
		t.Fatalf("expected 5 routes (all types expanded by default), got %d: %+v", len(routes), routes)
	}
}
