package externalsecrets

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestManagePullSecrets(t *testing.T) {
	fakeCluster := CreateFakeCluster(t, "platform", &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "source",
		},
		Data: map[string][]byte{
			"test": []byte("testdata"),
		},
		Type: corev1.SecretTypeDockerConfigJson,
	})
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		targetCluster    ManagedCluster
		imagePullSecrets []corev1.LocalObjectReference
		config           SecretCopyConfig
	}{
		{
			name:          "sync secret test from namespace source to namespace target",
			targetCluster: NewManagedCluster(fakeCluster, &rest.Config{}, "source", PlatformCluster),
			imagePullSecrets: []corev1.LocalObjectReference{
				{
					Name: "test",
				},
			},
			config: SecretCopyConfig{
				SourceClient:    fakeCluster.Client(),
				SourceNamespace: "source",
				TargetNamespace: "target",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ManagePullSecrets(tt.targetCluster, tt.imagePullSecrets, tt.config)
			ExecApply(t, []ManagedCluster{tt.targetCluster}, 1, []string{})
			for _, v := range tt.imagePullSecrets {
				sourceSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      v.Name,
						Namespace: tt.config.SourceNamespace,
					},
				}
				targetSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      v.Name,
						Namespace: tt.config.TargetNamespace,
					},
				}
				require.NoError(t, tt.config.SourceClient.Get(context.TODO(), client.ObjectKeyFromObject(sourceSecret), sourceSecret))
				require.NoError(t, tt.config.SourceClient.Get(context.TODO(), client.ObjectKeyFromObject(targetSecret), targetSecret))
				assert.Equal(t, sourceSecret.Data, targetSecret.Data)
				assert.Equal(t, corev1.SecretTypeDockerConfigJson, targetSecret.Type, "target secret should have the correct type")
			}
		})
	}
}
