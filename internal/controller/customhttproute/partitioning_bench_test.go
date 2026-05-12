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

package customhttproute

import "testing"

func BenchmarkSplitHostRoutes(b *testing.B) {
	cases := []struct {
		name string
		n    int
	}{
		{"100_routes", 100},
		{"600_routes", 600},
		{"2000_routes", 2000},
		{"5000_routes", 5000},
	}
	r := &CustomHTTPRouteReconciler{ConfigMapNamespace: "default"}
	host := "example.com"

	for _, tc := range cases {
		routes := largeRouteSet("bench", tc.n)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = r.splitHostRoutes("default", host, routes, 0)
			}
		})
	}
}
