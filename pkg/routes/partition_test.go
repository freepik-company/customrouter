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

package routes

import (
	"fmt"
	"testing"
)

// testHost is the shared hostname used across the partition tests.
const testHost = "example.com"

// envHeader builds a single exact env header matcher.
func envHeader(value string) []RouteHeaderMatch {
	return []RouteHeaderMatch{{Name: "env", Value: value, Type: HeaderMatchExact}}
}

// fullScan replicates the historical lookup so tests can assert the partition
// index returns byte-for-byte identical results.
func fullScan(hostRoutes []Route, req RequestMatch) *Route {
	for i := range hostRoutes {
		if hostRoutes[i].Match(req) {
			return &hostRoutes[i]
		}
	}
	return nil
}

func TestRoutePartitionValue(t *testing.T) {
	tests := []struct {
		name   string
		route  Route
		want   string
		wantOK bool
	}{
		{
			name:   "single exact env header is partitionable",
			route:  Route{Headers: envHeader("sbx-a")},
			want:   "sbx-a",
			wantOK: true,
		},
		{
			name:   "no env header is unpartitioned",
			route:  Route{Headers: []RouteHeaderMatch{{Name: "x-other", Value: "v"}}},
			want:   "",
			wantOK: false,
		},
		{
			name:   "empty type defaults to exact and is partitionable",
			route:  Route{Headers: []RouteHeaderMatch{{Name: "env", Value: "sbx-b"}}},
			want:   "sbx-b",
			wantOK: true,
		},
		{
			name:   "regex env header is unpartitioned",
			route:  Route{Headers: []RouteHeaderMatch{{Name: "env", Value: "sbx-.*", Type: HeaderMatchRegex}}},
			want:   "",
			wantOK: false,
		},
		{
			name:   "header name match is case-insensitive",
			route:  Route{Headers: []RouteHeaderMatch{{Name: "Env", Value: "sbx-c"}}},
			want:   "sbx-c",
			wantOK: true,
		},
		{
			name:   "multiple env headers are unpartitioned",
			route:  Route{Headers: []RouteHeaderMatch{{Name: "env", Value: "a"}, {Name: "env", Value: "b"}}},
			want:   "",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := routePartitionValue(&tt.route, "env")
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("routePartitionValue = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// buildSandboxConfig creates a config mirroring the sbx topology: several
// sandboxes share a host, each route gated by an "env" header, plus a couple of
// env-agnostic routes to exercise the unpartitioned-merge path.
func buildSandboxConfig(t *testing.T, withPartition bool) *RoutesConfig {
	t.Helper()
	host := testHost
	envs := []string{"sbx-a", "sbx-b", "sbx-c"}
	hostRoutes := make([]Route, 0, len(envs)*3+2)
	for _, env := range envs {
		hostRoutes = append(hostRoutes,
			Route{Path: "/images", Type: RouteTypeExact, Backend: env + "-images", Priority: 1000, Headers: envHeader(env)},
			Route{Path: "/static", Type: RouteTypePrefix, Backend: env + "-static", Priority: 1000, Headers: envHeader(env)},
			Route{Path: "/", Type: RouteTypePrefix, Backend: env + "-catchall", Priority: 1, Headers: envHeader(env)},
		)
	}
	// Env-agnostic routes (no env header): must be considered for every value.
	hostRoutes = append(hostRoutes,
		Route{Path: "/healthz", Type: RouteTypeExact, Backend: "shared-health", Priority: 5000},
		Route{Path: "/shared", Type: RouteTypePrefix, Backend: "shared-prefix", Priority: 2000},
	)

	cfg := &RoutesConfig{Version: 1, Hosts: map[string][]Route{host: hostRoutes}}
	SortRoutes(cfg.Hosts[host])
	if err := cfg.CompileRegexes(); err != nil {
		t.Fatalf("CompileRegexes: %v", err)
	}
	if withPartition {
		cfg.BuildPartitionIndex("env")
	}
	return cfg
}

func TestFindRoutePartitionEquivalence(t *testing.T) {
	host := testHost
	full := buildSandboxConfig(t, false)
	part := buildSandboxConfig(t, true)

	paths := []string{"/images", "/static/app.js", "/anything/deep/path", "/healthz", "/shared/x", "/"}
	envs := []string{"sbx-a", "sbx-b", "sbx-c", "sbx-unknown", ""}
	methods := []string{"GET", "POST"}

	for _, env := range envs {
		for _, path := range paths {
			for _, method := range methods {
				headers := map[string]string{}
				if env != "" {
					headers["env"] = env
				}
				req := RequestMatch{Path: path, Method: method, Headers: headers}

				want := fullScan(full.Hosts[host], req)
				gotFull := full.FindRoute(host, req)
				gotPart := part.FindRoute(host, req)

				if !sameRoute(gotFull, want) {
					t.Fatalf("baseline FindRoute diverged from full scan for env=%q path=%q: got %s want %s",
						env, path, backendOf(gotFull), backendOf(want))
				}
				if !sameRoute(gotPart, want) {
					t.Fatalf("partitioned FindRoute diverged for env=%q path=%q method=%q: got %s want %s",
						env, path, method, backendOf(gotPart), backendOf(want))
				}
			}
		}
	}
}

// TestFindRoutePartitionRandomizedEquivalence fuzzes a larger interleaved route
// set to make sure the index never changes which route wins.
func TestFindRoutePartitionRandomizedEquivalence(t *testing.T) {
	host := testHost
	hostRoutes := make([]Route, 0, 60)
	for i := 0; i < 50; i++ {
		env := fmt.Sprintf("e%d", i%7)
		prio := int32(1000 + (i*7)%500)
		path := fmt.Sprintf("/p%d", i%11)
		hostRoutes = append(hostRoutes, Route{
			Path:     path,
			Type:     RouteTypePrefix,
			Backend:  fmt.Sprintf("b%d", i),
			Priority: prio,
			Headers:  envHeader(env),
		})
		if i%5 == 0 {
			hostRoutes = append(hostRoutes, Route{
				Path:     path,
				Type:     RouteTypePrefix,
				Backend:  fmt.Sprintf("shared%d", i),
				Priority: prio,
			})
		}
	}
	full := &RoutesConfig{Version: 1, Hosts: map[string][]Route{host: append([]Route(nil), hostRoutes...)}}
	SortRoutes(full.Hosts[host])
	_ = full.CompileRegexes()

	part := &RoutesConfig{Version: 1, Hosts: map[string][]Route{host: append([]Route(nil), hostRoutes...)}}
	SortRoutes(part.Hosts[host])
	_ = part.CompileRegexes()
	part.BuildPartitionIndex("env")

	for e := 0; e < 9; e++ {
		for p := 0; p < 13; p++ {
			req := RequestMatch{
				Path:    fmt.Sprintf("/p%d/sub", p),
				Method:  "GET",
				Headers: map[string]string{"env": fmt.Sprintf("e%d", e)},
			}
			want := full.FindRoute(host, req)
			got := part.FindRoute(host, req)
			if !sameRoute(got, want) {
				t.Fatalf("divergence env=e%d path=/p%d/sub: got %s want %s", e, p, backendOf(got), backendOf(want))
			}
		}
	}
}

func TestBuildPartitionIndexDisabledByDefault(t *testing.T) {
	cfg := buildSandboxConfig(t, false)
	if cfg.partitions != nil || cfg.partitionHeader != "" {
		t.Fatal("partition index must stay nil when BuildPartitionIndex is not called")
	}
	// Empty header is an explicit no-op (clears any prior index).
	cfg.BuildPartitionIndex("")
	if cfg.partitions != nil || cfg.partitionHeader != "" {
		t.Fatal("BuildPartitionIndex(\"\") must disable partitioning")
	}
}

// buildLargeSandboxConfig emulates the sbx topology: many sandboxes share one
// host, every route gated by its own "env" header, with a low-priority catch-all
// per sandbox (the worst case — static-asset requests fall through to it).
func buildLargeSandboxConfig(sandboxes, pathsPerSandbox int, partition bool) (*RoutesConfig, string) {
	host := testHost
	hostRoutes := make([]Route, 0, sandboxes*(pathsPerSandbox+1))
	for s := 0; s < sandboxes; s++ {
		env := fmt.Sprintf("sbx-%d", s)
		for p := 0; p < pathsPerSandbox; p++ {
			hostRoutes = append(hostRoutes, Route{
				Path:     fmt.Sprintf("/section-%d", p),
				Type:     RouteTypePrefix,
				Backend:  fmt.Sprintf("%s-b%d", env, p),
				Priority: 1000,
				Headers:  envHeader(env),
			})
		}
		hostRoutes = append(hostRoutes, Route{
			Path: "/", Type: RouteTypePrefix, Backend: env + "-catchall", Priority: 1, Headers: envHeader(env),
		})
	}
	cfg := &RoutesConfig{Version: 1, Hosts: map[string][]Route{host: hostRoutes}}
	SortRoutes(cfg.Hosts[host])
	_ = cfg.CompileRegexes()
	if partition {
		cfg.BuildPartitionIndex("env")
	}
	return cfg, host
}

// BenchmarkFindRouteFullScan vs BenchmarkFindRoutePartitioned: a static-asset
// request that only matches its sandbox catch-all, forcing a worst-case scan.
func BenchmarkFindRouteFullScan(b *testing.B) {
	cfg, host := buildLargeSandboxConfig(40, 80, false)
	req := RequestMatch{Path: "/static/app.123.js", Method: "GET", Headers: map[string]string{"env": "sbx-20"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cfg.FindRoute(host, req)
	}
}

func BenchmarkFindRoutePartitioned(b *testing.B) {
	cfg, host := buildLargeSandboxConfig(40, 80, true)
	req := RequestMatch{Path: "/static/app.123.js", Method: "GET", Headers: map[string]string{"env": "sbx-20"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cfg.FindRoute(host, req)
	}
}

func sameRoute(a, b *Route) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Backend == b.Backend && a.Path == b.Path && a.Type == b.Type
}

func backendOf(r *Route) string {
	if r == nil {
		return "<nil>"
	}
	return r.Backend
}
