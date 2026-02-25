/*
Copyright 2026.

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

package webhook

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	customrouterv1alpha1 "github.com/freepik-company/customrouter/api/v1alpha1"
)

// +kubebuilder:webhook:path=/validate-customrouter-freepik-com-v1alpha1-customhttproute,mutating=false,failurePolicy=fail,sideEffects=None,groups=customrouter.freepik.com,resources=customhttproutes,verbs=create;update,versions=v1alpha1,name=vcustomhttproute.kb.io,admissionReviewVersions=v1

// CustomHTTPRouteValidator validates CustomHTTPRoute resources.
type CustomHTTPRouteValidator struct {
	checker *HostnameChecker
}

var _ admission.CustomValidator = &CustomHTTPRouteValidator{}

// ValidateCreate validates a CustomHTTPRoute on creation.
func (v *CustomHTTPRouteValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	route, ok := obj.(*customrouterv1alpha1.CustomHTTPRoute)
	if !ok {
		return nil, fmt.Errorf("expected CustomHTTPRoute, got %T", obj)
	}

	if err := route.Validate(); err != nil {
		return nil, err
	}
	if err := v.checker.CheckCustomHTTPRouteHostnames(ctx, route); err != nil {
		return nil, err
	}
	return nil, nil
}

// ValidateUpdate validates a CustomHTTPRoute on update.
func (v *CustomHTTPRouteValidator) ValidateUpdate(ctx context.Context, _ runtime.Object, newObj runtime.Object) (admission.Warnings, error) {
	route, ok := newObj.(*customrouterv1alpha1.CustomHTTPRoute)
	if !ok {
		return nil, fmt.Errorf("expected CustomHTTPRoute, got %T", newObj)
	}

	if err := route.Validate(); err != nil {
		return nil, err
	}
	if err := v.checker.CheckCustomHTTPRouteHostnames(ctx, route); err != nil {
		return nil, err
	}
	return nil, nil
}

// ValidateDelete is a no-op for CustomHTTPRoute.
func (v *CustomHTTPRouteValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// SetupCustomHTTPRouteWebhookWithManager registers the CustomHTTPRoute validating webhook.
func SetupCustomHTTPRouteWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&customrouterv1alpha1.CustomHTTPRoute{}).
		WithValidator(&CustomHTTPRouteValidator{
			checker: &HostnameChecker{Client: mgr.GetClient()},
		}).
		Complete()
}
