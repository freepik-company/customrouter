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
	"sort"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/freepik-company/customrouter/api/v1alpha1"
)

// sharedHost is the hostname every CHR in the determinism test contributes to,
// so their routes merge into one host's route slice.
const sharedHost = "shared.example.com"

// chrForHost builds a CustomHTTPRoute contributing a single route to sharedHost.
func chrForHost(ns, name, path string) *v1alpha1.CustomHTTPRoute {
	return &v1alpha1.CustomHTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID(ns + "-" + name)},
		Spec: v1alpha1.CustomHTTPRouteSpec{
			Hostnames: []string{sharedHost},
			TargetRef: v1alpha1.TargetRef{Name: "default"},
			Rules: []v1alpha1.Rule{
				{
					BackendRefs: []v1alpha1.BackendRef{{Name: "svc", Namespace: ns, Port: 80}},
					Matches:     []v1alpha1.PathMatch{{Path: path, Type: "Prefix"}},
				},
			},
		},
	}
}

// dumpTargetConfigMaps returns the concatenated routes.json of every ConfigMap
// for the target, ordered by ConfigMap name, so two rebuilds can be compared
// byte-for-byte.
func dumpTargetConfigMaps(t *testing.T, r *CustomHTTPRouteReconciler) string {
	t.Helper()
	list := &corev1.ConfigMapList{}
	if err := r.List(context.Background(), list); err != nil {
		t.Fatalf("listing ConfigMaps: %v", err)
	}
	names := make([]string, 0, len(list.Items))
	data := make(map[string]string, len(list.Items))
	for i := range list.Items {
		cm := &list.Items[i]
		if cm.Labels[configMapTargetLabel] != "default" {
			continue
		}
		names = append(names, cm.Name)
		data[cm.Name] = cm.Data[routesDataKey]
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteString("=")
		b.WriteString(data[n])
		b.WriteString("\n")
	}
	return b.String()
}

// TestRebuildConfigMapsForTarget_Deterministic is a regression guard for the
// ConfigMap-churn fix: rebuilding the same set of CustomHTTPRoutes must produce
// byte-identical ConfigMaps every time. Equal-priority routes from several CHRs
// merge into one host's slice; if their order is not pinned deterministically
// the serialized bytes differ between reconciles, the content-hash dedup misses,
// and every reconcile rewrites the ConfigMaps — the churn that forces extproc
// replicas to constantly reload. The controller pins this by sorting the routes
// by (namespace, name) before expansion.
func TestRebuildConfigMapsForTarget_Deterministic(t *testing.T) {
	// Equal-length, distinct paths so SortRoutes' priority/type/length keys are
	// all tied and the final order is decided purely by the (namespace, name)
	// ordering of the source CHRs that the controller pins.
	objs := []runtime.Object{
		chrForHost("ns-c", "route-1", "/c1"),
		chrForHost("ns-a", "route-2", "/a2"),
		chrForHost("ns-b", "route-1", "/b1"),
		chrForHost("ns-a", "route-1", "/a1"),
	}
	r := newReconciler(objs...)
	ctx := context.Background()

	if err := r.rebuildConfigMapsForTarget(ctx, "default"); err != nil {
		t.Fatalf("first rebuild failed: %v", err)
	}
	first := dumpTargetConfigMaps(t, r)

	for i := 0; i < 5; i++ {
		if err := r.rebuildConfigMapsForTarget(ctx, "default"); err != nil {
			t.Fatalf("rebuild %d failed: %v", i, err)
		}
		if got := dumpTargetConfigMaps(t, r); got != first {
			t.Fatalf("rebuild %d produced different ConfigMap bytes:\nfirst:\n%s\ngot:\n%s", i, first, got)
		}
	}

	// The merged routes for the shared host must be ordered by (namespace,name)
	// of their source CHR: ns-a/route-1, ns-a/route-2, ns-b/route-1, ns-c/route-1
	// -> paths /a1, /a2, /b1, /c1 — independent of the order the routes were listed.
	wantOrder := []string{`"path":"/a1"`, `"path":"/a2"`, `"path":"/b1"`, `"path":"/c1"`}
	prev := -1
	for _, marker := range wantOrder {
		idx := strings.Index(first, marker)
		if idx < 0 {
			t.Fatalf("expected %s in serialized routes:\n%s", marker, first)
		}
		if idx < prev {
			t.Fatalf("routes not ordered deterministically by source CHR; %s appears out of order:\n%s", marker, first)
		}
		prev = idx
	}
}
