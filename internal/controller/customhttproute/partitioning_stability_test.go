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

import (
	"fmt"
	"strings"
	"testing"

	"github.com/freepik-company/customrouter/pkg/routes"
)

const testHost = "example.com"

// largeRouteSet produces n routes that, in aggregate, exceed maxConfigMapSize
// so splitHostRoutes is forced to bucket them across multiple partitions.
func largeRouteSet(prefix string, n int) []routes.Route {
	out := make([]routes.Route, n)
	// Pad backend with bytes so each route is heavy enough to push the total
	// payload past the 1MB partition limit with just a few hundred entries.
	padding := strings.Repeat("x", 4096)
	for i := 0; i < n; i++ {
		out[i] = routes.Route{
			Type:    routes.RouteTypePrefix,
			Path:    fmt.Sprintf("/%s/%d/page", prefix, i),
			Backend: padding + "/backend-" + fmt.Sprintf("%d", i),
		}
	}
	return out
}

// partitionsByName indexes a slice of partitions by their CM name for
// difference-style assertions.
func partitionsByName(parts []ConfigMapPartition) map[string]string {
	out := make(map[string]string, len(parts))
	for _, p := range parts {
		out[p.Name] = p.Data
	}
	return out
}

func diffPartitions(before, after map[string]string) (changed, added, removed []string) {
	for name, data := range after {
		prev, ok := before[name]
		if !ok {
			added = append(added, name)
			continue
		}
		if prev != data {
			changed = append(changed, name)
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			removed = append(removed, name)
		}
	}
	return
}

// TestSplitHostRoutes_StableUnderInsertion is the core regression test for the
// hash-based partitioning. With greedy size-based packing, inserting a single
// route at an arbitrary position in the input slice cascades every downstream
// partition's content. With hash assignment a single insertion must touch at
// most one partition.
func TestSplitHostRoutes_StableUnderInsertion(t *testing.T) {
	r := &CustomHTTPRouteReconciler{ConfigMapNamespace: "default"}
	host := testHost

	base := largeRouteSet("base", 600)
	beforeParts := r.splitHostRoutes("default", host, base, 0)
	if len(beforeParts) < 5 {
		t.Fatalf("test setup too small: only %d partitions", len(beforeParts))
	}
	before := partitionsByName(beforeParts)

	// Insert one new route somewhere in the middle. Greedy packing would
	// shift all subsequent partition boundaries; hash assignment must not.
	withInsert := make([]routes.Route, 0, len(base)+1)
	withInsert = append(withInsert, base[:300]...)
	withInsert = append(withInsert, routes.Route{
		Type:    routes.RouteTypePrefix,
		Path:    "/inserted/somewhere/middle",
		Backend: strings.Repeat("y", 4096) + "/inserted",
	})
	withInsert = append(withInsert, base[300:]...)

	afterParts := r.splitHostRoutes("default", host, withInsert, 0)
	after := partitionsByName(afterParts)

	changed, added, removed := diffPartitions(before, after)
	totalAffected := len(changed) + len(added) + len(removed)
	if totalAffected > 1 {
		t.Fatalf("expected at most 1 partition to change on single insert; got changed=%v added=%v removed=%v (total=%d)",
			changed, added, removed, totalAffected)
	}
}

// TestSplitHostRoutes_StableUnderReorder asserts that reordering the input
// without changing the set of routes produces identical partitions.
func TestSplitHostRoutes_StableUnderReorder(t *testing.T) {
	r := &CustomHTTPRouteReconciler{ConfigMapNamespace: "default"}
	host := testHost

	base := largeRouteSet("base", 600)
	before := partitionsByName(r.splitHostRoutes("default", host, base, 0))

	// Reverse the slice to exercise the "different input order, same set"
	// case that would shuffle a greedy packer.
	reordered := make([]routes.Route, len(base))
	for i := range base {
		reordered[i] = base[len(base)-1-i]
	}
	after := partitionsByName(r.splitHostRoutes("default", host, reordered, 0))

	changed, added, removed := diffPartitions(before, after)
	if len(changed)+len(added)+len(removed) != 0 {
		t.Fatalf("reorder of identical route set should not change partitions; got changed=%v added=%v removed=%v",
			changed, added, removed)
	}
}

// TestSplitHostRoutes_DeterministicForSameInput confirms two runs with the
// same input produce byte-identical partitions (required by the per-partition
// content dedup at upsertSingleConfigMap).
func TestSplitHostRoutes_DeterministicForSameInput(t *testing.T) {
	r := &CustomHTTPRouteReconciler{ConfigMapNamespace: "default"}
	host := testHost
	base := largeRouteSet("base", 200)

	run1 := partitionsByName(r.splitHostRoutes("default", host, base, 0))
	run2 := partitionsByName(r.splitHostRoutes("default", host, base, 0))

	if len(run1) != len(run2) {
		t.Fatalf("partition count diverged: run1=%d run2=%d", len(run1), len(run2))
	}
	for name, data1 := range run1 {
		data2, ok := run2[name]
		if !ok {
			t.Errorf("partition %q present in run1 but missing in run2", name)
			continue
		}
		if data1 != data2 {
			t.Errorf("partition %q differs across runs (non-deterministic packing)", name)
		}
	}
}

// TestSplitHostRoutes_RespectsConfigMapSizeLimit asserts that no emitted
// partition exceeds the 1MB ConfigMap data limit even when an unlucky hash
// distribution would have packed too many large routes together.
func TestSplitHostRoutes_RespectsConfigMapSizeLimit(t *testing.T) {
	r := &CustomHTTPRouteReconciler{ConfigMapNamespace: "default"}
	host := testHost

	base := largeRouteSet("base", 1500)
	parts := r.splitHostRoutes("default", host, base, 0)

	for _, p := range parts {
		if len(p.Data) > maxConfigMapSize {
			t.Errorf("partition %s is %d bytes, exceeds maxConfigMapSize %d", p.Name, len(p.Data), maxConfigMapSize)
		}
	}
}

// TestSplitHostRoutes_EmptyInput returns no partitions for an empty input.
func TestSplitHostRoutes_EmptyInput(t *testing.T) {
	r := &CustomHTTPRouteReconciler{ConfigMapNamespace: "default"}
	parts := r.splitHostRoutes("default", testHost, nil, 0)
	if len(parts) != 0 {
		t.Fatalf("expected 0 partitions for empty input, got %d", len(parts))
	}
}

// TestRouteBucket_Deterministic asserts the bucket function returns the same
// index for the same input across calls — this is what makes the
// partitioning content-stable across reconciles.
func TestRouteBucket_Deterministic(t *testing.T) {
	host := testHost
	route := routes.Route{
		Type:    routes.RouteTypePrefix,
		Path:    "/api/v2",
		Method:  "GET",
		Backend: "svc.default.svc.cluster.local:80",
		Headers: []routes.RouteHeaderMatch{{Name: "X-Tenant", Value: "acme"}},
	}

	const buckets = 32
	first := routeBucket(host, route, buckets)
	for i := 0; i < 100; i++ {
		if got := routeBucket(host, route, buckets); got != first {
			t.Fatalf("routeBucket non-deterministic: first=%d call %d returned %d", first, i, got)
		}
	}
}

// TestRouteBucket_DistinctRoutesDistribute is a smoke test that the hash
// distributes a range of routes across the available buckets. We allow a
// generous tolerance because FNV does not guarantee perfect uniformity but
// the storm regression we care about appears at any non-trivial spread.
func TestRouteBucket_DistinctRoutesDistribute(t *testing.T) {
	const buckets = 16
	hits := make(map[uint32]int, buckets)
	for i := 0; i < 1000; i++ {
		route := routes.Route{
			Type:    routes.RouteTypePrefix,
			Path:    fmt.Sprintf("/route/%d", i),
			Backend: fmt.Sprintf("svc-%d.default.svc.cluster.local:80", i),
		}
		hits[routeBucket(testHost, route, buckets)]++
	}
	if len(hits) < buckets/2 {
		t.Fatalf("hash distribution too narrow: %d buckets hit out of %d", len(hits), buckets)
	}
}
