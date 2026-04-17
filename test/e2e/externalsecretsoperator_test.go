package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
		Assess("Platform Cluster: chart pull secret sync to tenant namespaces", chartSecretSynced("sp-eso-privateregcred")).
		Assess("MCP A: image pull secrets are synced", imagePullSecretSynced(mcpA, client.ObjectKey{Name: "privateregcred", Namespace: "eso-system"})).
		Assess("MCP B: image pull secrets are synced", imagePullSecretSynced(mcpB, client.ObjectKey{Name: "privateregcred", Namespace: "eso-system"})).
		Assess("MCP A: domain objects can be created", createSecretStoreAndExternalSecret(mcpA, &mcpAObjects)).
		Assess("MCP B: domain objects can be created", createSecretStoreAndExternalSecret(mcpB, &mcpBObjects)).
		Assess("MCP A: secret created from fake secret store", validateExternalSecret(mcpA)).
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

// verify given secret exists on mcp
func imagePullSecretSynced(mcpName string, secret client.ObjectKey) features.Func {
	return func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		mcp, err := clusterutils.MCPConfig(ctx, c, mcpName)
		if err != nil {
			t.Error(err)
			return ctx
		}
		secList := &corev1.SecretList{
			Items: []corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secret.Name,
						Namespace: secret.Namespace,
					},
				},
			},
		}
		if err := wait.For(conditions.New(mcp.Client().Resources()).ResourcesFound(secList)); err != nil {
			t.Error(err)
		}
		return ctx
	}
}

// verify given secret exists in every tenant namespace on the platform cluster
func chartSecretSynced(secretName string) features.Func {
	return func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		secList := &corev1.SecretList{}
		namespaces := &corev1.NamespaceList{}
		if err := c.Client().Resources().List(ctx, namespaces); err != nil {
			t.Error(err)
			return ctx
		}
		for _, ns := range namespaces.Items {
			if !strings.HasPrefix(ns.Name, "mcp--") {
				continue
			}
			secList.Items = append(secList.Items, corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: ns.Name,
				},
			})
		}
		if err := wait.For(conditions.New(c.Client().Resources()).ResourcesFound(secList)); err != nil {
			t.Error(err)
		}
		return ctx
	}
}
