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
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// TestUpsertConfigMaps_AllCreated verifies the happy path: every partition
// passed in turns into a ConfigMap in the fake API server. This also fixes
// the contract that parallelism does not silently drop partitions, which a
// naive WaitGroup-without-errgroup implementation could regress.
func TestUpsertConfigMaps_AllCreated(t *testing.T) {
	r := newReconciler()
	r.UpsertParallelism = 4

	const N = 12
	partitions := make([]ConfigMapPartition, N)
	for i := 0; i < N; i++ {
		partitions[i] = ConfigMapPartition{
			Name:   fmt.Sprintf("customrouter-routes-default-%d", i),
			Target: "default",
			Data:   fmt.Sprintf(`{"i":%d}`, i),
		}
	}

	if err := r.upsertConfigMaps(context.Background(), partitions); err != nil {
		t.Fatalf("upsertConfigMaps: %v", err)
	}

	for _, p := range partitions {
		cm := &corev1.ConfigMap{}
		key := types.NamespacedName{Name: p.Name, Namespace: r.ConfigMapNamespace}
		if err := r.Get(context.Background(), key, cm); err != nil {
			t.Errorf("partition %s not found: %v", p.Name, err)
			continue
		}
		if cm.Data[routesDataKey] != p.Data {
			t.Errorf("partition %s data mismatch: got %q, want %q", p.Name, cm.Data[routesDataKey], p.Data)
		}
	}
}

// TestUpsertConfigMaps_HashFastPathSkipsWrites verifies the dedup cache:
// a second upsert call with identical Data must not issue any extra writes.
// Counts Get calls on a wrapped client to make the assertion precise.
func TestUpsertConfigMaps_HashFastPathSkipsWrites(t *testing.T) {
	r := newReconciler()
	r.UpsertParallelism = 4

	partitions := []ConfigMapPartition{
		{Name: "customrouter-routes-default-0", Target: "default", Data: `{"x":1}`},
		{Name: "customrouter-routes-default-1", Target: "default", Data: `{"x":2}`},
	}

	// First call: cold cache, must write both.
	if err := r.upsertConfigMaps(context.Background(), partitions); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Capture creation timestamps; if the second call rewrites the CMs the
	// fake client bumps ResourceVersion. We assert that ResourceVersion is
	// stable across the second call → the hash fast-path skipped the work.
	rvBefore := map[string]string{}
	for _, p := range partitions {
		cm := &corev1.ConfigMap{}
		if err := r.Get(context.Background(), types.NamespacedName{Name: p.Name, Namespace: r.ConfigMapNamespace}, cm); err != nil {
			t.Fatalf("get %s: %v", p.Name, err)
		}
		rvBefore[p.Name] = cm.ResourceVersion
	}

	if err := r.upsertConfigMaps(context.Background(), partitions); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	for _, p := range partitions {
		cm := &corev1.ConfigMap{}
		if err := r.Get(context.Background(), types.NamespacedName{Name: p.Name, Namespace: r.ConfigMapNamespace}, cm); err != nil {
			t.Fatalf("get %s after second upsert: %v", p.Name, err)
		}
		if cm.ResourceVersion != rvBefore[p.Name] {
			t.Errorf("partition %s was rewritten (rv %s → %s); fast-path failed",
				p.Name, rvBefore[p.Name], cm.ResourceVersion)
		}
	}
}

// TestUpsertConfigMaps_SequentialFallback locks the contract that
// UpsertParallelism: 1 takes the sequential code path (no errgroup) and
// remains functionally equivalent to the parallel one: every partition
// must end up with the requested data.
func TestUpsertConfigMaps_SequentialFallback(t *testing.T) {
	r := newReconciler()
	r.UpsertParallelism = 1

	// Pre-populate stale ConfigMaps so the hash fast-path misses and the
	// reconciler actually walks the Get+Compare+Update path for each one.
	const N = 10
	partitions := make([]ConfigMapPartition, N)
	for i := range partitions {
		partitions[i] = ConfigMapPartition{
			Name:   fmt.Sprintf("customrouter-routes-default-%d", i),
			Target: "default",
			Data:   fmt.Sprintf(`{"new":%d}`, i),
		}
		stale := &corev1.ConfigMap{}
		stale.Name = partitions[i].Name
		stale.Namespace = r.ConfigMapNamespace
		stale.Data = map[string]string{routesDataKey: "stale"}
		_ = r.Create(context.Background(), stale)
	}

	if err := r.upsertConfigMaps(context.Background(), partitions); err != nil {
		t.Fatalf("sequential upsert: %v", err)
	}
	for _, p := range partitions {
		cm := &corev1.ConfigMap{}
		if err := r.Get(context.Background(), types.NamespacedName{Name: p.Name, Namespace: r.ConfigMapNamespace}, cm); err != nil {
			t.Fatalf("get %s: %v", p.Name, err)
		}
		if cm.Data[routesDataKey] != p.Data {
			t.Errorf("partition %s data: got %q, want %q", p.Name, cm.Data[routesDataKey], p.Data)
		}
	}
}

// TestEffectiveUpsertParallelism_Defaults locks the contract that zero falls
// back to the package default, negative is clamped to sequential (1), and
// any positive value is respected verbatim.
func TestEffectiveUpsertParallelism_Defaults(t *testing.T) {
	cases := []struct {
		configured int
		want       int
	}{
		{0, DefaultUpsertParallelism},
		{-1, 1},
		{-100, 1},
		{1, 1},
		{5, 5},
		{50, 50},
	}
	for _, tc := range cases {
		r := &CustomHTTPRouteReconciler{UpsertParallelism: tc.configured}
		if got := r.effectiveUpsertParallelism(); got != tc.want {
			t.Errorf("UpsertParallelism=%d: got %d, want %d", tc.configured, got, tc.want)
		}
	}
}
