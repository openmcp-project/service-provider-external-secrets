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
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// RequestedVersion defines a version of a Service Operator that can be installed.
// It implements flux.FluxResourceVersion (implicitly — no import needed).
// +kubebuilder:object:generate=true
type RequestedVersion struct {
	// Version is the Service Operator version to install.
	// This version is compared with ExternalSecretsOperator.Spec.Version to select
	// the deployment artifacts for a version.
	// +required
	Version string `json:"version"`

	// ChartVersion is the version of the Helm chart to install.
	// +required
	ChartVersion string `json:"chartVersion"`

	// ChartURL is a reference to an OCI artifact repository that hosts the Helm chart.
	// +optional
	ChartURL string `json:"chartURL,omitempty"`

	// ChartPullSecret is a reference to the secret containing the credentials to pull
	// the Helm chart. The secret must be of type kubernetes.io/dockerconfigjson.
	// +optional
	ChartPullSecret string `json:"chartPullSecret,omitempty"`

	// HelmValues are arbitrary Helm values passed directly to the managed HelmRelease.
	// +optional
	HelmValues *apiextensionsv1.JSON `json:"helmValues,omitempty"`
}

// GetVersion implements flux.FluxResourceVersion.
func (r RequestedVersion) GetVersion() string { return r.Version }

// GetChartVersion implements flux.FluxResourceVersion.
func (r RequestedVersion) GetChartVersion() string { return r.ChartVersion }

// GetChartURL implements flux.FluxResourceVersion.
func (r RequestedVersion) GetChartURL() string { return r.ChartURL }

// GetChartPullSecret implements flux.FluxResourceVersion.
// Returns the raw (un-prefixed) secret name stored in the spec. The controller
// wraps this type in a resolvedVersion adapter to supply the prefixed name when
// calling ManageFluxResources.
func (r RequestedVersion) GetChartPullSecret() string { return r.ChartPullSecret }

// GetHelmValues implements flux.FluxResourceVersion.
func (r RequestedVersion) GetHelmValues() *apiextensionsv1.JSON { return r.HelmValues }

// ProviderConfigSpec defines the desired state of ProviderConfig
type ProviderConfigSpec struct {
	// Versions specify the valid inputs for the ExternalSecretsOperator.Spec.Version field.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=version
	Versions []RequestedVersion `json:"versions"`

	// PollInterval at which the controller requeues to detect drift
	// +optional
	// +kubebuilder:default:="1m"
	// +kubebuilder:validation:Format=duration
	PollInterval *metav1.Duration `json:"pollInterval,omitempty"`
}

// ProviderConfigStatus defines the observed state of ProviderConfig.
type ProviderConfigStatus struct {
	// conditions represent the current state of the ProviderConfig resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ProviderConfig is the Schema for the providerconfigs API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:metadata:labels="openmcp.cloud/cluster=platform"
type ProviderConfig struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ProviderConfig
	// +required
	Spec ProviderConfigSpec `json:"spec"`

	// status defines the observed state of ProviderConfig
	// +optional
	Status ProviderConfigStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ProviderConfigList contains a list of ProviderConfig
type ProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &ProviderConfig{}, &ProviderConfigList{})
		return nil
	})
}

// PollInterval returns the poll interval duration from the spec.
func (o *ProviderConfig) PollInterval() time.Duration {
	return o.Spec.PollInterval.Duration
}
