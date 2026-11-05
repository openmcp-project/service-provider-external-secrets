package e2e

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"
	"github.com/openmcp-project/service-provider-external-secrets/api/v1alpha1"
	"github.com/openmcp-project/service-provider-external-secrets/pkg/externalsecrets"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"github.com/openmcp-project/openmcp-testing/pkg/clusterutils"
	openmcpconditions "github.com/openmcp-project/openmcp-testing/pkg/conditions"
	"github.com/openmcp-project/openmcp-testing/pkg/providers"
	"github.com/openmcp-project/openmcp-testing/pkg/resources"
	"github.com/openmcp-project/openmcp-testing/pkg/setup/extensions/prometheus"
)

const mcpA = "mcp-a"
const mcpB = "mcp-b"

func TestServiceProvider(t *testing.T) {
	var onboardingObjects unstructured.UnstructuredList
	var mcpAObjects unstructured.UnstructuredList
	var mcpBObjects unstructured.UnstructuredList
	var perofmanceTest prometheus.Prometheus
	basicProviderTest := features.New("provider test").
		Setup(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			if _, err := resources.CreateObjectsFromDir(ctx, c, "platform"); err != nil {
				t.Errorf("failed to create platform cluster objects: %v", err)
			}
			return ctx
		}).
		Setup(providers.CreateMCP(mcpA)).
		Setup(providers.CreateMCP(mcpB)).
		Setup(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			if err := perofmanceTest.AddClusters(ctx, c, prometheus.TargetCluster{
				Name:  mcpA, // control plane name
				Label: "controlplane-a",
			}); err != nil {
				t.Errorf("add scrape job for test control plane failed: %v", err)
			}
			return ctx
		}).
		Assess("verify service can be successfully consumed",
			func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
				onboardingConfig, err := clusterutils.OnboardingConfig()
				if err != nil {
					t.Error(err)
					return ctx
				}
				objList, err := resources.CreateObjectsFromDir(ctx, onboardingConfig, "onboarding")
				if err != nil {
					t.Errorf("failed to create onboarding cluster objects: %v", err)
					return ctx
				}
				for _, obj := range objList.Items {
					if err := wait.For(openmcpconditions.Match(&obj, onboardingConfig, "Ready", corev1.ConditionTrue)); err != nil {
						t.Error(err)
					}
				}
				objList.DeepCopyInto(&onboardingObjects)
				return ctx
			},
		).
		Assess("platform cluster resources tenant A", assesPlatformResources(mcpA, "sp-eso-privateregcred")).
		Assess("platform cluster resources tenant B", assesPlatformResources(mcpB, "")).
		Assess("ManagedControlPlane resources have been created", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			// we only need to test tenant A because B uses an ESO version without pull secrets (see ProviderConfig)
			mcp, err := clusterutils.MCPConfig(ctx, c, mcpA)
			if err != nil {
				t.Error(err)
				return ctx
			}
			imagePullSecret := &corev1.Secret{}
			imagePullSecret.SetName("privateregcred")
			imagePullSecret.SetNamespace(externalsecrets.DefaultNamespace)
			list := &corev1.SecretList{
				Items: []corev1.Secret{*imagePullSecret},
			}
			if err := wait.For(conditions.New(mcp.Client().Resources()).ResourcesFound(list), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("image pull secret not found on control plane: %v", err)
			}
			return ctx
		}).
		Assess("MCP A: domain objects can be created", createSecretStoreAndExternalSecret(mcpA, &mcpAObjects)).
		Assess("MCP A: secret created from fake secret store", validateExternalSecret(mcpA)).
		Assess("MCP B: domain objects can be created", createSecretStoreAndExternalSecret(mcpB, &mcpBObjects)).
		Assess("MCP B: secret created from fake secret store", validateExternalSecret(mcpB)).
		Assess("upgrade to latest", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			eso := &v1alpha1.ExternalSecretsOperator{}
			onboardingClient, err := clusterutils.OnboardingConfig()
			if err != nil {
				t.Errorf("failed to retrieve onboarding client: %v", err)
				return ctx
			}
			v1alpha1.AddToScheme(onboardingClient.Client().Resources().GetScheme())
			if err := onboardingClient.Client().Resources().Get(ctx, mcpA, corev1.NamespaceDefault, eso); err != nil {
				t.Errorf("failed to retrieve eso resource: %v", err)
				return ctx
			}
			eso.Spec.Version = "latest"
			if err := onboardingClient.Client().Resources().Update(ctx, eso); err != nil {
				t.Errorf("failed to update eso to latest version: %v", err)
				return ctx
			}
			if err := wait.For(openmcpconditions.Match(eso, onboardingClient, "Ready", corev1.ConditionTrue)); err != nil {
				t.Errorf("eso update to latest not ready: %v", err)
			}
			// todo add wait for scrape interval
			// wait one scrape intervals to ensure most queries will work (probably better to keep this out of the setup but let the user determine the wait interval)
			klog.Info("waiting one scrape interval")
			select {
			case <-time.After(time.Second * 30):
			case <-ctx.Done():
			}
			return ctx
		}).
		Assess("service provider performance (eso)", benchmark("sp-externalsecretsoperator", "externalsecretsoperator", prometheus.AllQueries)).
		Assess("domain service performance (eso)", benchmark("external-secrets", "", []prometheus.NamedQuery{
			prometheus.ContainerAverageCPUUsage,
			prometheus.ContainerAverageMemoryUsage,
		})).
		Teardown(cleanupMCPDomainObjects(mcpA, &mcpAObjects)).
		Teardown(cleanupMCPDomainObjects(mcpB, &mcpBObjects)).
		Teardown(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			klog.Info("cleanup of performance test")
			if err := perofmanceTest.Cleanup(ctx, c); err != nil {
				t.Error(err)
			}
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			onboardingConfig, err := clusterutils.OnboardingConfig()
			if err != nil {
				t.Error(err)
				return ctx
			}
			for _, obj := range onboardingObjects.Items {
				if err := resources.DeleteObject(ctx, onboardingConfig, &obj, wait.WithTimeout(time.Minute)); err != nil {
					t.Errorf("failed to delete onboarding object: %v", err)
				}
			}
			return ctx
		}).
		Teardown(providers.DeleteMCP(mcpA, wait.WithTimeout(5*time.Minute))).
		Teardown(providers.DeleteMCP(mcpB, wait.WithTimeout(5*time.Minute)))
	testenv.Test(t, basicProviderTest.Feature())
}

func assesPlatformResources(mcpName, chartPullSecret string) features.Func {
	return func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		tenantNamespace, err := libutils.StableMCPNamespace(mcpName, "default")
		if err != nil {
			t.Errorf("failed to get tenant namespace: %v", err)
			return ctx
		}
		ociRepo := &sourcev1.OCIRepository{}
		ociRepo.SetName(externalsecrets.OCIRepositoryName)
		ociRepo.SetNamespace(tenantNamespace)
		if err := wait.For(openmcpconditions.Match(ociRepo, c, "Ready", corev1.ConditionTrue), wait.WithTimeout(2*time.Minute)); err != nil {
			t.Errorf("OCIRepository not ready: %v", err)
		}
		helmRelease := &helmv2.HelmRelease{}
		helmRelease.SetName(externalsecrets.HelmReleaseName)
		helmRelease.SetNamespace(tenantNamespace)
		if err := wait.For(openmcpconditions.Match(helmRelease, c, "Ready", corev1.ConditionTrue), wait.WithTimeout(2*time.Minute)); err != nil {
			t.Errorf("HelmRelease not ready: %v", err)
		}
		if chartPullSecret != "" {
			chartSecret := &corev1.Secret{}
			chartSecret.SetName(chartPullSecret)
			chartSecret.SetNamespace(tenantNamespace)
			pullSecrets := &corev1.SecretList{
				Items: []corev1.Secret{*chartSecret},
			}
			if err := wait.For(conditions.New(c.Client().Resources()).ResourcesFound(pullSecrets), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("pull secret not found: %v", err)
			}
		}
		return ctx
	}
}

func createSecretStoreAndExternalSecret(mcpName string, mcpList *unstructured.UnstructuredList) features.Func {
	return func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		mcp, err := clusterutils.MCPConfig(ctx, c, mcpName)
		if err != nil {
			return ctx
		}
		objList, err := resources.CreateObjectsFromDir(ctx, mcp, "mcp")
		if err != nil {
			t.Errorf("failed to create mcp cluster objects: %v", err)
			return ctx
		}
		if err := wait.For(conditions.New(mcp.Client().Resources()).ResourcesFound(objList)); err != nil {
			t.Error(err)
			return ctx
		}
		objList.DeepCopyInto(mcpList)
		return ctx
	}
}

func validateExternalSecret(mcpName string) features.Func {
	return func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		mcp, err := clusterutils.MCPConfig(ctx, c, mcpName)
		if err != nil {
			t.Error(err)
			return ctx
		}
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "secret-to-be-created",
				Namespace: corev1.NamespaceDefault,
			},
		}
		if err := wait.For(conditions.New(mcp.Client().Resources()).ResourceMatch(sec, func(object k8s.Object) bool {
			secret := object.(*corev1.Secret)
			data := secret.Data
			return string(data["foo_bar"]) == "HELLO1" && string(data["john"]) == "doe"
		})); err != nil {
			t.Error(err)
		}
		return ctx
	}
}

func cleanupMCPDomainObjects(mcpName string, mcpList *unstructured.UnstructuredList) features.Func {
	return func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		mcp, err := clusterutils.MCPConfig(ctx, c, mcpName)
		if err != nil {
			t.Error(err)
			return ctx
		}
		for _, obj := range mcpList.Items {
			if err := resources.DeleteObject(ctx, mcp, &obj, wait.WithTimeout(time.Minute)); err != nil {
				t.Errorf("failed to delete mcp object: %v", err)
			}
		}
		return ctx
	}
}

func benchmark(container, controller string, queries []prometheus.NamedQuery) features.Func {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		window := prometheus.ObservabilityWindow{
			Start:         time.Now().Add(-15 * time.Minute),
			End:           time.Now(),
			ContainerName: container,
			Controller:    controller,
		}
		results, err := prometheus.Query(ctx, cfg, queries, window, "", "query-"+container)
		if err != nil {
			t.Errorf("promQL query failed: %v", err)
		}
		klog.Infof("query results for container %s", container)
		for _, qr := range results {
			klog.Infof("%s\n", qr)
		}
		return ctx
	}
}
