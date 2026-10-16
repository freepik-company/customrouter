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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	ef "github.com/freepik-company/customrouter/internal/controller/envoyfilter"
)

// With virtualHosts mode Auto the EPA controller is the only writer of "-catchall":
// this controller must leave it alone (and keep writing it for the other EPAs).
func TestReconcileCatchAllFromRoutes_SkipsEPAsWithVirtualHosts(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	if err := gatewayv1.Install(scheme); err != nil {
		t.Fatalf("gateway Install: %v", err)
	}
	scheme.AddKnownTypeWithName(ef.GVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(ef.GVK.GroupVersion().WithKind(ef.GVK.Kind+"List"), &unstructured.UnstructuredList{})

	selector := map[string]string{"istio": "gateway"}
	auto := v1alpha1.ExternalProcessorAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: "auto", Namespace: "istio"},
		Spec: v1alpha1.ExternalProcessorAttachmentSpec{
			GatewayRef:   v1alpha1.GatewayRef{Selector: selector},
			VirtualHosts: &v1alpha1.VirtualHostsConfig{Mode: v1alpha1.VirtualHostsModeAuto},
		},
	}
	legacy := v1alpha1.ExternalProcessorAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: "istio"},
		Spec:       v1alpha1.ExternalProcessorAttachmentSpec{GatewayRef: v1alpha1.GatewayRef{Selector: selector}},
	}
	routes := &v1alpha1.CustomHTTPRouteList{Items: []v1alpha1.CustomHTTPRoute{{
		ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "app"},
		Spec: v1alpha1.CustomHTTPRouteSpec{
			Hostnames:     []string{"www.example.com"},
			CatchAllRoute: &v1alpha1.CatchAllBackendRef{BackendRef: v1alpha1.BackendRef{Name: "front", Namespace: "web", Port: 80}},
		},
	}}}
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &CustomHTTPRouteReconciler{Client: cl, Scheme: scheme}

	epas := &v1alpha1.ExternalProcessorAttachmentList{Items: []v1alpha1.ExternalProcessorAttachment{auto, legacy}}
	if err := r.reconcileCatchAllFromRoutes(context.Background(), routes, epas); err != nil {
		t.Fatalf("reconcileCatchAllFromRoutes: %v", err)
	}

	exists := func(name string) bool {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(ef.GVK)
		return cl.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "istio"}, obj) == nil
	}
	if exists("auto" + ef.CatchAllFilterSuffix) {
		t.Fatal("the EPA in Auto must not get its -catchall from the CustomHTTPRoute controller")
	}
	if !exists("legacy" + ef.CatchAllFilterSuffix) {
		t.Fatal("EPAs without virtual hosts keep their -catchall from the CustomHTTPRoute controller")
	}
}
