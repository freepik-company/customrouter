/*
Copyright 2024.

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

package controller

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	CustomHttpRouteResourceType = "CustomHttpRoute"

	//
	ResourceNotFoundError         = "%s '%s' resource not found. Ignoring since object must be deleted."
	ResourceRetrievalError        = "Error getting the %s '%s' from the cluster: %s"
	ResourceFinalizersUpdateError = "Failed to update finalizer of %s '%s': %s"
	ResourceConditionUpdateError  = "Failed to update the condition on %s '%s': %s"
	ResourceReconcileError        = "Can not reconcile %s '%s': %s"
	ResourceValidationError       = "Validation failed for %s '%s': %s"

	//
	ResourceFinalizer = "customrouter.freepik.com/finalizer"
)

// UpdateWithRetry fetches the object, applies a mutation, and updates it with retry-on-conflict using exponential backoff.
func UpdateWithRetry(
	ctx context.Context,
	k8sClient client.Client,
	object client.Object,
	mutate func(obj client.Object) error) error {

	key := types.NamespacedName{
		Namespace: object.GetNamespace(),
		Name:      object.GetName(),
	}

	reasonableBackoff := wait.Backoff{
		Steps:    5,
		Duration: 200 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.2,
	}

	return retry.RetryOnConflict(reasonableBackoff, func() error {
		if err := k8sClient.Get(ctx, key, object); err != nil {
			return err
		}

		if err := mutate(object); err != nil {
			return err
		}

		return k8sClient.Update(ctx, object)
	})
}

// UpdateStatusWithRetry fetches the object, applies a mutation to its status, and updates the status subresource
// with retry-on-conflict using exponential backoff.
// IMPORTANT: The mutate function receives a freshly fetched object. Any status changes should be applied
// within the mutate function, not to the original object passed to this function.
func UpdateStatusWithRetry(
	ctx context.Context,
	k8sClient client.Client,
	object client.Object,
	mutate func(obj client.Object) error) error {

	key := types.NamespacedName{
		Namespace: object.GetNamespace(),
		Name:      object.GetName(),
	}

	reasonableBackoff := wait.Backoff{
		Steps:    5,
		Duration: 200 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.2,
	}

	return retry.RetryOnConflict(reasonableBackoff, func() error {
		if err := k8sClient.Get(ctx, key, object); err != nil {
			return err
		}

		if err := mutate(object); err != nil {
			return err
		}

		updateErr := k8sClient.Status().Update(ctx, object)
		return updateErr
	})
}
