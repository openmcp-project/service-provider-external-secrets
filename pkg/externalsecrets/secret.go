package externalsecrets

import (
	"context"

	openmcpresources "github.com/openmcp-project/controller-utils/pkg/resources"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SecretCopyConfig holds the configuration for copying a secret.
type SecretCopyConfig struct {
	// SourceClient is the client to read the source secret from.
	SourceClient client.Client
	// SourceNamespace is the namespace/name of the source secret.
	SourceNamespace string
	// TargetNamespace is the namespace/name of the target secret.
	TargetNamespace string
}

// ManagePullSecrets syncs every image pull secret the to cluster
func ManagePullSecrets(targetCluster ManagedCluster, imagePullSecrets []corev1.LocalObjectReference, config SecretCopyConfig) {
	for _, pullSecret := range imagePullSecrets {
		secret := NewManagedObject(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pullSecret.Name,
				Namespace: config.TargetNamespace,
			},
		}, ManagedObjectContext{
			ReconcileFunc: func(ctx context.Context, o client.Object) error {
				oSecret := o.(*corev1.Secret)
				sourceSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pullSecret.Name,
						Namespace: config.SourceNamespace,
					},
				}
				// retrieve source secret from platform cluster
				if err := config.SourceClient.Get(ctx, client.ObjectKeyFromObject(sourceSecret), sourceSecret); err != nil {
					return err
				}
				mutator := openmcpresources.NewSecretMutator(pullSecret.Name, config.TargetNamespace, sourceSecret.Data, corev1.SecretTypeDockerConfigJson)
				return mutator.Mutate(oSecret)
			},
			StatusFunc: SimpleStatus,
		})
		targetCluster.AddObject(secret)
	}
}
