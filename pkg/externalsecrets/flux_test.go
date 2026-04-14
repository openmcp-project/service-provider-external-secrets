package externalsecrets

import (
	"context"
	"testing"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1alpha1 "github.com/openmcp-project/service-provider-external-secrets/api/v1alpha1"
	"github.com/openmcp-project/service-provider-external-secrets/pkg/spruntime"
)

const (
	testNamespace       = "test"
	testCharURL         = "chart-url"
	testChartPullSecret = "chart-pull-secret"
	testKubeconfigKey   = "kubeconfig-key"
)

func TestManageFluxResources(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		cluster                  ManagedCluster
		externalSecretsNamespace string
		obj                      *apiv1alpha1.ExternalSecretsOperator
		pc                       *apiv1alpha1.ProviderConfig
		cc                       spruntime.ClusterContext
	}{
		{
			name:                     "APIObject and ProviderConfig values mapping",
			cluster:                  NewManagedCluster(CreateFakeCluster(t, "platform"), &rest.Config{}, testNamespace, PlatformCluster),
			externalSecretsNamespace: "external-secrets",
			obj: &apiv1alpha1.ExternalSecretsOperator{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: testNamespace,
				},
				Spec: apiv1alpha1.ExternalSecretsOperatorSpec{
					Version: "v2.2.0",
				},
			},
			pc: &apiv1alpha1.ProviderConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: testNamespace,
				},
				Spec: apiv1alpha1.ProviderConfigSpec{
					ChartURL:        new(testCharURL),
					ChartPullSecret: new(testChartPullSecret),
					HelmValues: &apiextensionv1.JSON{
						Raw: []byte(`{"foo":"bar"}`),
					},
					PollInterval: &metav1.Duration{
						Duration: time.Hour,
					},
				},
			},
			cc: spruntime.ClusterContext{
				MCPAccessSecretKey: client.ObjectKey{
					Namespace: testNamespace,
					Name:      testKubeconfigKey,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ManageFluxResources(tt.cluster, tt.externalSecretsNamespace, tt.obj, tt.pc, tt.cc)
			// expect oci repo and helm release without errors
			ExecApply(t, []ManagedCluster{tt.cluster}, 2, []string{})

			// assert oci repo
			ociRepo := &sourcev1.OCIRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.obj.Name,
					Namespace: testNamespace,
				},
			}
			require.NoError(t, tt.cluster.GetClient().Get(context.TODO(), client.ObjectKeyFromObject(ociRepo), ociRepo))
			assert.Equal(t, tt.pc.Spec.ChartURL, ptr.To(ociRepo.Spec.URL))
			assert.Equal(t, tt.pc.Spec.ChartPullSecret, ptr.To(ociRepo.Spec.SecretRef.Name))
			assert.Equal(t, tt.obj.Spec.Version, ociRepo.Spec.Reference.Tag)
			assert.Equal(t, tt.pc.Spec.PollInterval.Duration, ociRepo.Spec.Interval.Duration)

			// assert helm release
			helmRelease := &helmv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.obj.Name,
					Namespace: testNamespace,
				},
			}
			require.NoError(t, tt.cluster.GetClient().Get(context.TODO(), client.ObjectKeyFromObject(helmRelease), helmRelease))
			assert.Equal(t, tt.pc.Spec.HelmValues, helmRelease.Spec.Values)
			assert.Equal(t, tt.pc.Spec.PollInterval.Duration, helmRelease.Spec.Interval.Duration)
			assert.Equal(t, tt.externalSecretsNamespace, helmRelease.Spec.StorageNamespace)
			assert.Equal(t, tt.externalSecretsNamespace, helmRelease.Spec.TargetNamespace)
			assert.Equal(t, tt.cc.MCPAccessSecretKey.Name, helmRelease.Spec.KubeConfig.SecretRef.Name)
			assert.Equal(t, "kubeconfig", helmRelease.Spec.KubeConfig.SecretRef.Key)
		})
	}
}
