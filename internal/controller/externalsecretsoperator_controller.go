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
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"

	ctrlerrors "github.com/openmcp-project/controller-utils/pkg/errors"
	"github.com/openmcp-project/opencontrolplane-runtime/pkg/serviceprovider"
	"github.com/openmcp-project/opencontrolplane-runtime/pkg/serviceprovider/clusteraccess"

	apiv1alpha1 "github.com/openmcp-project/service-provider-external-secrets/api/v1alpha1"
	"github.com/openmcp-project/service-provider-external-secrets/pkg/externalsecrets"
)

const conditionReasonError = "ReconcileError"

// Placement selects where the managed service controllers are installed.
type Placement string

const (
	// PlacementMCP installs the service controllers on the managed control plane.
	PlacementMCP Placement = "mcp"
	// PlacementPlatform installs the service controllers on the existing platform cluster.
	// The controllers use the MCP access credential as their kubeconfig.
	PlacementPlatform Placement = "platform"
)

// Validate checks whether the controller cluster value is supported.
func (c Placement) Validate() error {
	if c != PlacementMCP && c != PlacementPlatform {
		return fmt.Errorf("service controller cluster must be %q or %q, got %q", PlacementMCP, PlacementPlatform, c)
	}
	return nil
}

// ErrManagedResources is an end-user facing error if errors are present inside ExternalSecretsOperator.Status.ManagedResources
var ErrManagedResources = errors.New("resources contain reconcile errors")

// ExternalSecretsOperatorReconciler reconciles a ExternalSecretsOperator object
type ExternalSecretsOperatorReconciler struct {
	// OnboardingCluster is the cluster where this controller watches ExternalSecretsOperator resources and reacts to their changes.
	OnboardingCluster *clusters.Cluster
	// PlatformCluster is the cluster where this controller is deployed and configured.
	PlatformCluster *clusters.Cluster
	// PodNamespace is the namespace where this controller is deployed in.
	PodNamespace string
	Placement    Placement
}

// CreateOrUpdate is called on every add or update event
func (r *ExternalSecretsOperatorReconciler) CreateOrUpdate(ctx context.Context, obj *apiv1alpha1.ExternalSecretsOperator, pc *apiv1alpha1.ProviderConfig, clusters clusteraccess.ClusterContext) (ctrl.Result, error) {
	serviceprovider.StatusProgressing(obj, "Reconciling", "Reconcile in progress")
	mgr, err := r.createObjectManager(ctx, obj, pc, clusters)
	if err != nil {
		serviceprovider.StatusProgressing(obj, conditionReasonError, err.Error())
		return ctrl.Result{}, ctrlerrors.IgnoreInvalidUserInput(err)
	}
	results, err := mgr.Apply(ctx)
	managedResources, resultContainsErrors := resultsToResources(ctx, results)
	obj.Status.Resources = managedResources
	if allResourcesReady(managedResources) {
		serviceprovider.StatusReady(obj)
	}
	if resultContainsErrors || err != nil {
		return ctrl.Result{}, updateStatusError(obj, resultContainsErrors, err)
	}
	return ctrl.Result{}, nil
}

// Delete is called on every delete event
func (r *ExternalSecretsOperatorReconciler) Delete(ctx context.Context, obj *apiv1alpha1.ExternalSecretsOperator, pc *apiv1alpha1.ProviderConfig, clusters clusteraccess.ClusterContext) (ctrl.Result, error) {
	serviceprovider.StatusTerminating(obj)
	mgr, err := r.createObjectManager(ctx, obj, pc, clusters)
	if err != nil {
		serviceprovider.StatusProgressing(obj, conditionReasonError, err.Error())
		return ctrl.Result{}, ctrlerrors.IgnoreInvalidUserInput(err)
	}
	results, err := mgr.Delete(ctx)
	managedResources, resultContainsErrors := resultsToResources(ctx, results)
	obj.Status.Resources = managedResources
	if resultContainsErrors || err != nil {
		return ctrl.Result{}, updateStatusError(obj, resultContainsErrors, err)
	}
	if externalsecrets.AllDeleted(results) {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{
		RequeueAfter: time.Second * 5,
	}, nil
}

func updateStatusError(obj *apiv1alpha1.ExternalSecretsOperator, resourceErrors bool, err error) error {
	if resourceErrors {
		err = errors.Join(ErrManagedResources, err)
	}
	serviceprovider.StatusProgressing(obj, conditionReasonError, userErrorMessage(err))
	return ctrlerrors.IgnoreInvalidUserInput(err)
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

func (r *ExternalSecretsOperatorReconciler) createObjectManager(ctx context.Context, obj *apiv1alpha1.ExternalSecretsOperator, pc *apiv1alpha1.ProviderConfig, clusters clusteraccess.ClusterContext) (externalsecrets.Manager, error) {
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
	controllersOnPlatform := r.Placement == PlacementPlatform
	externalSecretsNamespace := installationNamespace(externalsecrets.DefaultNamespace, helmValues.NamespaceOverride, tenantNamespace, controllersOnPlatform)
	mcpCluster := externalsecrets.NewManagedCluster(clusters.MCPCluster, clusters.MCPCluster.RESTConfig(), externalSecretsNamespace, externalsecrets.ManagedControlPlane)
	controllerCluster := mcpCluster
	var credential, remoteNamespace externalsecrets.ManagedObject
	if controllersOnPlatform {
		if err := r.configureRemoteVersion(ctx, obj.DeletionTimestamp.IsZero(), &esoVersion, tenantNamespace, clusters.MCPAccessSecretKey); err != nil {
			return nil, err
		}
		controllerCluster = platformCluster
		remoteNamespace = externalsecrets.ManageNamespace(mcpCluster, tenantNamespace)
		credential = externalsecrets.ManageMCPCredential(controllerCluster, r.PlatformCluster.Client(), clusters.MCPAccessSecretKey)
	}
	// sync image pull secrets from platform cluster to mcp
	// Note: No prefix needed - these go to the MCP cluster's ESO namespace,
	// not the shared tenant namespace where collisions can occur
	for _, imagePullSecret := range helmValues.Global.ImagePullSecrets {
		externalsecrets.ManagePullSecret(controllerCluster, imagePullSecret, externalsecrets.SecretCopyConfig{
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
		Cluster:               platformCluster,
		MCPNamespace:          externalSecretsNamespace,
		ChartPullSecretName:   prefixedChartPullSecret,
		Obj:                   obj,
		Interval:              pc.PollInterval(),
		ClusterContext:        clusters,
		RequestedVersion:      esoVersion,
		ControllersOnPlatform: controllersOnPlatform,
		RemoteNamespace:       remoteNamespace,
		RemoteCredential:      credential,
	})
	mgr := externalsecrets.NewManager()
	mgr.AddCluster(mcpCluster)
	mgr.AddCluster(platformCluster)

	platformSecrets := append([]corev1.LocalObjectReference{}, helmValues.Global.ImagePullSecrets...)
	platformSecrets = append(platformSecrets, corev1.LocalObjectReference{Name: prefixedChartPullSecret}, corev1.LocalObjectReference{Name: externalsecrets.RemoteCredentialName})
	platformSecretCleaner := externalsecrets.NewSecretCleaner(platformCluster, tenantNamespace, platformSecrets)
	controllerSecrets := append([]corev1.LocalObjectReference{}, helmValues.Global.ImagePullSecrets...)
	if controllersOnPlatform {
		controllerSecrets = append(controllerSecrets, corev1.LocalObjectReference{Name: prefixedChartPullSecret}, corev1.LocalObjectReference{Name: externalsecrets.RemoteCredentialName})
	}
	controlPlaneSecretCleaner := externalsecrets.NewSecretCleaner(controllerCluster, externalSecretsNamespace, controllerSecrets)

	mgr.AddCleaner(platformSecretCleaner)
	mgr.AddCleaner(controlPlaneSecretCleaner)

	return mgr, nil
}

func (r *ExternalSecretsOperatorReconciler) mcpCredentialHash(ctx context.Context, key client.ObjectKey) (string, error) {
	secret := &corev1.Secret{}
	if err := r.PlatformCluster.Client().Get(ctx, key, secret); err != nil {
		return "", fmt.Errorf("failed to read MCP access credential: %w", err)
	}
	kubeconfig, ok := secret.Data["kubeconfig"]
	if !ok || len(kubeconfig) == 0 {
		return "", fmt.Errorf("MCP access credential %s does not contain kubeconfig", key)
	}
	return fmt.Sprintf("%x", sha256.Sum256(kubeconfig)), nil
}

func selectExternalSecretsVersion(requestedVersion string, pc *apiv1alpha1.ProviderConfig) (apiv1alpha1.ExternalSecretsVersion, error) {
	for _, configVersion := range pc.Spec.Versions {
		if configVersion.Version == requestedVersion {
			return configVersion, nil
		}
	}
	return apiv1alpha1.ExternalSecretsVersion{}, fmt.Errorf("%w: requested version (%s) is not available", ctrlerrors.ErrInvalidUserInput, requestedVersion)
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
			l.Error(res.Error, "reconcile error", "objectID", externalsecrets.ObjectID(obj))
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

func installationNamespace(defaultNamespace, override, tenantNamespace string, onPlatform bool) string {
	if onPlatform {
		return tenantNamespace
	}
	if override != "" {
		return override
	}
	return defaultNamespace
}

func (r *ExternalSecretsOperatorReconciler) configureRemoteVersion(ctx context.Context, active bool, version *apiv1alpha1.ExternalSecretsVersion, namespace string, key client.ObjectKey) error {
	hash := ""
	var err error
	if active {
		hash, err = r.mcpCredentialHash(ctx, key)
		if err != nil {
			return err
		}
	}
	version.HelmValues, err = externalsecrets.ConfigureRemoteMCPControllers(version.HelmValues, externalsecrets.RemoteCredentialName, namespace, hash)
	return err
}
