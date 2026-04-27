package e2e

import (
	"context"
	"testing"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"
	"github.com/openmcp-project/openmcp-testing/pkg/clusterutils"
	openmcpconditions "github.com/openmcp-project/openmcp-testing/pkg/conditions"
	"github.com/openmcp-project/openmcp-testing/pkg/providers"
	"github.com/openmcp-project/openmcp-testing/pkg/resources"
	"github.com/openmcp-project/service-provider-external-secrets/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

func TestRenaming(t *testing.T) {
	// we keep track of all managed objects to verify the remain the same when switching to the new controller, e.g. uid (object itself hasn't been recreated) + generation (spec stayed the same) are untouched
	// status + metadata (labels/annotations) updates should be ok, else we could also compare the complete object or a computed hash
	var helmRelease *helmv2.HelmRelease
	var ociRepo *sourcev1.OCIRepository
	var eso *v1alpha1.ExternalSecretsOperator
	// var managedSecrets []corev1.Secret
	basicProviderTest := features.New("provider test").
		Setup(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			if _, err := resources.CreateObjectsFromDir(ctx, c, "platform"); err != nil {
				t.Errorf("failed to create platform cluster objects: %v", err)
			}
			return ctx
		}).
		// 1. create mcp
		Setup(providers.CreateMCP(mcpA)).
		// 2. consume old (api group) onboarding api with old provider
		Setup(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			onboardingConfig, err := clusterutils.OnboardingConfig()
			if err != nil {
				t.Error(err)
				return ctx
			}
			_, err = resources.CreateObjectsFromDir(ctx, onboardingConfig, "onboarding")
			if err != nil {
				t.Errorf("failed to create onboarding cluster objects: %v", err)
				return ctx
			}
			oldApiObject := &unstructured.Unstructured{}
			oldApiObject.SetName(mcpA)
			oldApiObject.SetNamespace(corev1.NamespaceDefault)
			oldApiObject.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "external-secrets.services.openmcp.cloud",
				Version: "v1alpha1",
				Kind:    "ExternalSecretsOperator",
			})

			if err := wait.For(openmcpconditions.Match(oldApiObject, onboardingConfig, "Ready", corev1.ConditionTrue)); err != nil {
				t.Errorf("external secrets operator not ready: %v", err)
				return ctx
			}

			if err := onboardingConfig.Client().Resources().Get(ctx, oldApiObject.GetName(), oldApiObject.GetNamespace(), oldApiObject); err != nil {
				t.Errorf("failed to get api object: %v", err)
				return ctx
			}
			eso = &v1alpha1.ExternalSecretsOperator{}
			eso.SetName(oldApiObject.GetName())
			eso.SetNamespace(oldApiObject.GetNamespace())
			eso.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "external-secrets.services.open-control-plane.io",
				Version: oldApiObject.GetObjectKind().GroupVersionKind().Version,
				Kind:    oldApiObject.GetKind(),
			})
			esoVersion, _, err := unstructured.NestedFieldCopy(oldApiObject.Object, "spec", "version")
			if err != nil {
				t.Errorf("failed to retrieve old api object spec: %v", err)
				return ctx
			}
			eso.Spec = v1alpha1.ExternalSecretsOperatorSpec{
				Version: esoVersion.(string),
			}
			return ctx
		},
		).
		Assess("verify that helm release and oci repository are ready", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			tenantNamespace, err := libutils.StableMCPNamespace(mcpA, "default")
			if err != nil {
				t.Errorf("failed to get tenant namespace: %v", err)
				return ctx
			}

			helmRelease = &helmv2.HelmRelease{}
			helmRelease.SetName(mcpA)
			helmRelease.SetNamespace(tenantNamespace)

			ociRepo = &sourcev1.OCIRepository{}
			ociRepo.SetName(mcpA)
			ociRepo.SetNamespace(tenantNamespace)

			chartSecret := &corev1.Secret{}
			chartSecret.SetName("sp-eso-privateregcred")
			chartSecret.SetNamespace(tenantNamespace)
			pullSecrets := &corev1.SecretList{
				Items: []corev1.Secret{*chartSecret},
			}

			if err := wait.For(openmcpconditions.Match(helmRelease, config, "Ready", corev1.ConditionTrue), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("HelmRelease not ready: %v", err)
			}
			if err := wait.For(openmcpconditions.Match(ociRepo, config, "Ready", corev1.ConditionTrue), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("OCIRepository not ready: %v", err)
			}
			if err := wait.For(conditions.New(config.Client().Resources()).ResourcesFound(pullSecrets), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Fatalf("pull secret not found: %v", err)
			}
			if err := config.Client().Resources().Get(ctx, ociRepo.GetName(), ociRepo.GetNamespace(), ociRepo); err != nil {
				t.Fatalf("failed to get object: %v", err)
			}
			if err := config.Client().Resources().Get(ctx, helmRelease.GetName(), helmRelease.GetNamespace(), helmRelease); err != nil {
				t.Fatalf("failed to get object: %v", err)
			}

			return ctx
		}).
		// 3. delete old provider -> this does not affect existing old api onboarding resources
		// -> unlike init, no offboarding job is scheduled when a service provider is deleted
		Assess("delete old service provider", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			if err := providers.DeleteServiceProvider(ctx, c, "externalsecretsoperator-old"); err != nil {
				t.Errorf("failed to delete old service provider: %v", err)
			}
			return ctx
		}).
		// 4. make sure managed objects stayed the same, e.g. uid (object itself hasn't been recreated) + generation (spec stayed the same) are untouched
		// status + metadata (labels/annotations) updates should be ok, else we could also compare the complete object or a computed hash
		Assess("oci repo and helm release are not deleted", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			assertObject(ctx, t, c, ociRepo, expectedParameters{generation: 1, uid: ociRepo.GetUID()})
			assertObject(ctx, t, c, helmRelease, expectedParameters{generation: 1, uid: helmRelease.GetUID()})
			return ctx
		}).
		// 5. create eso object with new api group based on existing old api resource
		Assess("create new api group object based on old api object", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			onboardingConfig, err := clusterutils.OnboardingConfig()
			if err != nil {
				t.Errorf("failed to retrieve onboarding config: %v", err)
				return ctx
			}
			v1alpha1.AddToScheme(onboardingConfig.Client().Resources().GetScheme())
			if err := onboardingConfig.Client().Resources().Create(ctx, eso); err != nil {
				t.Fatalf("failed to create new eso object: %v", err)
			}
			return ctx
		}).
		Assess("oci repo and helm release are still the same", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			assertObject(ctx, t, c, ociRepo, expectedParameters{generation: 1, uid: ociRepo.GetUID()})
			assertObject(ctx, t, c, helmRelease, expectedParameters{generation: 1, uid: helmRelease.GetUID()})
			return ctx
		}).
		// 6. use new eso api with new service provider
		Assess("update new eso api to demonstrate existing flux objects are picked up", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			onboardingConfig, err := clusterutils.OnboardingConfig()
			if err != nil {
				t.Errorf("failed to retrieve onboarding config: %v", err)
				return ctx
			}
			v1alpha1.AddToScheme(onboardingConfig.Client().Resources().GetScheme())
			if err := onboardingConfig.Client().Resources().Get(ctx, eso.Name, eso.Namespace, eso); err != nil {
				t.Fatalf("failed to get new eso obj: %v", err)
			}
			eso.Spec.Version = "2.2.0"
			if err := onboardingConfig.Client().Resources().Update(ctx, eso); err != nil {
				t.Fatalf("failed to update new eso obj: %v", err)
			}
			// wait until domain service is ready with new eso version
			if err := wait.For(openmcpconditions.Match(eso, onboardingConfig, "Ready", corev1.ConditionTrue)); err != nil {
				t.Errorf("external secrets operator not ready: %v", err)
				return ctx
			}

			// 6. make sure managed objects stayed the same, e.g. uid (object itself hasn't been recreated) + generation (spec stayed the same) are untouched
			// status + metadata (labels/annotations) updates should be ok, else we could also compare the complete object or a computed hash
			assertObject(ctx, t, c, ociRepo, expectedParameters{generation: 2, uid: ociRepo.GetUID()})
			assertObject(ctx, t, c, helmRelease, expectedParameters{generation: 1, uid: helmRelease.GetUID()})
			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			onboardingConfig, err := clusterutils.OnboardingConfig()
			if err != nil {
				t.Error(err)
				return ctx
			}
			v1alpha1.AddToScheme(onboardingConfig.Client().Resources().GetScheme())
			if err := resources.DeleteObject(ctx, onboardingConfig, eso, wait.WithTimeout(time.Minute)); err != nil {
				t.Errorf("failed to delete onboarding object: %v", err)
			}
			return ctx
		}).
		Teardown(providers.DeleteMCP(mcpA, wait.WithTimeout(5*time.Minute)))
	testenv.Test(t, basicProviderTest.Feature())

	// (stop reconciliation with old provider - not implemented right now but also not required because of 3)
	// (otherwise two controller try to manage the same resources which would impact tenant resources when deleting the old api)

	// 6. cleanup old resources and crd on the onboarding cluster (manual step in real environment)
	// 7. cleanup old provider config resources + crd on the platform cluster (manual step in real environment)
}

type expectedParameters struct {
	generation int64
	uid        types.UID
}

func assertObject(ctx context.Context, t *testing.T, c *envconf.Config, obj k8s.Object, exp expectedParameters) {
	objCopy := obj.DeepCopyObject().(k8s.Object)
	if err := c.Client().Resources().Get(ctx, obj.GetName(), obj.GetNamespace(), objCopy); err != nil {
		t.Errorf("failed to get object: %v", err)
		return
	}
	assert.Equal(t, exp.generation, objCopy.GetGeneration())
	assert.Equal(t, exp.uid, objCopy.GetUID())
}
