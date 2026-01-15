/*
Copyright 2024.

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

// Package routes provides shared types and utilities for the customrouter project.
// These types are used by both the controller (to generate routes) and the extproc (to serve them).
package routes

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Route represents a single expanded route for the proxy
type Route struct {
	Path     string `json:"path"`
	Type     string `json:"type"` // "exact", "prefix", "regex"
	Backend  string `json:"backend"`
	Priority int32  `json:"priority"`

	// compiledRegex is the compiled regex for regex type routes (not serialized)
	compiledRegex *regexp.Regexp
}

// RoutesConfig is the top-level structure for the ConfigMap data
type RoutesConfig struct {
	Version int                `json:"version"`
	Hosts   map[string][]Route `json:"hosts"`
}

// RouteType constants
const (
	RouteTypeExact  = "exact"
	RouteTypePrefix = "prefix"
	RouteTypeRegex  = "regex"
)

// ParseJSON parses a JSON byte slice into a RoutesConfig
func ParseJSON(data []byte) (*RoutesConfig, error) {
	var config RoutesConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

// ToJSON serializes the routes config to JSON
func (rc *RoutesConfig) ToJSON() ([]byte, error) {
	return json.MarshalIndent(rc, "", "  ")
}

// CompileRegexes compiles all regex patterns in the routes config.
// Should be called after loading the config.
func (rc *RoutesConfig) CompileRegexes() error {
	for host := range rc.Hosts {
		for i := range rc.Hosts[host] {
			route := &rc.Hosts[host][i]
			if route.Type == RouteTypeRegex {
				re, err := regexp.Compile(route.Path)
				if err != nil {
					return err
				}
				route.compiledRegex = re
			}
		}
	}
	return nil
}

// Match checks if the given path matches this route
func (r *Route) Match(path string) bool {
	switch r.Type {
	case RouteTypeExact:
		return path == r.Path
	case RouteTypePrefix:
		return strings.HasPrefix(path, r.Path)
	case RouteTypeRegex:
		if r.compiledRegex != nil {
			return r.compiledRegex.MatchString(path)
		}
		// Fallback: compile on the fly (slower)
		re, err := regexp.Compile(r.Path)
		if err != nil {
			return false
		}
		return re.MatchString(path)
	default:
		return false
	}
}

// ParseBackend parses the backend string into host and port
// Backend format: "service.namespace.svc.cluster.local:port"
func (r *Route) ParseBackend() (host string, port string) {
	parts := strings.Split(r.Backend, ":")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return r.Backend, "80"
}
