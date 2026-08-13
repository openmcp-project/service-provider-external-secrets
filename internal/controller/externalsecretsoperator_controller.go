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
	obj.Status.Resources = manager.ProjectResources(resources, newManagedResource)
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
	obj.Status.Resources = manager.ProjectResources(resources, newManagedResource)
	if err != nil {
		return ctrl.Result{}, updateStatusError(obj, err)
	}
	if !done {
		return ctrl.Result{RequeueAfter: pc.PollInterval()}, nil
	}
	return ctrl.Result{}, nil
}

// newManagedResource constructs a fresh, writable CRD-embedded ManagedResource
// for manager.ProjectResources to populate via its setter interface.
func newManagedResource() *apiv1alpha1.ManagedResource {
	return &apiv1alpha1.ManagedResource{}
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
	helmValues, err := externalsecrets.ExtractHelmValues(esoVersion.GetHelmValues())
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
	r.manageImagePullSecrets(platformCluster, mcpCluster, helmValues, externalSecretsNamespace)
	// sync chart pull secrets within platform cluster from pod namespace to tenant namespace
	prefixedChartPullSecret, err := r.manageChartPullSecret(platformCluster, esoVersion, tenantNamespace)
	if err != nil {
		return nil, err
	}

	fluxResourceVersion := esoVersion
	if prefixedChartPullSecret != "" {
		fluxResourceVersion.ChartPullSecret = prefixedChartPullSecret
	}
	err = fluxpkg.ManageFluxResources(fluxpkg.ManageFluxResourcesParams{
		Cluster:           platformCluster,
		MCPNamespace:      externalSecretsNamespace,
		Interval:          pc.PollInterval(),
		ClusterContext:    clusters,
		RequestedVersion:  fluxResourceVersion,
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

// manageImagePullSecrets registers copies of the image pull secrets from the
// platform cluster's pod namespace into the MCP cluster's ESO namespace.
func (r *ExternalSecretsOperatorReconciler) manageImagePullSecrets(platformCluster, mcpCluster manager.ManagedCluster, helmValues *externalsecrets.HelmValues, externalSecretsNamespace string) {
	for _, imagePullSecret := range helmValues.Global.ImagePullSecrets {
		manager.ManagePullSecret(mcpCluster, manager.SecretCopyConfig{
			SourceClient:    platformCluster.GetClient(),
			SourceName:      imagePullSecret.Name,
			SourceNamespace: r.PodNamespace,
			TargetNamespace: externalSecretsNamespace,
			TargetName:      imagePullSecret.Name,
		})
	}
}

// manageChartPullSecret registers a prefixed copy of the chart pull secret within
// the platform cluster (pod namespace -> tenant namespace) and returns the prefixed
// name, or an empty string if no chart pull secret is configured.
func (r *ExternalSecretsOperatorReconciler) manageChartPullSecret(platformCluster manager.ManagedCluster, esoVersion apiv1alpha1.RequestedVersion, tenantNamespace string) (string, error) {
	sourceSecret := esoVersion.GetChartPullSecret()
	if sourceSecret == "" {
		return "", nil
	}
	prefixedChartPullSecret, err := manager.PrefixSecretName(sourceSecret, externalsecrets.ChartPullSecretPrefix)
	if err != nil {
		return "", fmt.Errorf("error generating secret name: %w", err)
	}
	manager.ManagePullSecret(platformCluster, manager.SecretCopyConfig{
		SourceClient:    platformCluster.GetClient(),
		SourceName:      sourceSecret,
		SourceNamespace: r.PodNamespace,
		TargetNamespace: tenantNamespace,
		TargetName:      prefixedChartPullSecret,
	})
	return prefixedChartPullSecret, nil
}

func selectExternalSecretsVersion(requestedVersion string, pc *apiv1alpha1.ProviderConfig) (apiv1alpha1.RequestedVersion, error) {
	for _, configVersion := range pc.Spec.Versions {
		if configVersion.Version == requestedVersion {
			return configVersion, nil
		}
	}
	return apiv1alpha1.RequestedVersion{}, fmt.Errorf("%w: requested version (%s) is not available", ctrlerrors.ErrInvalidUserInput, requestedVersion)
}
