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

package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"

	fluxpkg "github.com/openmcp-project/controller-utils/pkg/manager/flux"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	"github.com/openmcp-project/controller-utils/pkg/manager"

	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"

	ctrlerrors "github.com/openmcp-project/controller-utils/pkg/errors"
	"github.com/openmcp-project/opencontrolplane-runtime/pkg/serviceprovider"
	"github.com/openmcp-project/opencontrolplane-runtime/pkg/serviceprovider/clusteraccess"

	apiv1alpha1 "github.com/openmcp-project/service-provider-external-secrets/api/v1alpha1"
	"github.com/openmcp-project/service-provider-external-secrets/pkg/externalsecrets"
)

const conditionReasonError = "ReconcileError"

// resolvedVersion wraps an apiv1alpha1.RequestedVersion with the prefixed chart
// pull secret name, satisfying fluxpkg.FluxResourceVersion for the
// ManageFluxResources call. This is the service-provider adapter pattern:
// the CRD type stores the raw secret name; the controller supplies the
// namespace-prefixed copy name when registering Flux resources.
type resolvedVersion struct {
	apiv1alpha1.RequestedVersion
	chartPullSecret string
}

// GetChartPullSecret overrides the promoted method to return the prefixed name.
func (r resolvedVersion) GetChartPullSecret() string { return r.chartPullSecret }

// ExternalSecretsOperatorReconciler reconciles a ExternalSecretsOperator object
type ExternalSecretsOperatorReconciler struct {
	// OnboardingCluster is the cluster where this controller watches ExternalSecretsOperator resources and reacts to their changes.
	OnboardingCluster *clusters.Cluster
	// PlatformCluster is the cluster where this controller is deployed and configured.
	PlatformCluster *clusters.Cluster
	// PodNamespace is the namespace where this controller is deployed in.
	PodNamespace string
}

// CreateOrUpdate is called on every add or update event
func (r *ExternalSecretsOperatorReconciler) CreateOrUpdate(ctx context.Context, obj *apiv1alpha1.ExternalSecretsOperator, pc *apiv1alpha1.ProviderConfig, clusters clusteraccess.ClusterContext) (ctrl.Result, error) {
	serviceprovider.StatusProgressing(obj, "Reconciling", "Reconcile in progress")
	mgr, err := r.createObjectManager(obj, pc, clusters)
	if err != nil {
		serviceprovider.StatusProgressing(obj, conditionReasonError, err.Error())
		return ctrl.Result{}, ctrlerrors.IgnoreInvalidUserInput(err)
	}
	resources, done, err := mgr.Apply(ctx)
	obj.Status.Resources = toResources(resources, obj.Status.Resources)
	if err != nil {
		return ctrl.Result{}, updateStatusError(obj, err)
	}
	if !done {
		return ctrl.Result{RequeueAfter: pc.PollInterval()}, nil
	}
	serviceprovider.StatusReady(obj)
	return ctrl.Result{}, nil
}

// Delete is called on every delete event
func (r *ExternalSecretsOperatorReconciler) Delete(ctx context.Context, obj *apiv1alpha1.ExternalSecretsOperator, pc *apiv1alpha1.ProviderConfig, clusters clusteraccess.ClusterContext) (ctrl.Result, error) {
	serviceprovider.StatusTerminating(obj)
	mgr, err := r.createObjectManager(obj, pc, clusters)
	if err != nil {
		serviceprovider.StatusProgressing(obj, conditionReasonError, err.Error())
		return ctrl.Result{}, ctrlerrors.IgnoreInvalidUserInput(err)
	}
	resources, done, err := mgr.Delete(ctx)
	obj.Status.Resources = toResources(resources, obj.Status.Resources)
	if err != nil {
		return ctrl.Result{}, updateStatusError(obj, err)
	}
	if !done {
		return ctrl.Result{RequeueAfter: pc.PollInterval()}, nil
	}
	return ctrl.Result{}, nil
}

// toResources converts the framework's []manager.ManagedResource interface slice into the
// CRD-embedded concrete []apiv1alpha1.ManagedResource slice owned by SPES.
// existing is the previous Resources slice from the object's status; it is used to
// preserve LastTransitionTime on conditions that have not changed status.
func toResources(in []manager.ManagedResource, existing []apiv1alpha1.ManagedResource) []apiv1alpha1.ManagedResource {
	type resourceKey struct {
		APIGroup  string
		Kind      string
		Namespace string
		Name      string
	}
	// Index existing conditions by Kind+Namespace+Name so SetCondition can preserve LastTransitionTime.
	prevConditions := make(map[resourceKey][]metav1.Condition, len(existing))
	for _, e := range existing {
		key := resourceKey{e.GetAPIGroup(), e.GetKind(), e.GetNamespace(), e.GetName()}
		prevConditions[key] = e.Status.Conditions
	}

	out := make([]apiv1alpha1.ManagedResource, len(in))
	for i, r := range in {
		status := r.GetStatus()

		key := resourceKey{r.GetAPIGroup(), r.GetKind(), r.GetNamespace(), r.GetName()}
		managedResourceStatus := apiv1alpha1.ManagedResourceStatus{
			Phase:      status.Phase,
			Message:    status.Message,
			Conditions: append([]metav1.Condition{}, prevConditions[key]...),
		}

		condition := status.GetCondition(r.GetGeneration())
		managedResourceStatus.SetCondition(condition)

		apiGroup := r.GetAPIGroup()
		var apiGroupPtr *string
		if apiGroup != "" {
			apiGroupPtr = &apiGroup
		}

		out[i] = apiv1alpha1.ManagedResource{
			TypedObjectReference: corev1.TypedObjectReference{
				APIGroup:  apiGroupPtr,
				Kind:      r.GetKind(),
				Name:      r.GetName(),
				Namespace: nilIfEmpty(r.GetNamespace()),
			},
			Status:   managedResourceStatus,
			Location: string(r.GetLocation()),
		}
	}
	return out
}

// NilIfEmpty returns nil if s is the empty string, otherwise a pointer to s.
// Use this when populating optional *string fields in Kubernetes API objects
// (e.g. TypedObjectReference.Namespace) from a plain string namespace.
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return ptr.To(s)
}

func updateStatusError(obj *apiv1alpha1.ExternalSecretsOperator, err error) error {
	serviceprovider.StatusProgressing(obj, conditionReasonError, userErrorMessage(err))
	return ctrlerrors.IgnoreInvalidUserInput(err)
}

// userErrorMessage extracts only the user-facing portion of an error for status reporting.
func userErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	var messages []string
	if errors.Is(err, manager.ErrManagedResourcesFailed) {
		messages = append(messages, manager.ErrManagedResourcesFailed.Error())
	}
	if errors.Is(err, manager.ErrOrphanCleanup) {
		messages = append(messages, manager.ErrOrphanCleanup.Error())
	}
	if len(messages) == 0 {
		messages = append(messages, "internal reconcile error — check controller logs")
	}
	return strings.Join(messages, "; ")
}

func (r *ExternalSecretsOperatorReconciler) createObjectManager(obj *apiv1alpha1.ExternalSecretsOperator, pc *apiv1alpha1.ProviderConfig, clusters clusteraccess.ClusterContext) (manager.Manager, error) {
	tenantNamespace, err := libutils.StableMCPNamespace(obj.Name, obj.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to determine tenant namespace for external secrets deployment: %w", err)
	}
	// select the requested version from the provider config
	esoVersion, err := selectExternalSecretsVersion(obj.Spec.Version, pc)
	if err != nil {
		return nil, err
	}
	helmValues, err := externalsecrets.ExtractHelmValues(esoVersion.HelmValues)
	if err != nil {
		return nil, fmt.Errorf("failed to extract helm values: %w", err)
	}
	platformCluster := manager.NewManagedCluster(r.PlatformCluster, r.PlatformCluster.RESTConfig(), tenantNamespace, manager.PlatformCluster)
	externalSecretsNamespace := externalsecrets.DefaultNamespace
	if helmValues.NamespaceOverride != "" {
		externalSecretsNamespace = helmValues.NamespaceOverride
	}
	mcpCluster := manager.NewManagedCluster(clusters.MCPCluster, clusters.MCPCluster.RESTConfig(), externalSecretsNamespace, manager.ManagedControlPlane)
	// sync image pull secrets from platform cluster to mcp
	// Note: No prefix needed - these go to the MCP cluster's ESO namespace,
	// not the shared tenant namespace where collisions can occur
	for _, imagePullSecret := range helmValues.Global.ImagePullSecrets {
		manager.ManagePullSecret(mcpCluster, manager.SecretCopyConfig{
			SourceClient:    platformCluster.GetClient(),
			SourceName:      imagePullSecret.Name,
			SourceNamespace: r.PodNamespace,
			TargetNamespace: externalSecretsNamespace,
			TargetName:      imagePullSecret.Name,
		})
	}
	// sync chart pull secrets within platform cluster from pod namespace to tenant namespace
	var prefixedChartPullSecret string
	if esoVersion.ChartPullSecret != "" {
		prefixedChartPullSecret, err = manager.PrefixSecretName(esoVersion.ChartPullSecret, externalsecrets.ChartPullSecretPrefix)
		if err != nil {
			return nil, fmt.Errorf("error generating secret name: %w", err)
		}
		manager.ManagePullSecret(platformCluster, manager.SecretCopyConfig{
			SourceClient:    platformCluster.GetClient(),
			SourceName:      esoVersion.ChartPullSecret,
			SourceNamespace: r.PodNamespace,
			TargetNamespace: tenantNamespace,
			TargetName:      prefixedChartPullSecret,
		})
	}
	// resolvedVersion supplies the prefixed secret name to the flux framework
	// while keeping all other version fields from the spec.
	rv := resolvedVersion{
		RequestedVersion: esoVersion,
		chartPullSecret:  prefixedChartPullSecret,
	}
	err = fluxpkg.ManageFluxResources(fluxpkg.ManageFluxResourcesParams{
		Cluster:           platformCluster,
		MCPNamespace:      externalSecretsNamespace,
		Interval:          pc.PollInterval(),
		ClusterContext:    clusters,
		RequestedVersion:  rv,
		OCIRepositoryName: pc.Name,
		HelmReleaseName:   pc.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("configuring Flux resources: %w", err)
	}
	mgr := manager.NewManager(pc.Name)
	mgr.AddCluster(mcpCluster)
	mgr.AddCluster(platformCluster)

	var secretsToKeep []corev1.LocalObjectReference
	if prefixedChartPullSecret != "" {
		secretsToKeep = []corev1.LocalObjectReference{
			{Name: prefixedChartPullSecret},
		}
	}

	platformSecretCleaner := manager.NewSecretCleaner(platformCluster, pc.Name, tenantNamespace, secretsToKeep)
	controlPlaneSecretCleaner := manager.NewSecretCleaner(mcpCluster, pc.Name, externalSecretsNamespace, helmValues.Global.ImagePullSecrets)

	mgr.AddCleaner(platformSecretCleaner)
	mgr.AddCleaner(controlPlaneSecretCleaner)

	return mgr, nil
}

func selectExternalSecretsVersion(requestedVersion string, pc *apiv1alpha1.ProviderConfig) (apiv1alpha1.RequestedVersion, error) {
	for _, configVersion := range pc.Spec.Versions {
		if configVersion.Version == requestedVersion {
			return configVersion, nil
		}
	}
	return apiv1alpha1.RequestedVersion{}, fmt.Errorf("%w: requested version (%s) is not available", ctrlerrors.ErrInvalidUserInput, requestedVersion)
}
