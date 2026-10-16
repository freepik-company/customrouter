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

package externalprocessorattachment

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	crv1alpha1 "github.com/freepik-company/customrouter/api/v1alpha1"
	ef "github.com/freepik-company/customrouter/internal/controller/envoyfilter"
)

func vhostScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := crv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("corev1 AddToScheme: %v", err)
	}
	if err := gatewayv1.Install(scheme); err != nil {
		t.Fatalf("gateway Install: %v", err)
	}
	scheme.AddKnownTypeWithName(ef.GVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(ef.GVK.GroupVersion().WithKind(ef.GVK.Kind+"List"), &unstructured.UnstructuredList{})
	return scheme
}

func reconcileEPA(t *testing.T, epa *crv1alpha1.ExternalProcessorAttachment, objs ...client.Object) (client.Client, *crv1alpha1.ExternalProcessorAttachment) {
	t.Helper()
	scheme := vhostScheme(t)
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(append([]client.Object{epa}, objs...)...).
		WithStatusSubresource(&crv1alpha1.ExternalProcessorAttachment{}).
		Build()
	r := &ExternalProcessorAttachmentReconciler{Client: cl, Scheme: scheme}
	key := types.NamespacedName{Name: epa.Name, Namespace: epa.Namespace}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	got := &crv1alpha1.ExternalProcessorAttachment{}
	if err := cl.Get(context.Background(), key, got); err != nil {
		t.Fatalf("Get EPA: %v", err)
	}
	return cl, got
}

func vhostsFilterExists(t *testing.T, cl client.Client, epa *crv1alpha1.ExternalProcessorAttachment) bool {
	t.Helper()
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(ef.GVK)
	err := cl.Get(context.Background(), types.NamespacedName{Name: epa.Name + ef.VirtualHostsFilterSuffix, Namespace: epa.Namespace}, obj)
	return err == nil
}

func testEPA(cfg *crv1alpha1.VirtualHostsConfig) *crv1alpha1.ExternalProcessorAttachment {
	return &crv1alpha1.ExternalProcessorAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: "gw", Namespace: "istio"},
		Spec: crv1alpha1.ExternalProcessorAttachmentSpec{
			GatewayRef: crv1alpha1.GatewayRef{Selector: map[string]string{"istio": "gateway"}},
			ExternalProcessorRef: crv1alpha1.ExternalProcessorRef{
				Service: crv1alpha1.ServiceRef{Name: "extproc", Namespace: "customrouter", Port: 9001},
			},
			VirtualHosts: cfg,
		},
	}
}

func testRoute(name string, hostnames ...string) *crv1alpha1.CustomHTTPRoute {
	return &crv1alpha1.CustomHTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "app"},
		Spec:       crv1alpha1.CustomHTTPRouteSpec{Hostnames: hostnames},
	}
}

func TestReconcile_VirtualHosts_OffByDefault(t *testing.T) {
	epa := testEPA(nil)
	cl, got := reconcileEPA(t, epa, testRoute("oidc", "id.example.com"))

	if vhostsFilterExists(t, cl, epa) {
		t.Fatal("no virtual hosts EnvoyFilter may be created unless spec.virtualHosts.mode is Auto")
	}
	if len(got.Status.ManagedVirtualHosts) != 0 || meta.FindStatusCondition(got.Status.Conditions, ConditionTypeVirtualHosts) != nil {
		t.Fatalf("status must not report virtual hosts when disabled: %+v", got.Status)
	}
}

func TestReconcile_VirtualHosts_Auto(t *testing.T) {
	epa := testEPA(&crv1alpha1.VirtualHostsConfig{
		Mode:             crv1alpha1.VirtualHostsModeAuto,
		ExcludeHostnames: []string{"manual.example.com"},
	})
	gwNamespace := gatewayv1.Namespace("istio")
	addressType := gatewayv1.HostnameAddressType
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "stable", Namespace: "istio"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "istio",
			Addresses:        []gatewayv1.GatewaySpecAddress{{Type: &addressType, Value: "gw.istio.svc.cluster.local"}},
			Listeners:        []gatewayv1.Listener{{Name: "proxy", Port: 80, Protocol: gatewayv1.HTTPProtocolType}},
		},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "gw", Namespace: "istio"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"istio": "gateway"}},
	}
	// Owned by this EPA's gateway: auth.example.com already has a virtual host.
	ownRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "console", Namespace: "keycloak"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{{Name: "stable", Namespace: &gwNamespace}}},
			Hostnames:       []gatewayv1.Hostname{"auth.example.com"},
		},
	}
	// Another gateway's HTTPRoute must not take id.example.com away.
	foreignRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "elsewhere", Namespace: "app"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{{Name: "internal", Namespace: &gwNamespace}}},
			Hostnames:       []gatewayv1.Hostname{"id.example.com"},
		},
	}
	cl, got := reconcileEPA(t, epa,
		testRoute("oidc", "id.example.com", "auth.example.com"),
		testRoute("other", "id.example.com", "manual.example.com"),
		gw, svc, ownRoute, foreignRoute,
	)

	if !vhostsFilterExists(t, cl, epa) {
		t.Fatal("virtual hosts EnvoyFilter not created")
	}
	if want := []string{"id.example.com"}; !reflect.DeepEqual(got.Status.ManagedVirtualHosts, want) {
		t.Fatalf("managedVirtualHosts = %v, want %v", got.Status.ManagedVirtualHosts, want)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, ConditionTypeVirtualHosts)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("missing %s condition: %+v", ConditionTypeVirtualHosts, got.Status.Conditions)
	}
	if want := "1 virtual hosts managed; skipped: auth.example.com (HTTPRoute), manual.example.com (Excluded)"; cond.Message != want {
		t.Fatalf("condition message = %q, want %q", cond.Message, want)
	}

	// Turning the feature off removes the generated EnvoyFilter.
	got.Spec.VirtualHosts.Mode = crv1alpha1.VirtualHostsModeDisabled
	if err := cl.Update(context.Background(), got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	r := &ExternalProcessorAttachmentReconciler{Client: cl, Scheme: cl.Scheme()}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: epa.Name, Namespace: epa.Namespace}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if vhostsFilterExists(t, cl, epa) {
		t.Fatal("virtual hosts EnvoyFilter must be deleted when the feature is turned off")
	}
}

// A Service whose selector changes flips Gateway ownership: the watch must enqueue the
// EPAs in Auto, and the next reconcile must stop managing the hostnames the now-owned
// Gateway's HTTPRoutes serve (never a duplicate domain).
func TestReconcile_VirtualHosts_ServiceChangesOwnership(t *testing.T) {
	epa := testEPA(&crv1alpha1.VirtualHostsConfig{Mode: crv1alpha1.VirtualHostsModeAuto})
	disabled := testEPA(nil)
	disabled.Name = "other-epa"
	addressType := gatewayv1.HostnameAddressType
	gwNamespace := gatewayv1.Namespace("istio")
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "stable", Namespace: "istio"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "istio",
			Addresses:        []gatewayv1.GatewaySpecAddress{{Type: &addressType, Value: "gw.istio.svc.cluster.local"}},
			Listeners:        []gatewayv1.Listener{{Name: "proxy", Port: 80, Protocol: gatewayv1.HTTPProtocolType}},
		},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "gw", Namespace: "istio"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"istio": "someone-else"}},
	}
	unrelated := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"}}
	hr := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "console", Namespace: "keycloak"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{{Name: "stable", Namespace: &gwNamespace}}},
			Hostnames:       []gatewayv1.Hostname{"auth.example.com"},
		},
	}
	cl, got := reconcileEPA(t, epa, disabled, gw, svc, unrelated, hr, testRoute("oidc", "auth.example.com"))
	if want := []string{"auth.example.com"}; !reflect.DeepEqual(got.Status.ManagedVirtualHosts, want) {
		t.Fatalf("foreign gateway: managed = %v, want %v", got.Status.ManagedVirtualHosts, want)
	}

	r := &ExternalProcessorAttachmentReconciler{Client: cl, Scheme: cl.Scheme()}
	if reqs := r.findEPAsForGatewayService(context.Background(), unrelated); len(reqs) != 0 {
		t.Fatalf("a Service that fronts no Gateway must not enqueue EPAs, got %v", reqs)
	}
	reqs := r.findEPAsForGatewayService(context.Background(), svc)
	if len(reqs) != 1 || reqs[0].Name != epa.Name {
		t.Fatalf("the Gateway's Service must enqueue only the EPAs in Auto, got %v", reqs)
	}

	svc.Spec.Selector = map[string]string{"istio": "gateway"}
	if err := cl.Update(context.Background(), svc); err != nil {
		t.Fatalf("Update Service: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), reqs[0]); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if vhostsFilterExists(t, cl, epa) {
		t.Fatal("once the Gateway is owned, auth.example.com belongs to its HTTPRoute: no automatic virtual host")
	}
}

// virtualHostDomains returns the domains an EnvoyFilter adds with VIRTUAL_HOST ADD.
func virtualHostDomains(obj *unstructured.Unstructured) []string {
	patches, _, _ := unstructured.NestedSlice(obj.Object, "spec", "configPatches")
	var out []string
	for _, p := range patches {
		patch := p.(map[string]interface{})
		if patch["applyTo"] != "VIRTUAL_HOST" {
			continue
		}
		domains, _, _ := unstructured.NestedStringSlice(patch, "patch", "value", "domains")
		out = append(out, domains...)
	}
	return out
}

// TestReconcile_VirtualHosts_NeverOverlapCatchAll moves a hostname from the automatic
// virtual hosts into the catch-all and back, and checks after every single write that
// no domain is added by both "-vhosts" and "-catchall" at the same time.
func TestReconcile_VirtualHosts_NeverOverlapCatchAll(t *testing.T) {
	epa := testEPA(&crv1alpha1.VirtualHostsConfig{Mode: crv1alpha1.VirtualHostsModeAuto})
	scheme := vhostScheme(t)

	// Live VIRTUAL_HOST domains per EnvoyFilter, updated on every write.
	state := map[string][]string{}
	var writes []string
	check := func(t *testing.T) {
		t.Helper()
		seen := map[string]string{}
		for name, domains := range state {
			for _, d := range domains {
				if other, dup := seen[d]; dup && other != name {
					t.Fatalf("domain %s added by both %s and %s after writes %v", d, other, name, writes)
				}
				seen[d] = name
			}
		}
	}
	record := func(obj client.Object, deleted bool) {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			return
		}
		writes = append(writes, u.GetName())
		if deleted {
			delete(state, u.GetName())
		} else {
			state[u.GetName()] = virtualHostDomains(u)
		}
		check(t)
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(epa, testRoute("oidc", "id.example.com")).
		WithStatusSubresource(&crv1alpha1.ExternalProcessorAttachment{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				record(obj, false)
				return c.Create(ctx, obj, opts...)
			},
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				record(obj, false)
				return c.Update(ctx, obj, opts...)
			},
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				record(obj, true)
				return c.Delete(ctx, obj, opts...)
			},
		}).
		Build()
	r := &ExternalProcessorAttachmentReconciler{Client: cl, Scheme: scheme}
	key := types.NamespacedName{Name: epa.Name, Namespace: epa.Namespace}
	reconcile := func() {
		t.Helper()
		if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
	}
	setCatchAll := func(hostnames ...string) {
		t.Helper()
		cur := &crv1alpha1.ExternalProcessorAttachment{}
		if err := cl.Get(context.Background(), key, cur); err != nil {
			t.Fatalf("Get: %v", err)
		}
		cur.Spec.CatchAllRoute = nil
		if len(hostnames) > 0 {
			cur.Spec.CatchAllRoute = &crv1alpha1.CatchAllRouteConfig{
				Hostnames:  hostnames,
				BackendRef: crv1alpha1.BackendRef{Name: "front", Namespace: "web", Port: 80},
			}
		}
		if err := cl.Update(context.Background(), cur); err != nil {
			t.Fatalf("Update EPA: %v", err)
		}
	}

	reconcile()
	if got := state[epa.Name+ef.VirtualHostsFilterSuffix]; !reflect.DeepEqual(got, []string{"id.example.com"}) {
		t.Fatalf("start: -vhosts = %v", got)
	}

	// vhosts -> catch-all
	setCatchAll("id.example.com")
	reconcile()
	if _, ok := state[epa.Name+ef.VirtualHostsFilterSuffix]; ok {
		t.Fatalf("id.example.com must have left -vhosts: %v", state)
	}
	if got := state[epa.Name+ef.CatchAllFilterSuffix]; !reflect.DeepEqual(got, []string{"id.example.com"}) {
		t.Fatalf("-catchall = %v", got)
	}

	// catch-all -> vhosts
	setCatchAll()
	reconcile()
	if _, ok := state[epa.Name+ef.CatchAllFilterSuffix]; ok {
		t.Fatalf("id.example.com must have left -catchall: %v", state)
	}
	if got := state[epa.Name+ef.VirtualHostsFilterSuffix]; !reflect.DeepEqual(got, []string{"id.example.com"}) {
		t.Fatalf("end: -vhosts = %v", got)
	}
}
