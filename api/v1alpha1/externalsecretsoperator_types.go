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
	"github.com/openmcp-project/controller-utils/pkg/manager"
	commonapi "github.com/openmcp-project/openmcp-operator/api/common"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	Resources []*ManagedResource `json:"resources,omitempty"`
}

// ManagedResource are helper resources managed by the service-provider-external-secrets
type ManagedResource struct {
	corev1.TypedObjectReference `json:",inline"`
	// +optional
	Status ManagedResourceStatus `json:"status,omitempty"`
	// +optional
	Location string `json:"location,omitempty"`
}

// SetReference stores the identity of the managed resource. It implements
// manager.ResourceStatusWriter, deciding here how optional fields
// (APIGroup, Namespace) are represented as pointers in the API type.
func (m *ManagedResource) SetReference(ref manager.ResourceRef) {
	m.APIGroup = nilIfEmpty(ref.APIGroup)
	m.Kind = ref.Kind
	m.Name = ref.Name
	m.Namespace = nilIfEmpty(ref.Namespace)
	m.Location = ref.Location
}

// SetPhase stores the lifecycle phase and message of the managed resource.
// It implements manager.ResourceStatusWriter.
func (m *ManagedResource) SetPhase(phase, message string) {
	m.Status.Phase = phase
	m.Status.Message = message
}

// nilIfEmpty returns nil if s is the empty string, otherwise a pointer to s.
// Use this when populating optional *string fields in Kubernetes API objects
// (e.g. TypedObjectReference.Namespace) from a plain string.
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ManagedResourceStatus is the status of a ManagedResource
type ManagedResourceStatus struct {
	// +optional
	Phase string `json:"phase,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
}

// GetPhase returns the lifecycle phase of the managed resource.
func (m *ManagedResourceStatus) GetPhase() string {
	return m.Phase
}

// GetMessage returns the human-readable status message of the managed resource.
func (m *ManagedResourceStatus) GetMessage() string {
	return m.Message
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
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &ExternalSecretsOperator{}, &ExternalSecretsOperatorList{})
		return nil
	})
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
