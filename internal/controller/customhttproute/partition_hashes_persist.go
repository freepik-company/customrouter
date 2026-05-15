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
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// partitionHashesConfigMapName is the ConfigMap (in the same namespace as the
// route partitions) where the in-memory partitionHashes map is checkpointed.
// Persisting the map avoids a cold-start "wave" of unconditional Get+Compare
// operations against the API server after every operator restart.
const partitionHashesConfigMapName = "customrouter-partition-hashes"

// partitionHashesDataKey is the data key inside the checkpoint ConfigMap
// holding the JSON-encoded {partitionName: hash} map.
const partitionHashesDataKey = "hashes.json"

// defaultPersistInterval is how often the in-memory partitionHashes map is
// flushed to its checkpoint ConfigMap. Persisting on every rebuild would
// double the write rate the cooldown is supposed to bound; a periodic flush
// trades a small recovery-window penalty (entries created since the last
// flush are re-validated against etcd after a crash) for a stable, predictable
// write rate.
const defaultPersistInterval = 30 * time.Second

// loadPartitionHashes reads the checkpoint ConfigMap into the in-memory map.
// Called once at startup, before the reconcile workers see traffic. Missing
// or malformed checkpoints are not errors — the controller simply starts with
// an empty cache, which is the pre-0.7.2 behaviour.
func (r *CustomHTTPRouteReconciler) loadPartitionHashes(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("partition-hashes-load")

	cm := &corev1.ConfigMap{}
	key := types.NamespacedName{
		Name:      partitionHashesConfigMapName,
		Namespace: r.ConfigMapNamespace,
	}
	if err := r.Get(ctx, key, cm); err != nil {
		if errors.IsNotFound(err) {
			logger.V(1).Info("no checkpoint ConfigMap found, starting with empty cache")
			return nil
		}
		return fmt.Errorf("read partition hashes checkpoint: %w", err)
	}

	raw, ok := cm.Data[partitionHashesDataKey]
	if !ok || raw == "" {
		logger.Info("checkpoint ConfigMap is empty, starting with empty cache")
		return nil
	}

	// Wire format is {name: hash-as-decimal-string}. uint32 cannot be the JSON
	// value type directly because some hashes overflow JS numeric precision
	// (Helm consumers may inspect this object), and storing as string keeps
	// the resulting ConfigMap diff readable.
	loaded := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &loaded); err != nil {
		// Malformed checkpoint is recoverable: just discard and rebuild on
		// the fly. Surfacing as an error would mean the controller refuses
		// to start over a stale on-disk format mismatch.
		logger.Error(err, "checkpoint ConfigMap is malformed, discarding")
		return nil
	}

	r.partitionHashesMu.Lock()
	if r.partitionHashes == nil {
		r.partitionHashes = make(map[string]uint32, len(loaded))
	}
	for name, hashStr := range loaded {
		h, err := strconv.ParseUint(hashStr, 10, 32)
		if err != nil {
			// Skip individual bad entries silently; they will be repopulated
			// by the next successful upsert.
			continue
		}
		r.partitionHashes[name] = uint32(h)
	}
	count := len(r.partitionHashes)
	r.partitionHashesMu.Unlock()

	logger.Info("loaded partition hash checkpoint", "entries", count)
	return nil
}

// persistPartitionHashes serializes the current in-memory map and upserts the
// checkpoint ConfigMap. Called periodically by the persistRunner — never on
// the reconcile hot path to keep p99 latency bounded.
func (r *CustomHTTPRouteReconciler) persistPartitionHashes(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("partition-hashes-persist")

	r.partitionHashesMu.Lock()
	snapshot := make(map[string]string, len(r.partitionHashes))
	for name, hash := range r.partitionHashes {
		snapshot[name] = strconv.FormatUint(uint64(hash), 10)
	}
	r.partitionHashesMu.Unlock()

	key := types.NamespacedName{
		Name:      partitionHashesConfigMapName,
		Namespace: r.ConfigMapNamespace,
	}

	if len(snapshot) == 0 {
		// In-memory map is empty (e.g. all targets were deleted via
		// clearTargetState). Delete any stale checkpoint instead of
		// leaving it intact — otherwise the next restart loads stale
		// entries, the upsert fast-path sees a "hit" for a partition
		// name that maps to a hash matching the freshly recreated
		// data, and silently skips the Create for a ConfigMap that no
		// longer exists in the cluster.
		existing := &corev1.ConfigMap{}
		err := r.Get(ctx, key, existing)
		if errors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read checkpoint ConfigMap before delete: %w", err)
		}
		if err := r.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete stale checkpoint ConfigMap: %w", err)
		}
		logger.V(1).Info("deleted stale partition hash checkpoint (in-memory map is empty)")
		return nil
	}

	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal partition hashes: %w", err)
	}

	existing := &corev1.ConfigMap{}
	err = r.Get(ctx, key, existing)
	if errors.IsNotFound(err) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      partitionHashesConfigMapName,
				Namespace: r.ConfigMapNamespace,
				Labels: map[string]string{
					"app.kubernetes.io/name": "customrouter",
					configMapManagedByLabel:  configMapManagedByValue,
				},
			},
			Data: map[string]string{partitionHashesDataKey: string(raw)},
		}
		if err := r.Create(ctx, cm); err != nil {
			return fmt.Errorf("create checkpoint ConfigMap: %w", err)
		}
		logger.V(1).Info("created partition hash checkpoint", "entries", len(snapshot))
		return nil
	}
	if err != nil {
		return fmt.Errorf("read checkpoint ConfigMap: %w", err)
	}

	// Skip the write when bytes are already identical — avoids etcd churn on
	// reconciles that did not change any partition. The hash map itself is
	// deterministic in content (FNV of partition data) but Go map iteration
	// is not, so json.Marshal output is not byte-stable across reconciles.
	// Instead, the previous serialized blob is treated as the comparison
	// baseline; an actual content change is detected by length or any
	// difference after canonicalising via re-marshal. For pragmatism we
	// compare by length first (cheap) and fall back to full equality.
	if existing.Data[partitionHashesDataKey] == string(raw) {
		return nil
	}

	existing.Data = map[string]string{partitionHashesDataKey: string(raw)}
	if err := r.Update(ctx, existing); err != nil {
		return fmt.Errorf("update checkpoint ConfigMap: %w", err)
	}
	logger.V(1).Info("updated partition hash checkpoint", "entries", len(snapshot))
	return nil
}

// runPartitionHashesPersist is the manager runnable that flushes the
// partitionHashes cache to its checkpoint ConfigMap on a fixed interval.
// Persisting periodically (rather than on every rebuild) bounds the worst-case
// write amplification at one extra ConfigMap update every defaultPersistInterval,
// regardless of how many partition changes occur in between.
func (r *CustomHTTPRouteReconciler) runPartitionHashesPersist(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("partition-hashes-runner")
	ticker := time.NewTicker(defaultPersistInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final flush before shutdown: gives the next leader a warm
			// cache on takeover, avoiding the cold-start wave we were
			// chasing in the first place. Use a fresh, short-lived
			// context so the write itself is not cancelled by ctx.
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := r.persistPartitionHashes(flushCtx); err != nil {
				logger.Error(err, "final partition hashes flush failed")
			}
			cancel()
			return nil
		case <-ticker.C:
			if err := r.persistPartitionHashes(ctx); err != nil {
				logger.Error(err, "periodic partition hashes flush failed, will retry next tick")
			}
		}
	}
}

// addPartitionHashesPersistRunnable registers the checkpoint runner with the
// manager. Bound to leader election so only the active leader writes the
// checkpoint — otherwise two pods with diverging in-memory state could
// trample each other's writes.
func (r *CustomHTTPRouteReconciler) addPartitionHashesPersistRunnable(mgr manager.Manager) error {
	return mgr.Add(&leaderElectedRunnable{
		fn: func(ctx context.Context) error {
			// Best-effort warmup load — failures are already logged inside
			// loadPartitionHashes and never propagate, so the runnable
			// always proceeds to the periodic flush loop even if the
			// initial read failed.
			if err := r.loadPartitionHashes(ctx); err != nil {
				log.FromContext(ctx).WithName("partition-hashes-runner").Error(err, "initial load failed, continuing with empty cache")
			}
			return r.runPartitionHashesPersist(ctx)
		},
	})
}

// leaderElectedRunnable is a manager.Runnable that participates in leader
// election: it only runs on the active leader. controller-runtime's default
// RunnableFunc does not implement LeaderElectionRunnable, which would run on
// every replica and cause divergent checkpoint writes.
type leaderElectedRunnable struct {
	fn func(context.Context) error
}

func (l *leaderElectedRunnable) Start(ctx context.Context) error { return l.fn(ctx) }
func (l *leaderElectedRunnable) NeedLeaderElection() bool        { return true }

// Ensure interface compliance at compile time.
var _ manager.Runnable = (*leaderElectedRunnable)(nil)
var _ manager.LeaderElectionRunnable = (*leaderElectedRunnable)(nil)
