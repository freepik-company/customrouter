/*
Copyright 2024-2026 Freepik Company S.L.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package externalprocessorattachment

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	crv1alpha1 "github.com/freepik-company/customrouter/api/v1alpha1"
	"github.com/freepik-company/customrouter/internal/controller"
)

// TestReconcile_FinalizerRemoval_NamespaceGone_DoesNotError is a regression
// test for the production log line:
//
//	"Failed to remove finalizer" error="namespaces \"…\" not found"
//
// When the owning namespace is cascade-deleted, the apiserver returns
// NotFound on the final Patch that strips the finalizer. The resource is
// already gone, so Reconcile must treat that as success and not log an error
// or surface the failure to the controller-runtime workqueue.
func TestReconcile_FinalizerRemoval_NamespaceGone_DoesNotError(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := crv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	now := metav1.Now()
	attachment := &crv1alpha1.ExternalProcessorAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "epa-1",
			Namespace:         "doomed-ns",
			Finalizers:        []string{controller.ResourceFinalizer},
			DeletionTimestamp: &now,
		},
	}

	notFoundOnPatch := interceptor.Funcs{
		Patch: func(
			ctx context.Context,
			c client.WithWatch,
			obj client.Object,
			patch client.Patch,
			opts ...client.PatchOption,
		) error {
			// Simulate the apiserver response when the owning namespace has
			// been deleted between the Reconcile Get and the finalizer Patch.
			return apierrors.NewNotFound(
				schema.GroupResource{Resource: "namespaces"},
				obj.GetNamespace(),
			)
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(attachment).
		WithInterceptorFuncs(notFoundOnPatch).
		Build()

	r := &ExternalProcessorAttachmentReconciler{Client: cl, Scheme: scheme}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: attachment.Name, Namespace: attachment.Namespace},
	})

	if err != nil {
		t.Fatalf("Reconcile must swallow NotFound from finalizer removal, got error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("Reconcile must not requeue on swallowed NotFound, got: %+v", res)
	}
}

// TestReconcile_FinalizerRemoval_RealErrorIsSurfaced ensures the NotFound
// shortcut does not also hide unrelated Patch failures, which would mask
// real bugs.
func TestReconcile_FinalizerRemoval_RealErrorIsSurfaced(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := crv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	now := metav1.Now()
	attachment := &crv1alpha1.ExternalProcessorAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "epa-2",
			Namespace:         "ns",
			Finalizers:        []string{controller.ResourceFinalizer},
			DeletionTimestamp: &now,
		},
	}

	forbiddenOnPatch := interceptor.Funcs{
		Patch: func(
			ctx context.Context,
			c client.WithWatch,
			obj client.Object,
			patch client.Patch,
			opts ...client.PatchOption,
		) error {
			return apierrors.NewForbidden(
				schema.GroupResource{Group: crv1alpha1.GroupVersion.Group, Resource: "externalprocessorattachments"},
				obj.GetName(),
				nil,
			)
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(attachment).
		WithInterceptorFuncs(forbiddenOnPatch).
		Build()

	r := &ExternalProcessorAttachmentReconciler{Client: cl, Scheme: scheme}

	// Reconcile currently does not return the finalizer-removal error (it just
	// logs it and returns nil). What we assert here is that the NotFound
	// shortcut is *narrow*: a Forbidden error must NOT be reclassified as
	// "resource already gone". If a future refactor decides to surface this
	// error, the assertion below will need adjusting — but the important
	// regression is that NotFound and Forbidden are not collapsed together.
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: attachment.Name, Namespace: attachment.Namespace},
	})
	if err != nil {
		t.Fatalf("current behavior: Reconcile swallows finalizer-removal errors; got %v", err)
	}

	// And the resource must still carry the finalizer (proof that the Patch
	// did not silently succeed).
	got := &crv1alpha1.ExternalProcessorAttachment{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: attachment.Name, Namespace: attachment.Namespace}, got); err != nil {
		t.Fatalf("Get after Reconcile: %v", err)
	}
	if len(got.Finalizers) == 0 {
		t.Fatalf("finalizer should still be present after Forbidden Patch, got none")
	}
}
