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
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"

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
	obj.Status.Resources = toResources(resources)
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
	obj.Status.Resources = toResources(resources)
	if err != nil {
		return ctrl.Result{}, updateStatusError(obj, err)
	}
	if !done {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

// toResources converts the framework's []manager.ManagedResource interface slice into the
// CRD-embedded concrete []apiv1alpha1.ManagedResource slice owned by SPES.
func toResources(in []manager.ManagedResource) []apiv1alpha1.ManagedResource {
	out := make([]apiv1alpha1.ManagedResource, len(in))
	for i, r := range in {
		apiGroup := r.GetAPIVersion()
		var apiGroupPtr *string
		if apiGroup != "" {
			apiGroupPtr = &apiGroup
		}
		out[i] = apiv1alpha1.ManagedResource{
			TypedObjectReference: corev1.TypedObjectReference{
				APIGroup:  apiGroupPtr,
				Kind:      r.GetKind(),
				Name:      r.GetName(),
				Namespace: r.GetNamespace(),
			},
			Status: apiv1alpha1.ManagedResourceStatus{
				Phase:   r.GetStatus().Phase,
				Message: r.GetStatus().Message,
			},
			Location: string(r.GetLocation()),
		}
	}
	return out
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
		manager.ManagePullSecret(mcpCluster, imagePullSecret, manager.SecretCopyConfig{
			SourceClient:    platformCluster.GetClient(),
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
		manager.ManagePullSecret(platformCluster, corev1.LocalObjectReference{Name: esoVersion.ChartPullSecret}, manager.SecretCopyConfig{
			SourceClient:    platformCluster.GetClient(),
			SourceNamespace: r.PodNamespace,
			TargetNamespace: tenantNamespace,
			TargetName:      prefixedChartPullSecret,
		})
	}
	manager.ManageFluxResources(manager.ManageFluxResourcesParams{
		Cluster:             platformCluster,
		MCPNamespace:        externalSecretsNamespace,
		ChartPullSecretName: prefixedChartPullSecret,
		Interval:            pc.PollInterval(),
		ClusterContext:      clusters,
		RequestedVersion:    esoVersion,
		OCIRepositoryName:   pc.Name,
		HelmReleaseName:     pc.Name,
	})
	mgr := manager.NewManager(pc.Name)
	mgr.AddCluster(mcpCluster)
	mgr.AddCluster(platformCluster)

	platformSecretCleaner := manager.NewSecretCleaner(platformCluster, pc.Name, tenantNamespace, []corev1.LocalObjectReference{
		{Name: prefixedChartPullSecret},
	})
	controlPlaneSecretCleaner := manager.NewSecretCleaner(mcpCluster, pc.Name, externalSecretsNamespace, helmValues.Global.ImagePullSecrets)

	mgr.AddCleaner(platformSecretCleaner)
	mgr.AddCleaner(controlPlaneSecretCleaner)

	return mgr, nil
}

func selectExternalSecretsVersion(requestedVersion string, pc *apiv1alpha1.ProviderConfig) (manager.RequestedVersion, error) {
	for _, configVersion := range pc.Spec.Versions {
		if configVersion.Version == requestedVersion {
			return configVersion, nil
		}
	}
	return manager.RequestedVersion{}, fmt.Errorf("%w: requested version (%s) is not available", ctrlerrors.ErrInvalidUserInput, requestedVersion)
}
