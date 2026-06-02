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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/internal/controller"
)

// crForDrain constructs a CustomHTTPRoute with the parameters needed by drain
// tests. When deletionTime is non-nil the route is marked as in deletion.
func crForDrain(name, target string, uid types.UID, deletionTime *time.Time, withFinalizer bool) *v1alpha1.CustomHTTPRoute {
	cr := &v1alpha1.CustomHTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       uid,
		},
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: target},
		},
	}
	if deletionTime != nil {
		t := metav1.NewTime(*deletionTime)
		cr.DeletionTimestamp = &t
	}
	if withFinalizer {
		cr.Finalizers = []string{controller.ResourceFinalizer}
	}
	return cr
}

func TestDrainSiblingsForDeletion_RemovesFinalizers(t *testing.T) {
	now := time.Now()
	self := crForDrain("self", "default", "uid-self", &now, true)
	sibA := crForDrain("sib-a", "default", "uid-a", &now, true)
	sibB := crForDrain("sib-b", "default", "uid-b", &now, true)
	live := crForDrain("alive", "default", "uid-live", nil, true)

	r := newReconciler(self, sibA, sibB, live)
	r.drainSiblingsForDeletion(context.Background(), "default", self.UID)

	if got := fetchFinalizers(t, r, "sib-a"); len(got) != 0 {
		t.Errorf("sib-a finalizers = %v, want empty", got)
	}
	if got := fetchFinalizers(t, r, "sib-b"); len(got) != 0 {
		t.Errorf("sib-b finalizers = %v, want empty", got)
	}
	if got := fetchFinalizers(t, r, "self"); len(got) == 0 {
		t.Errorf("self finalizer should be untouched (Reconcile removes it later), got empty")
	}
	if got := fetchFinalizers(t, r, "alive"); len(got) == 0 {
		t.Errorf("live CR finalizer should be untouched, got empty")
	}
}

func TestDrainSiblingsForDeletion_SkipsOtherTargets(t *testing.T) {
	now := time.Now()
	self := crForDrain("self", "default", "uid-self", &now, true)
	sibSameTarget := crForDrain("sib-default", "default", "uid-same", &now, true)
	sibOtherTarget := crForDrain("sib-other", "other", "uid-other", &now, true)

	r := newReconciler(self, sibSameTarget, sibOtherTarget)
	r.drainSiblingsForDeletion(context.Background(), "default", self.UID)

	if got := fetchFinalizers(t, r, "sib-default"); len(got) != 0 {
		t.Errorf("same-target sibling should be drained, got finalizers=%v", got)
	}
	if got := fetchFinalizers(t, r, "sib-other"); len(got) == 0 {
		t.Errorf("other-target sibling should NOT be drained, got empty finalizers")
	}
}

func TestDrainSiblingsForDeletion_SkipsLiveSiblings(t *testing.T) {
	now := time.Now()
	self := crForDrain("self", "default", "uid-self", &now, true)
	liveSibling := crForDrain("live", "default", "uid-live", nil, true)

	r := newReconciler(self, liveSibling)
	r.drainSiblingsForDeletion(context.Background(), "default", self.UID)

	if got := fetchFinalizers(t, r, "live"); len(got) == 0 {
		t.Errorf("live sibling should NOT be drained, got empty finalizers")
	}
}

func TestDrainSiblingsForDeletion_SkipsSiblingsWithoutOurFinalizer(t *testing.T) {
	now := time.Now()
	self := crForDrain("self", "default", "uid-self", &now, true)
	// A CR in deletion that holds only a third-party finalizer (not ours).
	// drain must leave it untouched.
	sibThirdParty := crForDrain("sib-third", "default", "uid-third", &now, false)
	sibThirdParty.Finalizers = []string{"third-party/finalizer"}

	r := newReconciler(self, sibThirdParty)
	r.drainSiblingsForDeletion(context.Background(), "default", self.UID)

	got := fetchFinalizers(t, r, "sib-third")
	if len(got) != 1 || got[0] != "third-party/finalizer" {
		t.Errorf("third-party finalizer should be untouched, got %v", got)
	}
}

func TestDrainSiblingsForDeletion_PreservesOtherFinalizers(t *testing.T) {
	now := time.Now()
	self := crForDrain("self", "default", "uid-self", &now, true)
	sib := crForDrain("sib", "default", "uid-sib", &now, true)
	sib.Finalizers = append(sib.Finalizers, "third-party/finalizer")

	r := newReconciler(self, sib)
	r.drainSiblingsForDeletion(context.Background(), "default", self.UID)

	got := fetchFinalizers(t, r, "sib")
	if len(got) != 1 || got[0] != "third-party/finalizer" {
		t.Errorf("expected only third-party finalizer to remain, got %v", got)
	}
}

func TestDrainSiblingsForDeletion_IsIdempotent(t *testing.T) {
	now := time.Now()
	self := crForDrain("self", "default", "uid-self", &now, true)
	sib := crForDrain("sib", "default", "uid-sib", &now, true)

	r := newReconciler(self, sib)
	ctx := context.Background()
	r.drainSiblingsForDeletion(ctx, "default", self.UID)
	// Second call: sibling is gone (fake client GCs after finalizers cleared).
	// Should be a clean no-op without errors.
	r.drainSiblingsForDeletion(ctx, "default", self.UID)
}

// fetchFinalizers reads the CR from the client and returns its finalizer list.
// When the object has been garbage-collected (no finalizers left), the fake
// client may either return the object with empty finalizers or NotFound; both
// shapes are normalised to nil.
func fetchFinalizers(t *testing.T, r *CustomHTTPRouteReconciler, name string) []string {
	t.Helper()
	cr := &v1alpha1.CustomHTTPRoute{}
	err := r.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: name}, cr)
	if err != nil {
		// Treat NotFound as "all finalizers gone".
		return nil
	}
	return cr.Finalizers
}

// Compile-time guard against unused runtime import drift.
var _ runtime.Object = &v1alpha1.CustomHTTPRoute{}
