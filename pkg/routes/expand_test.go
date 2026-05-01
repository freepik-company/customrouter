package routes

import (
	"regexp"
	"strings"
	"testing"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

const testPathUserMe = "/user/me"
const testPathCmsBlog = "/cms/blog"

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
			result := ExpandRegexWithPrefixes(tt.input, langPrefixes, tt.policy)
			if result != tt.expected {
				t.Errorf("\ninput:    %s\nexpected: %s\ngot:      %s", tt.input, tt.expected, result)
			}
		})
	}
}

func TestExpandRegexWithEmptyPrefixes(t *testing.T) {
	input := "^/other/[0-9]+/path$"
	result := ExpandRegexWithPrefixes(input, []string{}, v1alpha1.PathPrefixPolicyOptional)
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
			result := ExpandRegexWithPrefixes(regex, langPrefixes, v1alpha1.PathPrefixPolicyOptional)
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
			expanded := ExpandRegexWithPrefixes(tt.regex, langPrefixes, tt.policy)

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
					Rewrite: &v1alpha1.RewriteConfig{Path: testPathCmsBlog},
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
				{Type: "rewrite", RewritePath: testPathCmsBlog},
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
							Rewrite: &v1alpha1.RewriteConfig{Path: testPathCmsBlog},
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

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

	if route.Actions[0].Type != "rewrite" || route.Actions[0].RewritePath != testPathCmsBlog {
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

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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
			Hostnames: []string{"example.com"},
			PathPrefixes: &v1alpha1.PathPrefixes{
				Values: []string{"es", "fr", "de"},
				Policy: v1alpha1.PathPrefixPolicyOptional,
			},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: testPathUserMe, Type: v1alpha1.MatchTypeExact},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "user", Namespace: "user", Port: 80},
					},
				},
			},
		},
	}

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

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

	for _, expected := range []string{testPathUserMe, "/es/user/me", "/fr/user/me", "/de/user/me"} {
		if !paths[expected] {
			t.Errorf("missing expected path %s", expected)
		}
	}
}

func TestExpandExactRootPathWithPrefixes(t *testing.T) {
	// Regression: path "/" Exact with prefix should produce "/v1", not "/v1/"
	cr := &v1alpha1.CustomHTTPRoute{
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Hostnames: []string{"example.com"},
			PathPrefixes: &v1alpha1.PathPrefixes{
				Values: []string{"v1", "v2"},
				Policy: v1alpha1.PathPrefixPolicyRequired,
			},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: "/", Type: v1alpha1.MatchTypeExact},
					},
				},
			},
		},
	}

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d: %+v", len(routes), routes)
	}

	paths := make(map[string]bool)
	for _, r := range routes {
		paths[r.Path] = true
		if r.Type != RouteTypeExact {
			t.Errorf("expected exact type, got %s for path %s", r.Type, r.Path)
		}
	}

	for _, expected := range []string{"/v1", "/v2"} {
		if !paths[expected] {
			t.Errorf("missing expected path %s; got paths: %v", expected, paths)
		}
	}
	if paths["/v1/"] {
		t.Error("/v1/ should not be generated (double slash from root path)")
	}
}

func TestExpandExactWithPrefixesRequired(t *testing.T) {
	cr := &v1alpha1.CustomHTTPRoute{
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Hostnames: []string{"example.com"},
			PathPrefixes: &v1alpha1.PathPrefixes{
				Values: []string{"es", "fr"},
				Policy: v1alpha1.PathPrefixPolicyRequired,
			},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: testPathUserMe, Type: v1alpha1.MatchTypeExact},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "user", Namespace: "user", Port: 80},
					},
				},
			},
		},
	}

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d: %+v", len(routes), routes)
	}

	paths := make(map[string]bool)
	for _, r := range routes {
		paths[r.Path] = true
	}

	if paths[testPathUserMe] {
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
			Hostnames: []string{"example.com"},
			PathPrefixes: &v1alpha1.PathPrefixes{
				Values: []string{"es", "fr"},
				Policy: v1alpha1.PathPrefixPolicyDisabled,
			},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: testPathUserMe, Type: v1alpha1.MatchTypeExact},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "user", Namespace: "user", Port: 80},
					},
				},
			},
		},
	}

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Path != testPathUserMe {
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
						{Path: testPathUserMe, Type: v1alpha1.MatchTypeExact},
						{Path: "/app", Type: v1alpha1.MatchTypePathPrefix},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "svc", Namespace: "default", Port: 80},
					},
				},
			},
		},
	}

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
			if r.Path != testPathUserMe {
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
						{Path: testPathUserMe, Type: v1alpha1.MatchTypeExact},
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

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
						{Path: testPathUserMe, Type: v1alpha1.MatchTypeExact},
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

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

	// Rule overrides to PathPrefixOnly, so Exact should NOT expand = 1 route
	if len(routes) != 1 {
		t.Fatalf("expected 1 route (rule override to PathPrefixOnly), got %d: %+v", len(routes), routes)
	}
	if routes[0].Path != testPathUserMe {
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

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

	// Default (no expandMatchTypes): all types expanded
	// Exact: /es/exact + /exact = 2
	// PathPrefix: /es/prefix + /prefix = 2
	// Regex: 1 (modified regex with optional prefix)
	if len(routes) != 5 {
		t.Fatalf("expected 5 routes (all types expanded by default), got %d: %+v", len(routes), routes)
	}
}

func TestExpandRegexWithInlinePrefix(t *testing.T) {
	langPrefixes := []string{"es", "fr", "it"}

	tests := []struct {
		name     string
		input    string
		policy   v1alpha1.PathPrefixPolicy
		prefixes []string
		expected string
	}{
		{
			name:     "inline prefix required",
			input:    "^/_app/data/[^/]+/{prefix}/",
			policy:   v1alpha1.PathPrefixPolicyRequired,
			prefixes: langPrefixes,
			expected: "^/_app/data/[^/]+/(es|fr|it)/",
		},
		{
			name:     "inline prefix optional",
			input:    "^/_app/data/[^/]+/{prefix}/",
			policy:   v1alpha1.PathPrefixPolicyOptional,
			prefixes: langPrefixes,
			expected: "^/_app/data/[^/]+/(es|fr|it)?/",
		},
		{
			name:     "static assets with inline prefix required",
			input:    "^/static/locales/{prefix}/",
			policy:   v1alpha1.PathPrefixPolicyRequired,
			prefixes: langPrefixes,
			expected: "^/static/locales/(es|fr|it)/",
		},
		{
			name:     "inline prefix disabled returns original",
			input:    "^/_app/data/[^/]+/{prefix}/",
			policy:   v1alpha1.PathPrefixPolicyDisabled,
			prefixes: langPrefixes,
			expected: "^/_app/data/[^/]+/{prefix}/",
		},
		{
			name:     "inline prefix with empty prefixes returns original",
			input:    "^/_app/data/[^/]+/{prefix}/",
			policy:   v1alpha1.PathPrefixPolicyRequired,
			prefixes: []string{},
			expected: "^/_app/data/[^/]+/{prefix}/",
		},
		{
			name:     "multiple inline prefix placeholders",
			input:    "^/{prefix}/data/[^/]+/{prefix}/",
			policy:   v1alpha1.PathPrefixPolicyRequired,
			prefixes: langPrefixes,
			expected: "^/(es|fr|it)/data/[^/]+/(es|fr|it)/",
		},
		{
			name:     "inline prefix without anchors",
			input:    "/_app/data/[^/]+/{prefix}/",
			policy:   v1alpha1.PathPrefixPolicyRequired,
			prefixes: langPrefixes,
			expected: "/_app/data/[^/]+/(es|fr|it)/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExpandRegexWithPrefixes(tt.input, tt.prefixes, tt.policy)
			if result != tt.expected {
				t.Errorf("\ninput:    %s\nexpected: %s\ngot:      %s", tt.input, tt.expected, result)
			}
		})
	}
}

func TestExpandedInlinePrefixRegexCompiles(t *testing.T) {
	langPrefixes := []string{"es", "fr", "it", "de", "pt", "ja", "ko", "zh-CN", "af-ZA"}

	regexes := []string{
		"^/_app/data/[^/]+/{prefix}/",
		"^/static/locales/{prefix}/",
		"^/_app/data/[^/]+/{prefix}/[^/]+\\.json$",
		"/_app/data/[^/]+/{prefix}/",
	}

	for _, regex := range regexes {
		for _, policy := range []v1alpha1.PathPrefixPolicy{
			v1alpha1.PathPrefixPolicyRequired,
			v1alpha1.PathPrefixPolicyOptional,
		} {
			t.Run(regex+"_"+string(policy), func(t *testing.T) {
				result := ExpandRegexWithPrefixes(regex, langPrefixes, policy)
				if !IsValidRegex(result) {
					t.Errorf("expanded regex does not compile:\ninput:  %s\noutput: %s", regex, result)
				}
			})
		}
	}
}

func TestExpandedInlinePrefixRegexMatches(t *testing.T) {
	tests := []struct {
		name           string
		regex          string
		prefixes       []string
		policy         v1alpha1.PathPrefixPolicy
		shouldMatch    []string
		shouldNotMatch []string
	}{
		{
			name:     "app data required - language in path segment",
			regex:    "^/_app/data/[^/]+/{prefix}/",
			prefixes: []string{"es", "fr", "it"},
			policy:   v1alpha1.PathPrefixPolicyRequired,
			shouldMatch: []string{
				"/_app/data/RFc13G_JcDvFt-Ny5bOt-/es/products.json",
				"/_app/data/lXR99BKHpJAoi5tBMoMFo/fr/search.json",
				"/_app/data/nyhijxIqNASNgJgmnMRz7/it/user.json",
			},
			shouldNotMatch: []string{
				"/_app/data/abc123/en/user.json",
				"/_app/data/abc123/de/search.json",
				"/_app/static/chunks/main-abc123.js",
				"/es/_app/data/abc123/es/products.json",
			},
		},
		{
			name:     "app data required - asian locales",
			regex:    "^/_app/data/[^/]+/{prefix}/",
			prefixes: []string{"cn", "kr", "tw", "zh"},
			policy:   v1alpha1.PathPrefixPolicyRequired,
			shouldMatch: []string{
				"/_app/data/wA5C_aviBGrejK0XWJwbo/kr/%EC%8A%A4%ED%86%A1.json",
				"/_app/data/abc123/zh/search.json",
				"/_app/data/abc123/cn/search.json",
			},
			shouldNotMatch: []string{
				"/_app/data/abc123/en/user.json",
				"/_app/data/abc123/es/products.json",
			},
		},
		{
			name:     "static assets required - full locale codes",
			regex:    "^/static/locales/{prefix}/",
			prefixes: []string{"af-ZA", "zh-CN", "es-ES", "fr-FR"},
			policy:   v1alpha1.PathPrefixPolicyRequired,
			shouldMatch: []string{
				"/static/locales/af-ZA/common.json",
				"/static/locales/zh-CN/common.json",
				"/static/locales/es-ES/common.json",
			},
			shouldNotMatch: []string{
				"/static/locales/en-US/common.json",
				"/static/locales/de-DE/common.json",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expanded := ExpandRegexWithPrefixes(tt.regex, tt.prefixes, tt.policy)

			for _, path := range tt.shouldMatch {
				if !matchesRegex(expanded, path) {
					t.Errorf("expected %q to match expanded regex %q (from %q)",
						path, expanded, tt.regex)
				}
			}

			for _, path := range tt.shouldNotMatch {
				if matchesRegex(expanded, path) {
					t.Errorf("expected %q NOT to match expanded regex %q (from %q)",
						path, expanded, tt.regex)
				}
			}
		})
	}
}

func TestExpandRoutesWithInlinePrefixPlaceholder(t *testing.T) {
	cr := &v1alpha1.CustomHTTPRoute{
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Hostnames: []string{"app.example.com"},
			PathPrefixes: &v1alpha1.PathPrefixes{
				Values: []string{"es", "fr", "de"},
				Policy: v1alpha1.PathPrefixPolicyRequired,
			},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{
							Path: "^/_app/data/[^/]+/{prefix}/",
							Type: v1alpha1.MatchTypeRegex,
						},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "web-europe", Namespace: "web", Port: 80},
					},
				},
				{
					Matches: []v1alpha1.PathMatch{
						{
							Path: "^/users/[0-9]+$",
							Type: v1alpha1.MatchTypeRegex,
						},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "web-europe", Namespace: "web", Port: 80},
					},
				},
			},
		},
	}

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["app.example.com"]

	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d: %+v", len(routes), routes)
	}

	var inlineIdx, normalIdx int
	var foundInline, foundNormal bool
	for i := range routes {
		if strings.Contains(routes[i].Path, "_app") {
			inlineIdx = i
			foundInline = true
		} else {
			normalIdx = i
			foundNormal = true
		}
	}

	if !foundInline {
		t.Fatal("inline prefix route not found")
	}
	if !foundNormal {
		t.Fatal("normal route not found")
	}

	expectedInline := "^/_app/data/[^/]+/(es|fr|de)/"
	if routes[inlineIdx].Path != expectedInline {
		t.Errorf("inline route:\nexpected: %s\ngot:      %s", expectedInline, routes[inlineIdx].Path)
	}

	expectedNormal := "^/(es|fr|de)/users/[0-9]+$"
	if routes[normalIdx].Path != expectedNormal {
		t.Errorf("normal route:\nexpected: %s\ngot:      %s", expectedNormal, routes[normalIdx].Path)
	}
}

func TestExpandRegexWithoutPlaceholderUnchanged(t *testing.T) {
	langPrefixes := []string{"es", "fr", "it"}

	tests := []struct {
		input    string
		policy   v1alpha1.PathPrefixPolicy
		expected string
	}{
		{
			input:    "^/other/[0-9]+/path$",
			policy:   v1alpha1.PathPrefixPolicyOptional,
			expected: "^(?:/(es|fr|it))?/other/[0-9]+/path$",
		},
		{
			input:    "^/other/[0-9]+/path$",
			policy:   v1alpha1.PathPrefixPolicyRequired,
			expected: "^/(es|fr|it)/other/[0-9]+/path$",
		},
		{
			input:    "^/other/[0-9]+/path$",
			policy:   v1alpha1.PathPrefixPolicyDisabled,
			expected: "^/other/[0-9]+/path$",
		},
		{
			input:    "^/$",
			policy:   v1alpha1.PathPrefixPolicyOptional,
			expected: "^(?:/(es|fr|it))?/$",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input+"_"+string(tt.policy), func(t *testing.T) {
			result := ExpandRegexWithPrefixes(tt.input, langPrefixes, tt.policy)
			if result != tt.expected {
				t.Errorf("backwards compat broken:\ninput:    %s\nexpected: %s\ngot:      %s",
					tt.input, tt.expected, result)
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }

func TestConvertActionsPassesReplacePrefixMatch(t *testing.T) {
	tests := []struct {
		name    string
		input   *bool
		wantNil bool
		wantVal bool
	}{
		{"nil stays nil", nil, true, false},
		{"true is passed", boolPtr(true), false, true},
		{"false is passed", boolPtr(false), false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions := convertActions([]v1alpha1.Action{
				{
					Type: v1alpha1.ActionTypeRewrite,
					Rewrite: &v1alpha1.RewriteConfig{
						Path:               "/api/v1",
						ReplacePrefixMatch: tt.input,
					},
				},
			})
			if len(actions) != 1 {
				t.Fatalf("expected 1 action, got %d", len(actions))
			}
			got := actions[0].RewriteReplacePrefixMatch
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %v", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected non-nil, got nil")
				return
			}
			if *got != tt.wantVal {
				t.Errorf("expected %v, got %v", tt.wantVal, *got)
			}
		})
	}
}

func TestBuildBackendStringWithExternalNames(t *testing.T) {
	externalNames := map[string]string{
		"profile-svc/apps": "stable.profile.apps.internal",
	}

	tests := []struct {
		name     string
		refs     []v1alpha1.BackendRef
		extNames map[string]string
		expected string
	}{
		{
			name:     "ExternalName service resolved",
			refs:     []v1alpha1.BackendRef{{Name: "profile-svc", Namespace: "apps", Port: 8080}},
			extNames: externalNames,
			expected: "stable.profile.apps.internal:8080",
		},
		{
			name:     "regular service unchanged",
			refs:     []v1alpha1.BackendRef{{Name: "web", Namespace: "default", Port: 80}},
			extNames: externalNames,
			expected: "web.default.svc.cluster.local:80",
		},
		{
			name:     "nil externalNames map",
			refs:     []v1alpha1.BackendRef{{Name: "profile-svc", Namespace: "apps", Port: 8080}},
			extNames: nil,
			expected: "profile-svc.apps.svc.cluster.local:8080",
		},
		{
			name:     "dotted name still takes precedence",
			refs:     []v1alpha1.BackendRef{{Name: "my.external.host", Namespace: "default", Port: 443}},
			extNames: externalNames,
			expected: "my.external.host:443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildBackendString(tt.refs, tt.extNames)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

// --- preservePrefix tests ---

func TestPreservePrefixPathPrefixRewriteOptional(t *testing.T) {
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
						{Path: "/blog", Type: v1alpha1.MatchTypePathPrefix},
					},
					Actions: []v1alpha1.Action{
						{
							Type: v1alpha1.ActionTypeRewrite,
							Rewrite: &v1alpha1.RewriteConfig{
								Path:           testPathCmsBlog,
								PreservePrefix: boolPtr(true),
							},
						},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "cms", Namespace: "default", Port: 8080},
					},
				},
			},
		},
	}

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

	// 2 prefixed + 1 unprefixed = 3
	if len(routes) != 3 {
		t.Fatalf("expected 3 routes, got %d: %+v", len(routes), routes)
	}

	expected := map[string]string{
		"/blog":    testPathCmsBlog,
		"/es/blog": "/es/cms/blog",
		"/fr/blog": "/fr/cms/blog",
	}

	for _, r := range routes {
		wantRewrite, ok := expected[r.Path]
		if !ok {
			t.Errorf("unexpected route path: %s", r.Path)
			continue
		}
		if len(r.Actions) != 1 || r.Actions[0].RewritePath != wantRewrite {
			t.Errorf("path %s: expected rewrite %q, got %q", r.Path, wantRewrite, r.Actions[0].RewritePath)
		}
	}
}

func TestPreservePrefixPathPrefixRewriteRequired(t *testing.T) {
	cr := &v1alpha1.CustomHTTPRoute{
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Hostnames: []string{"example.com"},
			PathPrefixes: &v1alpha1.PathPrefixes{
				Values: []string{"es", "fr"},
				Policy: v1alpha1.PathPrefixPolicyRequired,
			},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: "/blog", Type: v1alpha1.MatchTypePathPrefix},
					},
					Actions: []v1alpha1.Action{
						{
							Type: v1alpha1.ActionTypeRewrite,
							Rewrite: &v1alpha1.RewriteConfig{
								Path:           testPathCmsBlog,
								PreservePrefix: boolPtr(true),
							},
						},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "cms", Namespace: "default", Port: 8080},
					},
				},
			},
		},
	}

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d: %+v", len(routes), routes)
	}

	expected := map[string]string{
		"/es/blog": "/es/cms/blog",
		"/fr/blog": "/fr/cms/blog",
	}

	for _, r := range routes {
		wantRewrite, ok := expected[r.Path]
		if !ok {
			t.Errorf("unexpected route path: %s", r.Path)
			continue
		}
		if len(r.Actions) != 1 || r.Actions[0].RewritePath != wantRewrite {
			t.Errorf("path %s: expected rewrite %q, got %q", r.Path, wantRewrite, r.Actions[0].RewritePath)
		}
	}
}

func TestPreservePrefixDisabledPolicyNoop(t *testing.T) {
	cr := &v1alpha1.CustomHTTPRoute{
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Hostnames: []string{"example.com"},
			PathPrefixes: &v1alpha1.PathPrefixes{
				Values: []string{"es", "fr"},
				Policy: v1alpha1.PathPrefixPolicyDisabled,
			},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: "/blog", Type: v1alpha1.MatchTypePathPrefix},
					},
					Actions: []v1alpha1.Action{
						{
							Type: v1alpha1.ActionTypeRewrite,
							Rewrite: &v1alpha1.RewriteConfig{
								Path:           testPathCmsBlog,
								PreservePrefix: boolPtr(true),
							},
						},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "cms", Namespace: "default", Port: 8080},
					},
				},
			},
		},
	}

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Actions[0].RewritePath != testPathCmsBlog {
		t.Errorf("expected rewrite /cms/blog, got %s", routes[0].Actions[0].RewritePath)
	}
}

func TestPreservePrefixExactRewrite(t *testing.T) {
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
						{Path: "/about", Type: v1alpha1.MatchTypeExact},
					},
					Actions: []v1alpha1.Action{
						{
							Type: v1alpha1.ActionTypeRewrite,
							Rewrite: &v1alpha1.RewriteConfig{
								Path:           "/pages/about",
								PreservePrefix: boolPtr(true),
							},
						},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "web", Namespace: "default", Port: 80},
					},
				},
			},
		},
	}

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}

	expected := map[string]string{
		"/about":    "/pages/about",
		"/es/about": "/es/pages/about",
	}

	for _, r := range routes {
		wantRewrite, ok := expected[r.Path]
		if !ok {
			t.Errorf("unexpected route path: %s", r.Path)
			continue
		}
		if r.Actions[0].RewritePath != wantRewrite {
			t.Errorf("path %s: expected rewrite %q, got %q", r.Path, wantRewrite, r.Actions[0].RewritePath)
		}
	}
}

func TestPreservePrefixRedirect(t *testing.T) {
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
						{Path: "/old-blog", Type: v1alpha1.MatchTypePathPrefix},
					},
					Actions: []v1alpha1.Action{
						{
							Type: v1alpha1.ActionTypeRedirect,
							Redirect: &v1alpha1.RedirectConfig{
								Path:           "/new-blog",
								StatusCode:     301,
								PreservePrefix: boolPtr(true),
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

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

	if len(routes) != 3 {
		t.Fatalf("expected 3 routes, got %d", len(routes))
	}

	expected := map[string]string{
		"/old-blog":    "/new-blog",
		"/es/old-blog": "/es/new-blog",
		"/fr/old-blog": "/fr/new-blog",
	}

	for _, r := range routes {
		wantRedirect, ok := expected[r.Path]
		if !ok {
			t.Errorf("unexpected route path: %s", r.Path)
			continue
		}
		if r.Actions[0].RedirectPath != wantRedirect {
			t.Errorf("path %s: expected redirect %q, got %q",
				r.Path, wantRedirect, r.Actions[0].RedirectPath)
		}
	}
}

func TestPreservePrefixFalseBackwardCompat(t *testing.T) {
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
						{Path: "/blog", Type: v1alpha1.MatchTypePathPrefix},
					},
					Actions: []v1alpha1.Action{
						{
							Type: v1alpha1.ActionTypeRewrite,
							Rewrite: &v1alpha1.RewriteConfig{
								Path: testPathCmsBlog,
							},
						},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "cms", Namespace: "default", Port: 8080},
					},
				},
			},
		},
	}

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

	// All routes should share the same rewrite path (no prefix prepended)
	for _, r := range routes {
		if r.Actions[0].RewritePath != testPathCmsBlog {
			t.Errorf("path %s: expected rewrite /cms/blog (unchanged), got %q",
				r.Path, r.Actions[0].RewritePath)
		}
	}
}

func TestPreservePrefixNoPathPrefixesDefined(t *testing.T) {
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
							Type: v1alpha1.ActionTypeRewrite,
							Rewrite: &v1alpha1.RewriteConfig{
								Path:           testPathCmsBlog,
								PreservePrefix: boolPtr(true),
							},
						},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "cms", Namespace: "default", Port: 8080},
					},
				},
			},
		},
	}

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Actions[0].RewritePath != testPathCmsBlog {
		t.Errorf("expected rewrite /cms/blog (no-op), got %s", routes[0].Actions[0].RewritePath)
	}
}

func TestPreservePrefixPathVariants(t *testing.T) {
	tests := []struct {
		name        string
		matchPath   string
		rewritePath string
		backendName string
		backendPort int32
		expected    map[string]string
	}{
		{
			name:        "root path",
			matchPath:   "/",
			rewritePath: "/app",
			backendName: "app",
			backendPort: 80,
			expected: map[string]string{
				"/":   "/app",
				"/es": "/es/app",
			},
		},
		{
			name:        "trailing slash",
			matchPath:   "/blog/",
			rewritePath: "/cms/blog/",
			backendName: "cms",
			backendPort: 8080,
			expected: map[string]string{
				"/blog/":    "/cms/blog/",
				"/es/blog/": "/es/cms/blog/",
			},
		},
		{
			name:        "with variables",
			matchPath:   "/blog",
			rewritePath: "/cms/${path.segment.1}",
			backendName: "cms",
			backendPort: 8080,
			expected: map[string]string{
				"/blog":    "/cms/${path.segment.1}",
				"/es/blog": "/es/cms/${path.segment.1}",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
								{Path: tt.matchPath, Type: v1alpha1.MatchTypePathPrefix},
							},
							Actions: []v1alpha1.Action{
								{
									Type: v1alpha1.ActionTypeRewrite,
									Rewrite: &v1alpha1.RewriteConfig{
										Path:           tt.rewritePath,
										PreservePrefix: boolPtr(true),
									},
								},
							},
							BackendRefs: []v1alpha1.BackendRef{
								{Name: tt.backendName, Namespace: "default", Port: tt.backendPort},
							},
						},
					},
				},
			}

			result, err := ExpandRoutes(cr, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			routes := result["example.com"]

			for _, r := range routes {
				wantRewrite, ok := tt.expected[r.Path]
				if !ok {
					t.Errorf("unexpected route path: %s", r.Path)
					continue
				}
				if r.Actions[0].RewritePath != wantRewrite {
					t.Errorf("path %s: expected rewrite %q, got %q", r.Path, wantRewrite, r.Actions[0].RewritePath)
				}
			}
		})
	}
}

func TestPreservePrefixWithReplacePrefixMatchFalse(t *testing.T) {
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
						{Path: "/blog", Type: v1alpha1.MatchTypePathPrefix},
					},
					Actions: []v1alpha1.Action{
						{
							Type: v1alpha1.ActionTypeRewrite,
							Rewrite: &v1alpha1.RewriteConfig{
								Path:               testPathCmsBlog,
								ReplacePrefixMatch: boolPtr(false),
								PreservePrefix:     boolPtr(true),
							},
						},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "cms", Namespace: "default", Port: 8080},
					},
				},
			},
		},
	}

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

	expected := map[string]string{
		"/blog":    testPathCmsBlog,
		"/es/blog": "/es/cms/blog",
	}

	for _, r := range routes {
		wantRewrite, ok := expected[r.Path]
		if !ok {
			t.Errorf("unexpected route path: %s", r.Path)
			continue
		}
		if r.Actions[0].RewritePath != wantRewrite {
			t.Errorf("path %s: expected rewrite %q, got %q", r.Path, wantRewrite, r.Actions[0].RewritePath)
		}
		// Verify replacePrefixMatch is preserved
		if r.Actions[0].RewriteReplacePrefixMatch == nil || *r.Actions[0].RewriteReplacePrefixMatch != false {
			t.Errorf("path %s: expected replacePrefixMatch=false", r.Path)
		}
	}
}

func TestPreservePrefixActionsCloned(t *testing.T) {
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
						{Path: "/blog", Type: v1alpha1.MatchTypePathPrefix},
					},
					Actions: []v1alpha1.Action{
						{
							Type: v1alpha1.ActionTypeRewrite,
							Rewrite: &v1alpha1.RewriteConfig{
								Path:           testPathCmsBlog,
								PreservePrefix: boolPtr(true),
							},
						},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "cms", Namespace: "default", Port: 8080},
					},
				},
			},
		},
	}

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

	// Verify each route's actions slice is independent
	routesByPath := make(map[string]*Route)
	for i := range routes {
		routesByPath[routes[i].Path] = &routes[i]
	}

	esRoute := routesByPath["/es/blog"]
	frRoute := routesByPath["/fr/blog"]
	baseRoute := routesByPath["/blog"]

	if esRoute == nil || frRoute == nil || baseRoute == nil {
		t.Fatalf("missing expected routes: es=%v fr=%v base=%v", esRoute, frRoute, baseRoute)
	}

	if esRoute.Actions[0].RewritePath != "/es/cms/blog" {
		t.Errorf("es route: expected /es/cms/blog, got %s", esRoute.Actions[0].RewritePath)
	}
	if frRoute.Actions[0].RewritePath != "/fr/cms/blog" {
		t.Errorf("fr route: expected /fr/cms/blog, got %s", frRoute.Actions[0].RewritePath)
	}
	if baseRoute.Actions[0].RewritePath != testPathCmsBlog {
		t.Errorf("base route: expected /cms/blog, got %s", baseRoute.Actions[0].RewritePath)
	}
}

func TestPreservePrefixHostnameOnlyRewrite(t *testing.T) {
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
						{Path: "/blog", Type: v1alpha1.MatchTypePathPrefix},
					},
					Actions: []v1alpha1.Action{
						{
							Type: v1alpha1.ActionTypeRewrite,
							Rewrite: &v1alpha1.RewriteConfig{
								Hostname:       "internal.svc.local",
								PreservePrefix: boolPtr(true),
							},
						},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "cms", Namespace: "default", Port: 8080},
					},
				},
			},
		},
	}

	result, err := ExpandRoutes(cr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	routes := result["example.com"]

	// All routes should have the same hostname, no path modification
	for _, r := range routes {
		if r.Actions[0].RewriteHostname != "internal.svc.local" {
			t.Errorf("path %s: expected hostname internal.svc.local, got %s",
				r.Path, r.Actions[0].RewriteHostname)
		}
		if r.Actions[0].RewritePath != "" {
			t.Errorf("path %s: expected empty rewrite path, got %s", r.Path, r.Actions[0].RewritePath)
		}
	}
}

func TestExpandRoutesWithExternalNames(t *testing.T) {
	cr := &v1alpha1.CustomHTTPRoute{
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Hostnames: []string{"app.example.com"},
			Rules: []v1alpha1.Rule{
				{
					Matches: []v1alpha1.PathMatch{
						{Path: "/profile", Type: v1alpha1.MatchTypePathPrefix},
					},
					BackendRefs: []v1alpha1.BackendRef{
						{Name: "profile-svc", Namespace: "apps", Port: 8080},
					},
				},
			},
		},
	}

	extNames := map[string]string{
		"profile-svc/apps": "stable.profile.apps.internal",
	}

	result, err := ExpandRoutes(cr, extNames)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	routes := result["app.example.com"]
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	expected := "stable.profile.apps.internal:8080"
	if routes[0].Backend != expected {
		t.Errorf("expected backend %q, got %q", expected, routes[0].Backend)
	}
}

func TestSortRoutesSpecificityTiebreakers(t *testing.T) {
	t.Run("method constrained routes sort before unconstrained routes", func(t *testing.T) {
		routes := []Route{
			{Path: "/example", Type: RouteTypePrefix, Priority: 1000},
			{Path: "/example", Type: RouteTypePrefix, Priority: 1000, Method: "GET"},
		}

		SortRoutes(routes)

		if routes[0].Method != "GET" {
			t.Fatalf("expected method-constrained route first, got %+v", routes[0])
		}
	})

	t.Run("routes with more header matches sort first", func(t *testing.T) {
		routes := []Route{
			{
				Path:     "/example",
				Type:     RouteTypePrefix,
				Priority: 1000,
				Headers: []RouteHeaderMatch{
					{Name: "force", Value: "staging"},
				},
			},
			{Path: "/example", Type: RouteTypePrefix, Priority: 1000},
		}

		SortRoutes(routes)

		if len(routes[0].Headers) != 1 {
			t.Fatalf("expected header-constrained route first, got %+v", routes[0])
		}
	})

	t.Run("routes with more query param matches sort first", func(t *testing.T) {
		routes := []Route{
			{
				Path:     "/example",
				Type:     RouteTypePrefix,
				Priority: 1000,
				QueryParams: []RouteQueryParamMatch{
					{Name: "env", Value: "staging"},
				},
			},
			{Path: "/example", Type: RouteTypePrefix, Priority: 1000},
		}

		SortRoutes(routes)

		if len(routes[0].QueryParams) != 1 {
			t.Fatalf("expected query-constrained route first, got %+v", routes[0])
		}
	})
}

func TestFindRoutePrefersMoreSpecificMatches(t *testing.T) {
	t.Run("header-specific route wins over generic route", func(t *testing.T) {
		routes := []Route{
			{
				Path:     "/example",
				Type:     RouteTypePrefix,
				Priority: 1000,
				Backend:  "stable.default.svc.cluster.local:80",
			},
			{
				Path:     "/example",
				Type:     RouteTypePrefix,
				Priority: 1000,
				Backend:  "staging.default.svc.cluster.local:80",
				Headers: []RouteHeaderMatch{
					{Name: "force", Value: "staging"},
				},
			},
		}
		SortRoutes(routes)

		loader := &Loader{
			config: &RoutesConfig{
				Version: 1,
				Hosts: map[string][]Route{
					"example.com": routes,
				},
			},
		}

		route := loader.FindRoute("example.com", RequestMatch{
			Path:    "/example",
			Headers: map[string]string{"force": "staging"},
		})
		if route == nil {
			t.Fatal("expected matching route")
		}
		if route.Backend != "staging.default.svc.cluster.local:80" {
			t.Fatalf("expected staging backend, got %q", route.Backend)
		}
	})

	t.Run("method-specific route wins over generic route", func(t *testing.T) {
		routes := []Route{
			{
				Path:     "/example",
				Type:     RouteTypePrefix,
				Priority: 1000,
				Backend:  "stable.default.svc.cluster.local:80",
			},
			{
				Path:     "/example",
				Type:     RouteTypePrefix,
				Priority: 1000,
				Backend:  "get.default.svc.cluster.local:80",
				Method:   "GET",
			},
		}
		SortRoutes(routes)

		loader := &Loader{
			config: &RoutesConfig{
				Version: 1,
				Hosts: map[string][]Route{
					"example.com": routes,
				},
			},
		}

		route := loader.FindRoute("example.com", RequestMatch{
			Path:   "/example",
			Method: "GET",
		})
		if route == nil {
			t.Fatal("expected matching route")
		}
		if route.Backend != "get.default.svc.cluster.local:80" {
			t.Fatalf("expected GET backend, got %q", route.Backend)
		}
	})

	t.Run("query-specific route wins over generic route", func(t *testing.T) {
		routes := []Route{
			{
				Path:     "/example",
				Type:     RouteTypePrefix,
				Priority: 1000,
				Backend:  "stable.default.svc.cluster.local:80",
			},
			{
				Path:     "/example",
				Type:     RouteTypePrefix,
				Priority: 1000,
				Backend:  "staging.default.svc.cluster.local:80",
				QueryParams: []RouteQueryParamMatch{
					{Name: "env", Value: "staging"},
				},
			},
		}
		SortRoutes(routes)

		loader := &Loader{
			config: &RoutesConfig{
				Version: 1,
				Hosts: map[string][]Route{
					"example.com": routes,
				},
			},
		}

		route := loader.FindRoute("example.com", RequestMatch{
			Path:        "/example",
			QueryParams: map[string]string{"env": "staging"},
		})
		if route == nil {
			t.Fatal("expected matching route")
		}
		if route.Backend != "staging.default.svc.cluster.local:80" {
			t.Fatalf("expected staging backend, got %q", route.Backend)
		}
	})
}
