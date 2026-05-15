/*
Copyright 2024-2026 Freepik Company S.L.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package customhttproute

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/internal/controller"
)

// TestReconcile_FinalizerRemoval_NamespaceGone_DoesNotError is a regression
// test for the production error:
//
//	"Failed to remove finalizer" error="namespaces \"…\" not found"
//
// When a namespace is cascade-deleted, the apiserver removes its
// CustomHTTPRoutes alongside it. The controller's final reconcile observes
// the DeletionTimestamp and tries to strip the finalizer; the Get-then-Update
// inside UpdateWithRetry then hits a 404 because the namespace (and the CR)
// are gone. Reconcile must treat that as success rather than logging a
// scary "Failed to remove finalizer" line and surfacing a workqueue error.
func TestReconcile_FinalizerRemoval_NamespaceGone_DoesNotError(t *testing.T) {
	scheme := newScheme()

	now := metav1.Now()
	route := &v1alpha1.CustomHTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "route-1",
			Namespace:         "doomed-ns",
			Finalizers:        []string{controller.ResourceFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "gw"},
		},
	}

	// Simulate the apiserver response when the owning namespace has been
	// deleted between the Reconcile Get and the finalizer-removal Update.
	// UpdateWithRetry does Get-then-Update; intercepting Update is enough to
	// reach the error-classification block we want to exercise.
	notFoundOnUpdate := interceptor.Funcs{
		Update: func(
			ctx context.Context,
			c client.WithWatch,
			obj client.Object,
			opts ...client.UpdateOption,
		) error {
			return apierrors.NewNotFound(
				schema.GroupResource{Resource: "namespaces"},
				obj.GetNamespace(),
			)
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(route).
		WithIndex(&v1alpha1.CustomHTTPRoute{}, targetRefIndexField, func(obj client.Object) []string {
			r := obj.(*v1alpha1.CustomHTTPRoute)
			return []string{r.Spec.TargetRef.Name}
		}).
		WithInterceptorFuncs(notFoundOnUpdate).
		Build()

	r := &CustomHTTPRouteReconciler{
		Client:             cl,
		Scheme:             scheme,
		ConfigMapNamespace: "test-ns",
		// Disable the in-memory state GC so the test stays self-contained.
		StateGCInterval: -1,
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: route.Name, Namespace: route.Namespace},
	})

	if err != nil {
		t.Fatalf("Reconcile must swallow NotFound from finalizer removal, got error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("Reconcile must not requeue on swallowed NotFound, got: %+v", res)
	}
}

// TestReconcile_FinalizerRemoval_RealErrorIsNotMisclassified guards the
// inverse direction: the NotFound shortcut must be *narrow*. A Forbidden or
// any other non-NotFound error must remain observable so real bugs are not
// hidden behind the "namespace gone" log line.
func TestReconcile_FinalizerRemoval_RealErrorIsNotMisclassified(t *testing.T) {
	scheme := newScheme()

	now := metav1.Now()
	route := &v1alpha1.CustomHTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "route-2",
			Namespace:         "ns",
			Finalizers:        []string{controller.ResourceFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: v1alpha1.CustomHTTPRouteSpec{
			TargetRef: v1alpha1.TargetRef{Name: "gw"},
		},
	}

	forbiddenOnUpdate := interceptor.Funcs{
		Update: func(
			ctx context.Context,
			c client.WithWatch,
			obj client.Object,
			opts ...client.UpdateOption,
		) error {
			return apierrors.NewForbidden(
				schema.GroupResource{Group: v1alpha1.GroupVersion.Group, Resource: "customhttproutes"},
				obj.GetName(),
				nil,
			)
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(route).
		WithIndex(&v1alpha1.CustomHTTPRoute{}, targetRefIndexField, func(obj client.Object) []string {
			r := obj.(*v1alpha1.CustomHTTPRoute)
			return []string{r.Spec.TargetRef.Name}
		}).
		WithInterceptorFuncs(forbiddenOnUpdate).
		Build()

	r := &CustomHTTPRouteReconciler{
		Client:             cl,
		Scheme:             scheme,
		ConfigMapNamespace: "test-ns",
		StateGCInterval:    -1,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: route.Name, Namespace: route.Namespace},
	})
	// Current Reconcile contract: finalizer-removal failures are logged but
	// not returned to the workqueue. What this test pins down is that the
	// finalizer is *still present* on the object after a non-NotFound error,
	// proving the shortcut did not silently swallow the real failure.
	if err != nil {
		t.Fatalf("current contract: Reconcile logs but does not return finalizer-removal errors; got %v", err)
	}

	got := &v1alpha1.CustomHTTPRoute{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: route.Name, Namespace: route.Namespace}, got); err != nil {
		t.Fatalf("Get after Reconcile: %v", err)
	}
	if len(got.Finalizers) == 0 {
		t.Fatalf("finalizer should still be present after Forbidden Update, got none")
	}
}
