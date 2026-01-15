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
