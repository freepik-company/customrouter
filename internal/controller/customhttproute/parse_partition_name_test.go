package customhttproute

import (
	"context"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

func TestParsePartitionName(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantTarget string
		wantIndex  int
		wantOK     bool
	}{
		{"simple target index 0", "customrouter-routes-foo-0", "foo", 0, true},
		{"simple target larger index", "customrouter-routes-foo-12", "foo", 12, true},
		{"hyphenated target", "customrouter-routes-foo-bar-5", "foo-bar", 5, true},
		{"deeply hyphenated target", "customrouter-routes-a-b-c-d-99", "a-b-c-d", 99, true},

		{"missing prefix", "other-prefix-foo-0", "", 0, false},
		{"empty target", "customrouter-routes--0", "", 0, false},
		{"missing index", "customrouter-routes-foo", "", 0, false},
		{"trailing dash", "customrouter-routes-foo-", "", 0, false},
		{"non-numeric index", "customrouter-routes-foo-abc", "", 0, false},
		{"empty string", "", "", 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotTarget, gotIndex, gotOK := parsePartitionName(tc.input)
			if gotOK != tc.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if gotTarget != tc.wantTarget {
				t.Errorf("target = %q, want %q", gotTarget, tc.wantTarget)
			}
			if gotIndex != tc.wantIndex {
				t.Errorf("index = %d, want %d", gotIndex, tc.wantIndex)
			}
		})
	}
}

// TestClearTargetStateNoPrefixCollision verifies that clearing target "foo"
// does not evict cached partition hashes that genuinely belong to a different
// target whose name happens to share the "foo-" prefix (e.g. "foo-bar").
// Prior to M-2 the implementation used naive prefix matching which would
// incorrectly drop neighbour-target entries.
func TestClearTargetStateNoPrefixCollision(t *testing.T) {
	r := &CustomHTTPRouteReconciler{
		rebuildMu:         sync.Mutex{},
		lastRebuildAt:     map[string]time.Time{"foo": time.Now(), "foo-bar": time.Now()},
		partitionHashesMu: sync.Mutex{},
		partitionHashes: map[string]uint32{
			"customrouter-routes-foo-0":     1,
			"customrouter-routes-foo-1":     2,
			"customrouter-routes-foo-bar-0": 3,
			"customrouter-routes-foo-bar-7": 4,
			"customrouter-routes-other-0":   5,
		},
	}

	r.clearTargetState("foo")

	if _, ok := r.lastRebuildAt["foo"]; ok {
		t.Errorf("lastRebuildAt[foo] should have been cleared")
	}
	if _, ok := r.lastRebuildAt["foo-bar"]; !ok {
		t.Errorf("lastRebuildAt[foo-bar] should have been preserved")
	}

	wantPresent := []string{
		"customrouter-routes-foo-bar-0",
		"customrouter-routes-foo-bar-7",
		"customrouter-routes-other-0",
	}
	wantAbsent := []string{
		"customrouter-routes-foo-0",
		"customrouter-routes-foo-1",
	}
	for _, k := range wantPresent {
		if _, ok := r.partitionHashes[k]; !ok {
			t.Errorf("partitionHashes[%q] should have been preserved", k)
		}
	}
	for _, k := range wantAbsent {
		if _, ok := r.partitionHashes[k]; ok {
			t.Errorf("partitionHashes[%q] should have been cleared", k)
		}
	}
}

// TestGCStateOnceEvictsDeadTargets verifies that the periodic state GC
// removes lastRebuildAt and partitionHashes entries whose target no longer
// has any live CustomHTTPRoute, while preserving entries for live targets.
func TestGCStateOnceEvictsDeadTargets(t *testing.T) {
	// Only "alive" target has a live CustomHTTPRoute; "dead" target's CR
	// was already deleted but its in-memory state was leaked.
	aliveCR := &v1alpha1.CustomHTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "r1", Namespace: "default"},
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "alive"},
		},
	}
	r := newReconciler(runtime.Object(aliveCR))
	r.lastRebuildAt = map[string]time.Time{
		"alive": time.Now(),
		"dead":  time.Now(),
	}
	r.partitionHashes = map[string]uint32{
		"customrouter-routes-alive-0":   1,
		"customrouter-routes-alive-2":   2,
		"customrouter-routes-dead-0":    3,
		"customrouter-routes-dead-7":    4,
		"unparseable-name":              5, // defensive eviction
	}
	r.rebuildMu = sync.Mutex{}
	r.partitionHashesMu = sync.Mutex{}

	if err := r.gcStateOnce(context.Background()); err != nil {
		t.Fatalf("gcStateOnce returned error: %v", err)
	}

	if _, ok := r.lastRebuildAt["alive"]; !ok {
		t.Errorf("lastRebuildAt[alive] should have been preserved")
	}
	if _, ok := r.lastRebuildAt["dead"]; ok {
		t.Errorf("lastRebuildAt[dead] should have been evicted")
	}

	wantPresent := []string{
		"customrouter-routes-alive-0",
		"customrouter-routes-alive-2",
	}
	wantAbsent := []string{
		"customrouter-routes-dead-0",
		"customrouter-routes-dead-7",
		"unparseable-name",
	}
	for _, k := range wantPresent {
		if _, ok := r.partitionHashes[k]; !ok {
			t.Errorf("partitionHashes[%q] should have been preserved", k)
		}
	}
	for _, k := range wantAbsent {
		if _, ok := r.partitionHashes[k]; ok {
			t.Errorf("partitionHashes[%q] should have been evicted", k)
		}
	}
}
