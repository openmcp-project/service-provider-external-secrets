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
		params ManageFluxResourcesParams
	}{
		{
			name: "APIObject and ProviderConfig values mapping",
			params: ManageFluxResourcesParams{
				Cluster:      NewManagedCluster(CreateFakeCluster(t, "platform"), &rest.Config{}, testNamespace, PlatformCluster),
				MCPNamespace: "external-secrets",
				Obj: &apiv1alpha1.ExternalSecretsOperator{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: testNamespace,
					},
					Spec: apiv1alpha1.ExternalSecretsOperatorSpec{
						Version: "v2.2.0",
					},
				},
				ProviderConfig: &apiv1alpha1.ProviderConfig{
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
				ClusterContext: spruntime.ClusterContext{
					MCPAccessSecretKey: client.ObjectKey{
						Namespace: testNamespace,
						Name:      testKubeconfigKey,
					},
				},
				ChartPullSecretName: "secret-copy",
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ManageFluxResources(tt.params)
			// expect oci repo and helm release without errors
			ExecApply(t, []ManagedCluster{tt.params.Cluster}, 2, []string{})

			// assert oci repo
			ociRepo := &sourcev1.OCIRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.params.Obj.Name,
					Namespace: testNamespace,
				},
			}
			require.NoError(t, tt.params.Cluster.GetClient().Get(context.TODO(), client.ObjectKeyFromObject(ociRepo), ociRepo))
			assert.Equal(t, tt.params.ProviderConfig.Spec.ChartURL, ptr.To(ociRepo.Spec.URL))
			assert.Equal(t, tt.params.ChartPullSecretName, ociRepo.Spec.SecretRef.Name)
			assert.Equal(t, tt.params.Obj.Spec.Version, ociRepo.Spec.Reference.Tag)
			assert.Equal(t, tt.params.ProviderConfig.Spec.PollInterval.Duration, ociRepo.Spec.Interval.Duration)

			// assert helm release
			helmRelease := &helmv2.HelmRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.params.Obj.Name,
					Namespace: testNamespace,
				},
			}
			require.NoError(t, tt.params.Cluster.GetClient().Get(context.TODO(), client.ObjectKeyFromObject(helmRelease), helmRelease))
			assert.Equal(t, tt.params.ProviderConfig.Spec.HelmValues, helmRelease.Spec.Values)
			assert.Equal(t, tt.params.ProviderConfig.Spec.PollInterval.Duration, helmRelease.Spec.Interval.Duration)
			assert.Equal(t, tt.params.MCPNamespace, helmRelease.Spec.StorageNamespace)
			assert.Equal(t, tt.params.MCPNamespace, helmRelease.Spec.TargetNamespace)
			assert.Equal(t, tt.params.ClusterContext.MCPAccessSecretKey.Name, helmRelease.Spec.KubeConfig.SecretRef.Name)
			assert.Equal(t, "kubeconfig", helmRelease.Spec.KubeConfig.SecretRef.Key)
		})
	}
}
