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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/pkg/routes"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func newReconciler(objs ...runtime.Object) *CustomHTTPRouteReconciler {
	scheme := newScheme()
	clientObjs := make([]runtime.Object, len(objs))
	copy(clientObjs, objs)
	cb := fake.NewClientBuilder().WithScheme(scheme)
	for _, obj := range clientObjs {
		cb = cb.WithRuntimeObjects(obj)
	}
	cb = cb.WithIndex(&v1alpha1.CustomHTTPRoute{}, targetRefIndexField, func(obj client.Object) []string {
		route := obj.(*v1alpha1.CustomHTTPRoute)
		return []string{route.Spec.TargetRef.Name}
	})
	return &CustomHTTPRouteReconciler{
		Client:             cb.Build(),
		Scheme:             scheme,
		ConfigMapNamespace: "test-ns",
	}
}

func TestMapsEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b map[string]string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"both empty", map[string]string{}, map[string]string{}, true},
		{"equal", map[string]string{"a": "1", "b": "2"}, map[string]string{"a": "1", "b": "2"}, true},
		{"different values", map[string]string{"a": "1"}, map[string]string{"a": "2"}, false},
		{"different keys", map[string]string{"a": "1"}, map[string]string{"b": "1"}, false},
		{"different lengths", map[string]string{"a": "1"}, map[string]string{"a": "1", "b": "2"}, false},
		{"nil vs empty", nil, map[string]string{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mapsEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("mapsEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPartitionConfig_SinglePartition(t *testing.T) {
	r := &CustomHTTPRouteReconciler{ConfigMapNamespace: "ns"}
	config := &routes.RoutesConfig{
		Version: 1,
		Hosts: map[string][]routes.Route{
			"example.com": {{Path: "/api", Type: "prefix", Backend: "svc.ns.svc.cluster.local:80"}},
		},
	}

	partitions, err := r.partitionConfig("default", config)
	if err != nil {
		t.Fatalf("partitionConfig returned error: %v", err)
	}
	if len(partitions) != 1 {
		t.Fatalf("expected 1 partition, got %d", len(partitions))
	}
	if partitions[0].Name != "customrouter-routes-default-0" {
		t.Errorf("expected name customrouter-routes-default-0, got %s", partitions[0].Name)
	}
	if partitions[0].Target != "default" {
		t.Errorf("expected target default, got %s", partitions[0].Target)
	}
}

func TestRebuildConfigMapsForTarget_OnlyAffectsOwnTarget(t *testing.T) {
	route1 := &v1alpha1.CustomHTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route-a", Namespace: "ns", UID: "uid-a"},
		Spec: v1alpha1.CustomHTTPRouteSpec{
			Hostnames: []string{"a.example.com"},
			TargetRef: v1alpha1.TargetRef{Name: "target-a"},
			Rules: []v1alpha1.Rule{
				{
					BackendRefs: []v1alpha1.BackendRef{{Name: "svc", Namespace: "ns", Port: 80}},
					Matches:     []v1alpha1.PathMatch{{Path: "/a", Type: "Exact"}},
				},
			},
		},
	}
	route2 := &v1alpha1.CustomHTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route-b", Namespace: "ns", UID: "uid-b"},
		Spec: v1alpha1.CustomHTTPRouteSpec{
			Hostnames: []string{"b.example.com"},
			TargetRef: v1alpha1.TargetRef{Name: "target-b"},
			Rules: []v1alpha1.Rule{
				{
					BackendRefs: []v1alpha1.BackendRef{{Name: "svc", Namespace: "ns", Port: 80}},
					Matches:     []v1alpha1.PathMatch{{Path: "/b", Type: "Exact"}},
				},
			},
		},
	}

	r := newReconciler(route1, route2)

	// Rebuild only target-a
	if err := r.rebuildConfigMapsForTarget(context.Background(), "target-a"); err != nil {
		t.Fatalf("rebuildConfigMapsForTarget failed: %v", err)
	}

	// Verify target-a ConfigMap exists
	cmA := &corev1.ConfigMap{}
	err := r.Get(context.Background(), types.NamespacedName{
		Name: "customrouter-routes-target-a-0", Namespace: "test-ns",
	}, cmA)
	if err != nil {
		t.Fatalf("expected ConfigMap for target-a, got error: %v", err)
	}
	if cmA.Labels[configMapTargetLabel] != "target-a" {
		t.Errorf("expected target label target-a, got %s", cmA.Labels[configMapTargetLabel])
	}

	// Verify target-b ConfigMap does NOT exist
	cmB := &corev1.ConfigMap{}
	err = r.Get(context.Background(), types.NamespacedName{
		Name: "customrouter-routes-target-b-0", Namespace: "test-ns",
	}, cmB)
	if err == nil {
		t.Error("expected no ConfigMap for target-b, but one was found")
	}
}

func TestRebuildConfigMapsForTarget_CleansStalePartitions(t *testing.T) {
	// Pre-create a stale ConfigMap for target-a partition 1
	staleCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "customrouter-routes-target-a-1",
			Namespace: "test-ns",
			Labels: map[string]string{
				configMapManagedByLabel: configMapManagedByValue,
				configMapTargetLabel:    "target-a",
				configMapPartLabel:      "1",
			},
		},
		Data: map[string]string{routesDataKey: `{"version":1,"hosts":{}}`},
	}

	route := &v1alpha1.CustomHTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route-a", Namespace: "ns", UID: "uid-a"},
		Spec: v1alpha1.CustomHTTPRouteSpec{
			Hostnames: []string{"a.example.com"},
			TargetRef: v1alpha1.TargetRef{Name: "target-a"},
			Rules: []v1alpha1.Rule{
				{
					BackendRefs: []v1alpha1.BackendRef{{Name: "svc", Namespace: "ns", Port: 80}},
					Matches:     []v1alpha1.PathMatch{{Path: "/a", Type: "Exact"}},
				},
			},
		},
	}

	r := newReconciler(route, staleCM)

	if err := r.rebuildConfigMapsForTarget(context.Background(), "target-a"); err != nil {
		t.Fatalf("rebuildConfigMapsForTarget failed: %v", err)
	}

	// Partition 0 should exist
	cm0 := &corev1.ConfigMap{}
	if err := r.Get(context.Background(), types.NamespacedName{
		Name: "customrouter-routes-target-a-0", Namespace: "test-ns",
	}, cm0); err != nil {
		t.Fatalf("expected partition 0 to exist: %v", err)
	}

	// Stale partition 1 should be deleted
	cm1 := &corev1.ConfigMap{}
	err := r.Get(context.Background(), types.NamespacedName{
		Name: "customrouter-routes-target-a-1", Namespace: "test-ns",
	}, cm1)
	if err == nil {
		t.Error("expected stale partition 1 to be deleted, but it still exists")
	}
}

func TestRebuildConfigMapsForTarget_DeletesAllCMsWhenNoRoutes(t *testing.T) {
	// ConfigMap exists but no routes for target-a
	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "customrouter-routes-target-a-0",
			Namespace: "test-ns",
			Labels: map[string]string{
				configMapManagedByLabel: configMapManagedByValue,
				configMapTargetLabel:    "target-a",
				configMapPartLabel:      "0",
			},
		},
		Data: map[string]string{routesDataKey: `{"version":1,"hosts":{"a.com":[]}}`},
	}

	r := newReconciler(existingCM)

	if err := r.rebuildConfigMapsForTarget(context.Background(), "target-a"); err != nil {
		t.Fatalf("rebuildConfigMapsForTarget failed: %v", err)
	}

	cm := &corev1.ConfigMap{}
	err := r.Get(context.Background(), types.NamespacedName{
		Name: "customrouter-routes-target-a-0", Namespace: "test-ns",
	}, cm)
	if err == nil {
		t.Error("expected ConfigMap to be deleted when no routes exist for target")
	}
}

func TestUpsertSingleConfigMap_SkipsUnchanged(t *testing.T) {
	data := `{"version":1,"hosts":{"a.com":[{"path":"/api","type":"prefix","backend":"svc.ns.svc.cluster.local:80"}]}}`
	labels := map[string]string{
		"app.kubernetes.io/name": "customrouter",
		configMapManagedByLabel:  configMapManagedByValue,
		configMapTargetLabel:     "target-a",
		configMapPartLabel:       "0",
	}

	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "customrouter-routes-target-a-0",
			Namespace: "test-ns",
			Labels:    labels,
		},
		Data: map[string]string{routesDataKey: data},
	}

	r := newReconciler(existingCM)

	partition := ConfigMapPartition{
		Name:   "customrouter-routes-target-a-0",
		Target: "target-a",
		Data:   data,
	}

	// Should succeed without error (skip update)
	if err := r.upsertSingleConfigMap(context.Background(), partition); err != nil {
		t.Fatalf("upsertSingleConfigMap failed: %v", err)
	}

	// Verify the ConfigMap still has the same ResourceVersion (not updated)
	cm := &corev1.ConfigMap{}
	if err := r.Get(context.Background(), types.NamespacedName{
		Name: "customrouter-routes-target-a-0", Namespace: "test-ns",
	}, cm); err != nil {
		t.Fatalf("failed to get ConfigMap: %v", err)
	}
	if cm.ResourceVersion != existingCM.ResourceVersion {
		t.Error("ConfigMap was updated despite having the same content")
	}
}

func TestUpsertSingleConfigMap_CreatesNew(t *testing.T) {
	r := newReconciler()

	partition := ConfigMapPartition{
		Name:   "customrouter-routes-target-a-0",
		Target: "target-a",
		Data:   `{"version":1,"hosts":{}}`,
	}

	if err := r.upsertSingleConfigMap(context.Background(), partition); err != nil {
		t.Fatalf("upsertSingleConfigMap failed: %v", err)
	}

	cm := &corev1.ConfigMap{}
	if err := r.Get(context.Background(), types.NamespacedName{
		Name: "customrouter-routes-target-a-0", Namespace: "test-ns",
	}, cm); err != nil {
		t.Fatalf("expected ConfigMap to be created: %v", err)
	}
	if cm.Data[routesDataKey] != partition.Data {
		t.Errorf("unexpected data: %s", cm.Data[routesDataKey])
	}
}

func TestResolveExternalNames(t *testing.T) {
	extSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "ext-svc", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Type:         corev1.ServiceTypeExternalName,
			ExternalName: "external.example.com",
		},
	}
	normalSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "normal-svc", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	r := newReconciler(extSvc, normalSvc)

	testRoutes := []*v1alpha1.CustomHTTPRoute{
		{
			Spec: v1alpha1.CustomHTTPRouteSpec{
				Rules: []v1alpha1.Rule{
					{BackendRefs: []v1alpha1.BackendRef{
						{Name: "ext-svc", Namespace: "ns", Port: 80},
						{Name: "normal-svc", Namespace: "ns", Port: 80},
						{Name: "missing-svc", Namespace: "ns", Port: 80},
					}},
				},
			},
		},
	}

	result := r.resolveExternalNames(context.Background(), testRoutes)

	if result["ext-svc/ns"] != "external.example.com" {
		t.Errorf("expected ExternalName to be resolved, got %q", result["ext-svc/ns"])
	}
	if _, ok := result["normal-svc/ns"]; ok {
		t.Error("normal ClusterIP service should not appear in externalNames map")
	}
	if _, ok := result["missing-svc/ns"]; ok {
		t.Error("missing service should not appear in externalNames map")
	}
}

func TestDeleteStaleConfigMapsForTarget_OnlyDeletesOwnTarget(t *testing.T) {
	cmA := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "customrouter-routes-target-a-0",
			Namespace: "test-ns",
			Labels: map[string]string{
				configMapManagedByLabel: configMapManagedByValue,
				configMapTargetLabel:    "target-a",
			},
		},
	}
	cmB := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "customrouter-routes-target-b-0",
			Namespace: "test-ns",
			Labels: map[string]string{
				configMapManagedByLabel: configMapManagedByValue,
				configMapTargetLabel:    "target-b",
			},
		},
	}

	r := newReconciler(cmA, cmB)

	// Delete stale for target-a with empty active set (should delete cmA)
	if err := r.deleteStaleConfigMapsForTarget(context.Background(), "target-a", map[string]bool{}); err != nil {
		t.Fatalf("deleteStaleConfigMapsForTarget failed: %v", err)
	}

	// target-a's CM should be gone
	cm := &corev1.ConfigMap{}
	err := r.Get(context.Background(), types.NamespacedName{
		Name: "customrouter-routes-target-a-0", Namespace: "test-ns",
	}, cm)
	if err == nil {
		t.Error("expected target-a ConfigMap to be deleted")
	}

	// target-b's CM should still exist
	err = r.Get(context.Background(), types.NamespacedName{
		Name: "customrouter-routes-target-b-0", Namespace: "test-ns",
	}, cm)
	if err != nil {
		t.Errorf("target-b ConfigMap should still exist: %v", err)
	}
}
