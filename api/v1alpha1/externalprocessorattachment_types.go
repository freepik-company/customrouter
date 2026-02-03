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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GatewayRef defines the reference to a Gateway workload by label selector
type GatewayRef struct {
	// selector is a set of labels used to identify the Gateway workload.
	// These labels are used in the EnvoyFilter's workloadSelector.
	// +required
	// +kubebuilder:validation:MinProperties=1
	Selector map[string]string `json:"selector"`
}

// ServiceRef defines a reference to a Kubernetes Service
type ServiceRef struct {
	// name is the name of the Service
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// namespace is the namespace of the Service
	// +required
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// port is the port of the Service
	// +required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
}

// ExternalProcessorRef defines the reference to an external processor service
type ExternalProcessorRef struct {
	// service is the reference to the external processor Kubernetes Service
	// +required
	Service ServiceRef `json:"service"`

	// timeout is the gRPC timeout for the external processor service.
	// This is the timeout for establishing the gRPC connection.
	// Must be a valid duration string (e.g., "1s", "500ms", "2s").
	// Defaults to "5s" if not specified.
	// +optional
	// +kubebuilder:default="5s"
	Timeout string `json:"timeout,omitempty"`

	// messageTimeout is the timeout for individual messages sent to the external processor.
	// This applies to each request/response exchange with the ext_proc service.
	// Must be a valid duration string (e.g., "1s", "500ms", "5s").
	// Defaults to "5s" if not specified.
	// +optional
	// +kubebuilder:default="5s"
	MessageTimeout string `json:"messageTimeout,omitempty"`
}

// ExternalProcessorAttachmentSpec defines the desired state of ExternalProcessorAttachment
type ExternalProcessorAttachmentSpec struct {
	// gatewayRef identifies the Gateway workload to attach the external processor to
	// +required
	GatewayRef GatewayRef `json:"gatewayRef"`

	// externalProcessorRef identifies the external processor service to use
	// +required
	ExternalProcessorRef ExternalProcessorRef `json:"externalProcessorRef"`
}

// ExternalProcessorAttachmentStatus defines the observed state of ExternalProcessorAttachment.
type ExternalProcessorAttachmentStatus struct {
	// conditions represent the current state of the ExternalProcessorAttachment resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Gateway",type="string",JSONPath=".spec.gatewayRef.selector",description="Gateway selector"
// +kubebuilder:printcolumn:name="Processor",type="string",JSONPath=".spec.externalProcessorRef.service.name",description="External processor service"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ExternalProcessorAttachment is the Schema for the externalprocessorattachments API.
// It attaches an external processor to a Gateway by generating the necessary EnvoyFilters.
type ExternalProcessorAttachment struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ExternalProcessorAttachment
	// +required
	Spec ExternalProcessorAttachmentSpec `json:"spec"`

	// status defines the observed state of ExternalProcessorAttachment
	// +optional
	Status ExternalProcessorAttachmentStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ExternalProcessorAttachmentList contains a list of ExternalProcessorAttachment
type ExternalProcessorAttachmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ExternalProcessorAttachment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ExternalProcessorAttachment{}, &ExternalProcessorAttachmentList{})
}
