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
	"encoding/json"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// HTTPRouteValidator validates HTTPRoute resources against existing CustomHTTPRoutes.
type HTTPRouteValidator struct {
	Client  client.Reader
	checker *HostnameChecker
}

// NewHTTPRouteValidator creates a new HTTPRouteValidator.
func NewHTTPRouteValidator(cl client.Reader) *HTTPRouteValidator {
	return &HTTPRouteValidator{
		Client:  cl,
		checker: &HostnameChecker{Client: cl},
	}
}

// Handle processes admission requests for HTTPRoute resources.
func (v *HTTPRouteValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	if req.Operation == admissionv1.Delete {
		return admission.Allowed("delete is always allowed")
	}

	httpRoute := &gatewayv1.HTTPRoute{}
	if err := json.Unmarshal(req.Object.Raw, httpRoute); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if err := v.checker.CheckHTTPRouteHostnames(ctx, httpRoute); err != nil {
		return admission.Denied(err.Error())
	}

	return admission.Allowed("no hostname conflicts")
}
