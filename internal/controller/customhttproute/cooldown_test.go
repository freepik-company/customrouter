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
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRebuildWait_NoPriorRebuild(t *testing.T) {
	r := &CustomHTTPRouteReconciler{RebuildCooldown: 500 * time.Millisecond}

	wait, throttled := r.rebuildWait("target-a", time.Now())
	if throttled {
		t.Fatalf("expected no throttling with no prior rebuild, got wait=%s", wait)
	}
}

func TestRebuildWait_WithinCooldown(t *testing.T) {
	r := &CustomHTTPRouteReconciler{RebuildCooldown: 500 * time.Millisecond}

	base := time.Now()
	r.markRebuilt("target-a", base)

	wait, throttled := r.rebuildWait("target-a", base.Add(100*time.Millisecond))
	if !throttled {
		t.Fatalf("expected throttling 100ms into a 500ms cooldown")
	}
	want := 400 * time.Millisecond
	if wait != want {
		t.Errorf("wait = %s, want %s", wait, want)
	}
}

func TestRebuildWait_AfterCooldown(t *testing.T) {
	r := &CustomHTTPRouteReconciler{RebuildCooldown: 500 * time.Millisecond}

	base := time.Now()
	r.markRebuilt("target-a", base)

	wait, throttled := r.rebuildWait("target-a", base.Add(600*time.Millisecond))
	if throttled {
		t.Fatalf("expected no throttling 600ms into a 500ms cooldown, got wait=%s", wait)
	}
}

func TestRebuildWait_DistinctTargetsAreIndependent(t *testing.T) {
	r := &CustomHTTPRouteReconciler{RebuildCooldown: 500 * time.Millisecond}

	base := time.Now()
	r.markRebuilt("target-a", base)

	// target-b never rebuilt → must not be throttled.
	if _, throttled := r.rebuildWait("target-b", base); throttled {
		t.Fatalf("target-b should not inherit target-a cooldown")
	}
}

func TestRebuildWait_DefaultsToDefaultCooldown(t *testing.T) {
	// RebuildCooldown left at zero, exercising the fallback.
	r := &CustomHTTPRouteReconciler{}

	base := time.Now()
	r.markRebuilt("target-a", base)

	wait, throttled := r.rebuildWait("target-a", base.Add(10*time.Millisecond))
	if !throttled {
		t.Fatalf("expected throttling under the default cooldown")
	}
	want := DefaultRebuildCooldown - 10*time.Millisecond
	if wait != want {
		t.Errorf("wait = %s, want %s", wait, want)
	}
}

func TestRebuildWait_ZeroCooldownDisablesThrottling(t *testing.T) {
	// Negative cooldown disables the gate entirely (useful for tests of the
	// rebuild path itself).
	r := &CustomHTTPRouteReconciler{RebuildCooldown: -1}

	base := time.Now()
	r.markRebuilt("target-a", base)

	if _, throttled := r.rebuildWait("target-a", base); throttled {
		t.Fatalf("negative cooldown should disable throttling")
	}
}

func TestMarkRebuilt_LastWriteWins(t *testing.T) {
	r := &CustomHTTPRouteReconciler{RebuildCooldown: 500 * time.Millisecond}

	t0 := time.Now()
	r.markRebuilt("target-a", t0)
	r.markRebuilt("target-a", t0.Add(200*time.Millisecond))

	// Cooldown should be measured from the latest mark.
	wait, throttled := r.rebuildWait("target-a", t0.Add(250*time.Millisecond))
	if !throttled {
		t.Fatalf("expected throttling 50ms after latest mark")
	}
	want := 450 * time.Millisecond
	if wait != want {
		t.Errorf("wait = %s, want %s (measured from latest mark)", wait, want)
	}
}

// TestRebuildWait_ConcurrentAccessSafe asserts no data race when many
// goroutines query and mark the same target concurrently.
func TestRebuildWait_ConcurrentAccessSafe(t *testing.T) {
	r := &CustomHTTPRouteReconciler{RebuildCooldown: 10 * time.Millisecond}

	const goroutines = 64
	const ops = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				now := time.Now()
				_, _ = r.rebuildWait("target-a", now)
				r.markRebuilt("target-a", now)
			}
		}()
	}
	wg.Wait()
}

// TestRebuildWait_ThrottlesBurst simulates a cache resync where many
// reconciles for the same target fire in parallel. Only the first sees an
// empty cooldown; the rest are throttled until the cooldown expires.
func TestRebuildWait_ThrottlesBurst(t *testing.T) {
	r := &CustomHTTPRouteReconciler{RebuildCooldown: 200 * time.Millisecond}

	const callers = 50
	var allowed, throttled int32

	now := time.Now()
	// First caller wins immediately and records the rebuild.
	if _, t1 := r.rebuildWait("target-a", now); t1 {
		t.Fatalf("first caller must not be throttled")
	}
	r.markRebuilt("target-a", now)

	// Remaining callers within the cooldown window must all be throttled.
	for i := 0; i < callers; i++ {
		if _, t1 := r.rebuildWait("target-a", now.Add(time.Duration(i)*time.Millisecond)); t1 {
			atomic.AddInt32(&throttled, 1)
		} else {
			atomic.AddInt32(&allowed, 1)
		}
	}

	if got := atomic.LoadInt32(&throttled); got != callers {
		t.Errorf("throttled = %d, want %d", got, callers)
	}
	if got := atomic.LoadInt32(&allowed); got != 0 {
		t.Errorf("allowed = %d, want 0 within cooldown window", got)
	}
}
