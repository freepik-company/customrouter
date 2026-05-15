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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// TestServicePredicate_IgnoresIrrelevantUpdates locks the contract that
// annotation/label/status changes on a Service do NOT trigger a reconcile.
// Without this filter the operator was waking up dozens of times per
// second in busy clusters as cloud controllers, prometheus operator, and
// similar agents annotate Services for their own bookkeeping.
func TestServicePredicate_IgnoresIrrelevantUpdates(t *testing.T) {
	pred := servicePredicate()

	mkSvc := func() *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
			Spec: corev1.ServiceSpec{
				Type:         corev1.ServiceTypeClusterIP,
				ExternalName: "",
				Ports:        []corev1.ServicePort{{Port: 80}},
			},
		}
	}

	tests := []struct {
		name   string
		mutate func(s *corev1.Service)
		want   bool
	}{
		{
			name: "no spec.Type or ExternalName change → ignored",
			mutate: func(s *corev1.Service) {
				s.Annotations = map[string]string{"prometheus.io/scrape": "true"}
				s.Labels = map[string]string{"app": "new"}
				s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: "1.2.3.4"}}
			},
			want: false,
		},
		{
			name: "Type changed → enqueue",
			mutate: func(s *corev1.Service) {
				s.Spec.Type = corev1.ServiceTypeExternalName
				s.Spec.ExternalName = "elsewhere.example.com"
			},
			want: true,
		},
		{
			name: "ExternalName changed (still same Type=ExternalName) → enqueue",
			mutate: func(s *corev1.Service) {
				s.Spec.Type = corev1.ServiceTypeExternalName
				s.Spec.ExternalName = "old.example.com"
				// "new" version
			},
			want: true, // old=ClusterIP/"" → new=ExternalName/"old.example.com"
		},
		{
			name: "Port added but Type stable → ignored (operator does not consume ports)",
			mutate: func(s *corev1.Service) {
				s.Spec.Ports = append(s.Spec.Ports, corev1.ServicePort{Port: 8080})
			},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldSvc := mkSvc()
			newSvc := mkSvc()
			tc.mutate(newSvc)
			got := pred.Update(event.UpdateEvent{ObjectOld: oldSvc, ObjectNew: newSvc})
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestServicePredicate_CreateAndDeleteAlwaysPass asserts that the predicate
// only filters Updates. Creates and Deletes are unconditionally relevant —
// a new Service may be referenced by a CR, and a deleted one may invalidate
// existing rendering.
func TestServicePredicate_CreateAndDeleteAlwaysPass(t *testing.T) {
	pred := servicePredicate()
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc"}}

	if !pred.Create(event.CreateEvent{Object: svc}) {
		t.Error("Create should always pass")
	}
	if !pred.Delete(event.DeleteEvent{Object: svc}) {
		t.Error("Delete should always pass")
	}
}

// TestHTTPRoutePredicate_IgnoresStatusChurn is the load-bearing test for the
// noise reduction goal: Istio's pilot-discovery rewrites HTTPRoute.status
// every time it reconciles the dataplane (so, constantly). Without this
// filter, those writes drive the operator's reconcile rate. The predicate
// must reject any update that does not touch Spec.Hostnames.
func TestHTTPRoutePredicate_IgnoresStatusChurn(t *testing.T) {
	pred := httpRoutePredicate()

	mkHR := func() *gatewayv1.HTTPRoute {
		return &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "hr", Namespace: "ns"},
			Spec: gatewayv1.HTTPRouteSpec{
				Hostnames: []gatewayv1.Hostname{"example.com", "api.example.com"},
			},
		}
	}

	tests := []struct {
		name   string
		mutate func(h *gatewayv1.HTTPRoute)
		want   bool
	}{
		{
			name: "status-only update → ignored",
			mutate: func(h *gatewayv1.HTTPRoute) {
				h.Status.Parents = []gatewayv1.RouteParentStatus{{
					ControllerName: "istio.io/gateway-controller",
				}}
			},
			want: false,
		},
		{
			name: "annotation churn → ignored",
			mutate: func(h *gatewayv1.HTTPRoute) {
				h.Annotations = map[string]string{"istio.io/last-applied": "..."}
			},
			want: false,
		},
		{
			name: "hostname added → enqueue",
			mutate: func(h *gatewayv1.HTTPRoute) {
				h.Spec.Hostnames = append(h.Spec.Hostnames, "new.example.com")
			},
			want: true,
		},
		{
			name: "hostname removed → enqueue",
			mutate: func(h *gatewayv1.HTTPRoute) {
				h.Spec.Hostnames = h.Spec.Hostnames[:1]
			},
			want: true,
		},
		{
			name: "hostname reordered → enqueue (reorder requires explicit edit)",
			mutate: func(h *gatewayv1.HTTPRoute) {
				h.Spec.Hostnames = []gatewayv1.Hostname{"api.example.com", "example.com"}
			},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldHR := mkHR()
			newHR := mkHR()
			tc.mutate(newHR)
			got := pred.Update(event.UpdateEvent{ObjectOld: oldHR, ObjectNew: newHR})
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestHTTPRoutePredicate_CreateAndDeleteAlwaysPass mirrors the Service
// case: structural HTTPRoute lifecycle events always matter.
func TestHTTPRoutePredicate_CreateAndDeleteAlwaysPass(t *testing.T) {
	pred := httpRoutePredicate()
	hr := &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "hr"}}

	if !pred.Create(event.CreateEvent{Object: hr}) {
		t.Error("Create should always pass")
	}
	if !pred.Delete(event.DeleteEvent{Object: hr}) {
		t.Error("Delete should always pass")
	}
}
