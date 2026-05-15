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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// TestPartitionHashesPersistRoundTrip exercises the checkpoint contract:
// a Persist followed by a Load on a fresh reconciler must restore the exact
// same map. This is the property the runner relies on to give the next leader
// a warm cache after a restart.
func TestPartitionHashesPersistRoundTrip(t *testing.T) {
	writer := newReconciler()
	writer.partitionHashes = map[string]uint32{
		"customrouter-routes-default-0": 0xdeadbeef,
		"customrouter-routes-default-1": 0x12345678,
		"customrouter-routes-shadow-0":  0,          // zero is a valid hash
		"customrouter-routes-other-42":  ^uint32(0), // max uint32 must round-trip
	}

	if err := writer.persistPartitionHashes(context.Background()); err != nil {
		t.Fatalf("persist: %v", err)
	}

	// Pull the checkpoint ConfigMap out of the writer's fake client and
	// inject it into a fresh reconciler, simulating a controller restart.
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Name: partitionHashesConfigMapName, Namespace: writer.ConfigMapNamespace}
	if err := writer.Get(context.Background(), key, cm); err != nil {
		t.Fatalf("checkpoint ConfigMap missing: %v", err)
	}

	reader := newReconciler(cm)
	if err := reader.loadPartitionHashes(context.Background()); err != nil {
		t.Fatalf("load: %v", err)
	}

	if got, want := len(reader.partitionHashes), len(writer.partitionHashes); got != want {
		t.Fatalf("entry count: got %d, want %d", got, want)
	}
	for k, want := range writer.partitionHashes {
		if got := reader.partitionHashes[k]; got != want {
			t.Errorf("hash for %q: got %v, want %v", k, got, want)
		}
	}
}

// TestPartitionHashesLoadMissingConfigMap fixes the "cold start with no
// checkpoint" contract: it must not error, and must leave the map empty so
// the controller falls back to the pre-0.7.2 behaviour of re-validating
// every partition.
func TestPartitionHashesLoadMissingConfigMap(t *testing.T) {
	r := newReconciler()
	if err := r.loadPartitionHashes(context.Background()); err != nil {
		t.Fatalf("load on empty cluster should not error, got: %v", err)
	}
	if got := len(r.partitionHashes); got != 0 {
		t.Errorf("expected empty cache, got %d entries", got)
	}
}

// TestPartitionHashesPersistEmpty avoids creating an empty checkpoint
// ConfigMap on cold start. Otherwise every fresh deploy would leave a
// confusing zero-byte object around until the first rebuild populated it.
func TestPartitionHashesPersistEmpty(t *testing.T) {
	r := newReconciler()
	if err := r.persistPartitionHashes(context.Background()); err != nil {
		t.Fatalf("persist of empty map should not error, got: %v", err)
	}
	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Name: partitionHashesConfigMapName, Namespace: r.ConfigMapNamespace}
	if err := r.Get(context.Background(), key, cm); err == nil {
		t.Errorf("expected no checkpoint ConfigMap, but one was created: %+v", cm.Data)
	}
}

// TestPartitionHashesPersistDeletesStaleCheckpointWhenMapEmpty fixes the
// scenario flagged in PR #40 review: if persistPartitionHashes returned
// early on an empty in-memory map without touching the on-disk checkpoint,
// a subsequent restart would load stale entries. The fast-path in
// upsertSingleConfigMap could then skip the Create for a freshly-needed
// ConfigMap (same name, same content hash by coincidence), silently breaking
// routing. The contract here: empty in-memory ⇒ no checkpoint in cluster.
func TestPartitionHashesPersistDeletesStaleCheckpointWhenMapEmpty(t *testing.T) {
	// Seed a reconciler that already has a checkpoint in the cluster from
	// a previous run with non-empty state.
	stale := &corev1.ConfigMap{}
	stale.Name = partitionHashesConfigMapName
	stale.Namespace = "test-ns"
	stale.Data = map[string]string{
		partitionHashesDataKey: `{"customrouter-routes-default-0":"3735928559"}`,
	}
	r := newReconciler(stale)
	// In-memory map is empty: simulating the post-clearTargetState state.
	r.partitionHashes = map[string]uint32{}

	if err := r.persistPartitionHashes(context.Background()); err != nil {
		t.Fatalf("persist with empty map: %v", err)
	}

	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{Name: partitionHashesConfigMapName, Namespace: "test-ns"}
	err := r.Get(context.Background(), key, cm)
	if err == nil {
		t.Errorf("stale checkpoint should have been deleted, found: %v", cm.Data)
	}
}

// TestPartitionHashesPersistMalformedRecoverable lets the controller boot
// even when the checkpoint was corrupted by some external actor (manual edit,
// upgrade from a future-incompatible format, etc.). Surfacing as an error
// would mean the operator refuses to start.
func TestPartitionHashesPersistMalformedRecoverable(t *testing.T) {
	bad := &corev1.ConfigMap{}
	bad.Name = partitionHashesConfigMapName
	bad.Namespace = "test-ns"
	bad.Data = map[string]string{partitionHashesDataKey: "{not valid json"}

	r := newReconciler(bad)
	if err := r.loadPartitionHashes(context.Background()); err != nil {
		t.Fatalf("malformed checkpoint should not propagate an error, got: %v", err)
	}
	if got := len(r.partitionHashes); got != 0 {
		t.Errorf("malformed checkpoint should leave the cache empty, got %d entries", got)
	}
}
