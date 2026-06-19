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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

func routeForTarget(target, path string) *v1alpha1.CustomHTTPRoute {
	return &v1alpha1.CustomHTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "r-" + path[1:], Namespace: "ns", UID: types.UID(target + path)},
		Spec: v1alpha1.CustomHTTPRouteSpec{
			Hostnames: []string{"a.example.com"},
			TargetRef: v1alpha1.TargetRef{Name: target},
			Rules: []v1alpha1.Rule{{
				BackendRefs: []v1alpha1.BackendRef{{Name: "svc", Namespace: "ns", Port: 80}},
				Matches:     []v1alpha1.PathMatch{{Path: path, Type: "Exact"}},
			}},
		},
	}
}

// TestTargetTryLock verifies the per-target single-flight lock.
func TestTargetTryLock(t *testing.T) {
	r := &CustomHTTPRouteReconciler{}
	if !r.targetTryLock("t") {
		t.Fatal("first lock should succeed")
	}
	if r.targetTryLock("t") {
		t.Fatal("second lock should fail while held")
	}
	if !r.targetTryLock("b") {
		t.Fatal("distinct target must not be blocked")
	}
	r.targetUnlock("t")
	if !r.targetTryLock("t") {
		t.Fatal("lock should be available after unlock")
	}
	r.targetUnlock("t")
	r.targetUnlock("b")
}

// TestClearTargetStateKeepsLock is the regression for the empty-target path:
// clearTargetState runs inside rebuildConfigMapsForTarget while rebuildTarget
// holds the per-target lock, so it must NOT release it (doing so would let a
// concurrent reconcile start a parallel rebuild).
func TestClearTargetStateKeepsLock(t *testing.T) {
	r := &CustomHTTPRouteReconciler{}
	if !r.targetTryLock("t") {
		t.Fatal("setup: should acquire lock")
	}
	r.clearTargetState("t")
	if r.targetTryLock("t") {
		t.Fatal("clearTargetState must not release the per-target rebuild lock")
	}
	r.targetUnlock("t")
}

// TestRebuildTargetDefers checks rebuildTarget returns a positive wait (and does
// not rebuild) when the target is in cooldown or its lock is held.
func TestRebuildTargetDefers(t *testing.T) {
	r := newReconciler(routeForTarget("default", "/a"))
	ctx := context.Background()

	if wait, err := r.rebuildTarget(ctx, "default", false); err != nil || wait != 0 {
		t.Fatalf("first rebuild should perform: wait=%v err=%v", wait, err)
	}
	// Within the cooldown window the next rebuild must defer.
	if wait, err := r.rebuildTarget(ctx, "default", false); err != nil || wait <= 0 {
		t.Fatalf("rebuild within cooldown should defer: wait=%v err=%v", wait, err)
	}

	// With the lock held by someone else, defer regardless of cooldown.
	r2 := newReconciler(routeForTarget("default", "/a"))
	r2.RebuildCooldown = -1 // disable cooldown to isolate the lock behaviour
	if !r2.targetTryLock("default") {
		t.Fatal("setup: should hold the lock")
	}
	if wait, err := r2.rebuildTarget(ctx, "default", false); err != nil || wait <= 0 {
		t.Fatalf("rebuild while lock held should defer: wait=%v err=%v", wait, err)
	}
	r2.targetUnlock("default")
}

// TestRebuildTargetNoConcurrentRebuilds runs many goroutines that each retry
// rebuildTarget until it performs, with the cooldown disabled so only the lock
// gates concurrency. The actual rebuild work must never overlap.
func TestRebuildTargetNoConcurrentRebuilds(t *testing.T) {
	route := routeForTarget("default", "/a")

	var inFlight, maxInFlight int32
	scheme := newScheme()
	cb := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(route).
		WithIndex(&v1alpha1.CustomHTTPRoute{}, targetRefIndexField, func(obj client.Object) []string {
			return []string{obj.(*v1alpha1.CustomHTTPRoute).Spec.TargetRef.Name}
		}).
		WithInterceptorFuncs(interceptor.Funcs{
			// rebuildConfigMapsForTarget lists CustomHTTPRoutes first; this runs
			// inside the per-target lock, so widen it to expose any overlap.
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
	r := &CustomHTTPRouteReconciler{
		Client:             cb.Build(),
		Scheme:             scheme,
		ConfigMapNamespace: "test-ns",
		RebuildCooldown:    -1, // disabled: contention is gated only by the lock
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				wait, err := r.rebuildTarget(context.Background(), "default", false)
				if err != nil {
					t.Errorf("rebuildTarget: %v", err)
					return
				}
				if wait == 0 {
					return // performed
				}
				// Lock was held; retry until we get our turn.
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxInFlight); got != 1 {
		t.Fatalf("rebuilds ran concurrently: max in-flight = %d, want 1", got)
	}
	cm := &corev1.ConfigMap{}
	if err := r.Get(context.Background(), client.ObjectKey{Name: "customrouter-routes-default-0", Namespace: "test-ns"}, cm); err != nil {
		t.Fatalf("expected ConfigMap to be built: %v", err)
	}
}
