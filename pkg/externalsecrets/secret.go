package externalsecrets

import (
	"context"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	openmcpresources "github.com/openmcp-project/controller-utils/pkg/resources"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SyncPullSecrets syncs every image pull secret defined in the provider config helm values to the managed control plane cluster
func SyncPullSecrets(mcp ManagedCluster, platformCluster *clusters.Cluster, helmValues HelmValues, sourceNamespace string) {
	if len(helmValues.ImagePullSecrets) == 0 {
		return
	}
	for _, pullSecret := range helmValues.ImagePullSecrets {
		secret := NewManagedObject(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pullSecret.Name,
				Namespace: mcp.GetDefaultNamespace(),
			},
		}, ManagedObjectContext{
			ReconcileFunc: func(ctx context.Context, o client.Object) error {
				oSecret := o.(*corev1.Secret)
				sourceSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pullSecret.Name,
						Namespace: sourceNamespace,
					},
				}
				// retrieve source secret from platform cluster
				if err := platformCluster.Client().Get(ctx, client.ObjectKeyFromObject(sourceSecret), sourceSecret); err != nil {
					return err
				}
				mutator := openmcpresources.NewSecretMutator(pullSecret.Name, mcp.GetDefaultNamespace(), sourceSecret.Data, corev1.SecretTypeDockerConfigJson)
				return mutator.Mutate(oSecret)
			},
			StatusFunc: SimpleStatus,
		})
		mcp.AddObject(secret)
	}
}
