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
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"

	apiv1alpha1 "github.com/openmcp-project/service-provider-external-secrets/api/v1alpha1"
	"github.com/openmcp-project/service-provider-external-secrets/pkg/externalsecrets"
	"github.com/openmcp-project/service-provider-external-secrets/pkg/spruntime"
)

const conditionReasonError = "ReconcileError"

// ErrManagedResources is an end-user facing error if errors are present inside ExternalSecretsOperator.Status.ManagedResources
var ErrManagedResources error = errors.New("resources contain reconcile errors")

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
func (r *ExternalSecretsOperatorReconciler) CreateOrUpdate(ctx context.Context, obj *apiv1alpha1.ExternalSecretsOperator, pc *apiv1alpha1.ProviderConfig, clusters spruntime.ClusterContext) (ctrl.Result, error) {
	spruntime.StatusProgressing(obj, "Reconciling", "Reconcile in progress")
	mgr, err := r.createObjectManager(obj, pc, clusters)
	if err != nil {
		spruntime.StatusProgressing(obj, conditionReasonError, err.Error())
		return ctrl.Result{}, spruntime.IgnoreFunctionalError(err)
	}
	results, err := mgr.Apply(ctx)
	managedResources, resultContainsErrors := resultsToResources(ctx, results)
	obj.Status.Resources = managedResources
	if allResourcesReady(managedResources) {
		spruntime.StatusReady(obj)
	}
	if resultContainsErrors || err != nil {
		return ctrl.Result{}, updateStatusError(obj, resultContainsErrors, err)
	}
	return ctrl.Result{}, nil
}

// Delete is called on every delete event
func (r *ExternalSecretsOperatorReconciler) Delete(ctx context.Context, obj *apiv1alpha1.ExternalSecretsOperator, pc *apiv1alpha1.ProviderConfig, clusters spruntime.ClusterContext) (ctrl.Result, error) {
	spruntime.StatusTerminating(obj)
	mgr, err := r.createObjectManager(obj, pc, clusters)
	if err != nil {
		spruntime.StatusProgressing(obj, conditionReasonError, err.Error())
		return ctrl.Result{}, spruntime.IgnoreFunctionalError(err)
	}
	results, err := mgr.Delete(ctx)
	managedResources, resultContainsErrors := resultsToResources(ctx, results)
	obj.Status.Resources = managedResources
	if externalsecrets.AllDeleted(results) {
		return ctrl.Result{}, nil
	}
	if resultContainsErrors || err != nil {
		return ctrl.Result{}, updateStatusError(obj, resultContainsErrors, err)
	}
	return ctrl.Result{
		RequeueAfter: time.Second * 5,
	}, nil
}

func updateStatusError(obj *apiv1alpha1.ExternalSecretsOperator, resourceErrors bool, err error) error {
	if resourceErrors {
		err = errors.Join(ErrManagedResources, err)
	}
	spruntime.StatusProgressing(obj, conditionReasonError, userErrorMessage(err))
	return spruntime.IgnoreFunctionalError(err)
}

// userErrorMessage constructs an end-user facing error message.
// Only end-user errors are processed.
func userErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	errorMessages := []string{}
	if errors.Is(err, ErrManagedResources) {
		errorMessages = append(errorMessages, ErrManagedResources.Error())
	}
	if errors.Is(err, externalsecrets.ErrOrphanCleanup) {
		errorMessages = append(errorMessages, externalsecrets.ErrOrphanCleanup.Error())
	}
	return strings.Join(errorMessages, "; ")
}

func (r *ExternalSecretsOperatorReconciler) createObjectManager(obj *apiv1alpha1.ExternalSecretsOperator, pc *apiv1alpha1.ProviderConfig, clusters spruntime.ClusterContext) (externalsecrets.Manager, error) {
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
	platformCluster := externalsecrets.NewManagedCluster(r.PlatformCluster, r.PlatformCluster.RESTConfig(), tenantNamespace, externalsecrets.PlatformCluster)
	externalSecretsNamespace := externalsecrets.DefaultNamespace
	if helmValues.NamespaceOverride != "" {
		externalSecretsNamespace = helmValues.NamespaceOverride
	}
	mcpCluster := externalsecrets.NewManagedCluster(clusters.MCPCluster, clusters.MCPCluster.RESTConfig(), externalSecretsNamespace, externalsecrets.ManagedControlPlane)
	// sync image pull secrets from platform cluster to mcp
	// Note: No prefix needed - these go to the MCP cluster's ESO namespace,
	// not the shared tenant namespace where collisions can occur
	for _, imagePullSecret := range helmValues.Global.ImagePullSecrets {
		externalsecrets.ManagePullSecret(mcpCluster, imagePullSecret, externalsecrets.SecretCopyConfig{
			SourceClient:    platformCluster.GetClient(),
			SourceNamespace: r.PodNamespace,
			TargetNamespace: externalSecretsNamespace,
			TargetName:      imagePullSecret.Name,
		})
	}
	// sync chart pull secrets within platform cluster from pod namespace to tenant namespace
	var prefixedChartPullSecret string
	if esoVersion.ChartPullSecret != "" {
		prefixedChartPullSecret, err = externalsecrets.PrefixSecretName(esoVersion.ChartPullSecret)
		if err != nil {
			return nil, fmt.Errorf("error generating secret name: %w", err)
		}
		externalsecrets.ManagePullSecret(platformCluster, corev1.LocalObjectReference{Name: esoVersion.ChartPullSecret}, externalsecrets.SecretCopyConfig{
			SourceClient:    platformCluster.GetClient(),
			SourceNamespace: r.PodNamespace,
			TargetNamespace: tenantNamespace,
			TargetName:      prefixedChartPullSecret,
		})
	}
	externalsecrets.ManageFluxResources(externalsecrets.ManageFluxResourcesParams{
		Cluster:             platformCluster,
		MCPNamespace:        externalSecretsNamespace,
		ChartPullSecretName: prefixedChartPullSecret,
		Obj:                 obj,
		Interval:            pc.PollInterval(),
		ClusterContext:      clusters,
		RequestedVersion:    esoVersion,
	})
	mgr := externalsecrets.NewManager()
	mgr.AddCluster(mcpCluster)
	mgr.AddCluster(platformCluster)

	ociRepoCleaner := externalsecrets.NewOCIRepositoryCleaner(platformCluster, tenantNamespace)
	helmReleaseCleaner := externalsecrets.NewHelmReleaseCleaner(platformCluster, tenantNamespace)

	platformSecretCleaner := externalsecrets.NewSecretCleaner(platformCluster, tenantNamespace, []corev1.LocalObjectReference{
		{
			Name: prefixedChartPullSecret,
		},
	})
	controlPlaneSecretCleaner := externalsecrets.NewSecretCleaner(mcpCluster, externalSecretsNamespace, helmValues.Global.ImagePullSecrets)

	mgr.AddCleaner(ociRepoCleaner)
	mgr.AddCleaner(helmReleaseCleaner)
	mgr.AddCleaner(platformSecretCleaner)
	mgr.AddCleaner(controlPlaneSecretCleaner)

	return mgr, nil
}

func selectExternalSecretsVersion(requestedVersion string, pc *apiv1alpha1.ProviderConfig) (apiv1alpha1.ExternalSecretsVersion, spruntime.FunctionalError) {
	for _, configVersion := range pc.Spec.Versions {
		if configVersion.Version == requestedVersion {
			return configVersion, nil
		}
	}
	return apiv1alpha1.ExternalSecretsVersion{}, spruntime.NewFunctionalError(fmt.Errorf("requested version is not available: %s", requestedVersion))
}

func resultsToResources(ctx context.Context, results []externalsecrets.Result) ([]apiv1alpha1.ManagedResource, bool) {
	l := log.FromContext(ctx)
	containsError := false
	resources := make([]apiv1alpha1.ManagedResource, 0, len(results))
	for _, res := range results {
		obj := res.Object.GetObject()
		status := res.Object.GetStatus(apiv1alpha1.ResourceLocation(res.Cluster.GetClusterType()))
		resources = append(resources, apiv1alpha1.ManagedResource{
			TypedObjectReference: corev1.TypedObjectReference{
				Kind:      reflect.TypeOf(obj).Elem().Name(),
				Name:      obj.GetName(),
				Namespace: nilIfEmptyString(obj.GetNamespace()),
			},
			Phase:    status.Phase,
			Message:  status.Message,
			Location: status.Location,
		})
		if res.Error != nil {
			containsError = true
			l.Error(res.Error, "objectID", externalsecrets.ObjectID(obj))
		}
	}
	return resources, containsError
}

func nilIfEmptyString(str string) *string {
	if str == "" {
		return nil
	}
	return ptr.To(str)
}

func allResourcesReady(resources []apiv1alpha1.ManagedResource) bool {
	for _, res := range resources {
		if res.Phase != apiv1alpha1.Ready {
			return false
		}
	}
	return true
}
