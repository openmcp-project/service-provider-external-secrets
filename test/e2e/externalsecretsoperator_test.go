package e2e

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"
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
)

const mcpA = "mcp-a"
const mcpB = "mcp-b"

func TestServiceProvider(t *testing.T) {
	var onboardingObjects unstructured.UnstructuredList
	var mcpAObjects unstructured.UnstructuredList
	var mcpBObjects unstructured.UnstructuredList
	basicProviderTest := features.New("provider test").
		Setup(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			if _, err := resources.CreateObjectsFromDir(ctx, c, "platform"); err != nil {
				t.Errorf("failed to create platform cluster objects: %v", err)
			}
			return ctx
		}).
		Setup(providers.CreateMCP(mcpA)).
		Setup(providers.CreateMCP(mcpB)).
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
			// tenant A uses an ESO version with namespace override (see ProviderConfig)
			imagePullSecret.SetNamespace("eso-system")
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
		Teardown(cleanupMCPDomainObjects(mcpA, &mcpAObjects)).
		Teardown(cleanupMCPDomainObjects(mcpB, &mcpBObjects)).
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
