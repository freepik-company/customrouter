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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

// TestRebuildSingleFlightPrimitives verifies the begin/finish/release state
// machine that drives per-target rebuild coalescing.
func TestRebuildSingleFlightPrimitives(t *testing.T) {
	r := &CustomHTTPRouteReconciler{}

	if !r.tryBeginRebuild("t") {
		t.Fatal("first tryBeginRebuild should take ownership")
	}
	// While in progress, another caller must not take ownership and must record pending.
	if r.tryBeginRebuild("t") {
		t.Fatal("second tryBeginRebuild should not take ownership while in progress")
	}
	// Owner sees the pending flag and is told to loop once more.
	if !r.finishOrContinueRebuild("t") {
		t.Fatal("finishOrContinueRebuild should report pending → continue")
	}
	// Nothing pending now → ownership released.
	if r.finishOrContinueRebuild("t") {
		t.Fatal("finishOrContinueRebuild should report no pending → release")
	}
	// Released → ownership available again.
	if !r.tryBeginRebuild("t") {
		t.Fatal("after release, tryBeginRebuild should take ownership again")
	}
	// releaseRebuild (error path) must free ownership.
	r.releaseRebuild("t")
	if !r.tryBeginRebuild("t") {
		t.Fatal("after releaseRebuild, tryBeginRebuild should take ownership")
	}
	r.releaseRebuild("t")

	// Different targets are independent.
	if !r.tryBeginRebuild("a") || !r.tryBeginRebuild("b") {
		t.Fatal("distinct targets must not block each other")
	}
}

// TestClearTargetStateKeepsSingleFlightOwnership is the regression for the
// empty-target path: clearTargetState runs inside rebuildConfigMapsForTarget
// while coalescedRebuildForTarget still holds ownership, so it must NOT release
// the single-flight flags (doing so would let a concurrent reconcile start a
// parallel rebuild).
func TestClearTargetStateKeepsSingleFlightOwnership(t *testing.T) {
	r := &CustomHTTPRouteReconciler{}

	if !r.tryBeginRebuild("t") {
		t.Fatal("setup: should take ownership")
	}
	// Simulate the empty-target cleanup running mid-rebuild.
	r.clearTargetState("t")
	// Ownership must still be held: a concurrent reconcile must not be able to
	// begin a parallel rebuild (this call returns false and records pending).
	if r.tryBeginRebuild("t") {
		t.Fatal("clearTargetState must not release single-flight ownership mid-rebuild")
	}
	// That blocked attempt set pending, so the owner loops once...
	if !r.finishOrContinueRebuild("t") {
		t.Fatal("expected pending (from the blocked begin) → continue")
	}
	// ...then releases cleanly.
	if r.finishOrContinueRebuild("t") {
		t.Fatal("expected no pending → release")
	}
	if !r.tryBeginRebuild("t") {
		t.Fatal("after the owner finished, ownership should be available again")
	}
	r.releaseRebuild("t")
}

// TestCoalescedRebuildNoConcurrentRebuilds runs many concurrent
// coalescedRebuildForTarget calls for the same target and asserts the actual
// rebuild work never runs concurrently (which would multiply operator memory).
func TestCoalescedRebuildNoConcurrentRebuilds(t *testing.T) {
	route := &v1alpha1.CustomHTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "ns", UID: "uid"},
		Spec: v1alpha1.CustomHTTPRouteSpec{
			Hostnames: []string{"a.example.com"},
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Rules: []v1alpha1.Rule{{
				BackendRefs: []v1alpha1.BackendRef{{Name: "svc", Namespace: "ns", Port: 80}},
				Matches:     []v1alpha1.PathMatch{{Path: "/a", Type: "Exact"}},
			}},
		},
	}

	var inFlight, maxInFlight int32
	scheme := newScheme()
	cb := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(route).
		WithIndex(&v1alpha1.CustomHTTPRoute{}, targetRefIndexField, func(obj client.Object) []string {
			return []string{obj.(*v1alpha1.CustomHTTPRoute).Spec.TargetRef.Name}
		}).
		WithInterceptorFuncs(interceptor.Funcs{
			// rebuildConfigMapsForTarget begins by listing CustomHTTPRoutes;
			// widen that window so any overlap between rebuilds is observed.
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*v1alpha1.CustomHTTPRouteList); ok {
					n := atomic.AddInt32(&inFlight, 1)
					for {
						m := atomic.LoadInt32(&maxInFlight)
						if n <= m || atomic.CompareAndSwapInt32(&maxInFlight, m, n) {
							break
						}
					}
					time.Sleep(10 * time.Millisecond)
					atomic.AddInt32(&inFlight, -1)
				}
				return c.List(ctx, list, opts...)
			},
		})
	r := &CustomHTTPRouteReconciler{Client: cb.Build(), Scheme: scheme, ConfigMapNamespace: "test-ns"}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.coalescedRebuildForTarget(context.Background(), "default"); err != nil {
				t.Errorf("coalescedRebuildForTarget: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxInFlight); got != 1 {
		t.Fatalf("rebuilds ran concurrently: max in-flight = %d, want 1", got)
	}

	// The route's ConfigMap must still have been produced.
	cm := &corev1.ConfigMap{}
	if err := r.Get(context.Background(), client.ObjectKey{Name: "customrouter-routes-default-0", Namespace: "test-ns"}, cm); err != nil {
		t.Fatalf("expected ConfigMap to be built: %v", err)
	}
}
