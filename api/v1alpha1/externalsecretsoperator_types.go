/*
Copyright 2025.

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
	commonapi "github.com/openmcp-project/openmcp-operator/api/common"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InstancePhase is a custom type representing the phase of a service instance.
type InstancePhase string

// ResourceLocation is a custom type representing the location of a resource.
type ResourceLocation string

// Constants representing the phases of an instance lifecycle.
const (
	Pending     InstancePhase = "Pending"
	Progressing InstancePhase = "Progressing"
	Ready       InstancePhase = "Ready"
	Failed      InstancePhase = "Failed"
	Terminating InstancePhase = "Terminating"
	Unknown     InstancePhase = "Unknown"

	ManagedControlPlane ResourceLocation = "ManagedControlPlane"
	PlatformCluster     ResourceLocation = "PlatformCluster"
)

// ExternalSecretsOperatorSpec defines the desired state of ExternalSecretsOperator
type ExternalSecretsOperatorSpec struct {
	// Version is the external-secrets Helm chart version to install.
	Version string `json:"version"`
}

// ExternalSecretsOperatorStatus defines the observed state of ExternalSecretsOperator.
type ExternalSecretsOperatorStatus struct {
	commonapi.Status `json:",inline"`

	// Resources managed by this External Secrets Operator instance
	// +optional
	Resources []ManagedResource `json:"resources,omitempty"`
}

// ManagedResource defines a kubernetes object with its lifecycle phase
type ManagedResource struct {
	corev1.TypedObjectReference `json:",inline"`

	// +required
	Phase InstancePhase `json:"phase"`
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	Location ResourceLocation `json:"location,omitempty"`
}

// ExternalSecretsOperator is the Schema for the externalsecretsoperators API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=`.status.phase`,name="Phase",type=string
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:metadata:labels="openmcp.cloud/cluster=onboarding"
type ExternalSecretsOperator struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ExternalSecretsOperator
	// +required
	Spec ExternalSecretsOperatorSpec `json:"spec"`

	// status defines the observed state of ExternalSecretsOperator
	// +optional
	Status ExternalSecretsOperatorStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ExternalSecretsOperatorList contains a list of ExternalSecretsOperator
type ExternalSecretsOperatorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ExternalSecretsOperator `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ExternalSecretsOperator{}, &ExternalSecretsOperatorList{})
}

// Finalizer returns the finalizer string for the ExternalSecretsOperator resource
func (o *ExternalSecretsOperator) Finalizer() string {
	return GroupVersion.Group + "/finalizer"
}

// GetStatus returns the status of the ExternalSecretsOperator resource
func (o *ExternalSecretsOperator) GetStatus() any {
	return o.Status
}

// GetConditions returns the conditions of the ExternalSecretsOperator resource
func (o *ExternalSecretsOperator) GetConditions() *[]metav1.Condition {
	return &o.Status.Conditions
}

// SetPhase sets the phase of the ExternalSecretsOperator resource status
func (o *ExternalSecretsOperator) SetPhase(phase string) {
	o.Status.Phase = phase
}

// SetObservedGeneration sets the observed generation of the ExternalSecretsOperator resource
func (o *ExternalSecretsOperator) SetObservedGeneration(gen int64) {
	o.Status.ObservedGeneration = gen
}
