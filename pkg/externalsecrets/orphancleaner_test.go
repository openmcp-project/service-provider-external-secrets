package externalsecrets

import (
	"context"
	"errors"
	"slices"
	"testing"

	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_orphanCleaner_Cleanup(t *testing.T) {
	tests := []struct {
		name            string // description of this test case
		targetNamespace string
		cluster         ManagedCluster
		cleanerType     cleanerType[*sourcev1.OCIRepositoryList]
		want            []sourcev1.OCIRepository
		wantErr         bool
		wantResultErr   bool
	}{
		{
			name:            "only OCIRepository managed by sp-eso are deleted",
			targetNamespace: DefaultNamespace,
			cluster: createFakeCluster(createFakeClient([]client.Object{
				createOCIRepo("managed", DefaultNamespace, true),
				createOCIRepo("unmanaged", DefaultNamespace, false),
			})),
			cleanerType: createOCIRepoCleanerType(),
			want: []sourcev1.OCIRepository{
				*createOCIRepo("unmanaged", DefaultNamespace, false),
			},
			wantErr: false,
		},
		{
			name:            "OCIRepository in other namespaces are not deleted",
			targetNamespace: "openmcp-system",
			cluster: createFakeCluster(createFakeClient([]client.Object{
				createOCIRepo("managed", DefaultNamespace, true),
				createOCIRepo("unmanaged", DefaultNamespace, false),
			})),
			cleanerType: createOCIRepoCleanerType(),
			want: []sourcev1.OCIRepository{
				*createOCIRepo("managed", DefaultNamespace, true),
				*createOCIRepo("unmanaged", DefaultNamespace, false),
			},
			wantErr: false,
		},
		{
			name:            "objects to keep are not deleted",
			targetNamespace: "openmcp-system",
			cluster: createFakeCluster(createFakeClient([]client.Object{
				createOCIRepo("managed", DefaultNamespace, true),
				createOCIRepo("unmanaged", DefaultNamespace, false),
			})),
			cleanerType: createOCIRepoCleanerType(corev1.LocalObjectReference{Name: "managed"}),
			want: []sourcev1.OCIRepository{
				*createOCIRepo("managed", DefaultNamespace, true),
				*createOCIRepo("unmanaged", DefaultNamespace, false),
			},
			wantErr: false,
		},
		{
			name:            "error is returned when list fails",
			targetNamespace: DefaultNamespace,
			cluster:         createFakeCluster(listErrorClient{}),
			cleanerType:     createOCIRepoCleanerType(),
			want:            []sourcev1.OCIRepository{},
			wantErr:         true,
		},
		{
			name:            "if a single delete fails, the single contains an error but overall cleanup succeeds",
			targetNamespace: DefaultNamespace,
			cluster: createFakeCluster(deleteErrorClient{
				fakeOCIRepo: *createOCIRepo("managed", DefaultNamespace, true),
			}),
			cleanerType:   createOCIRepoCleanerType(),
			want:          []sourcev1.OCIRepository{},
			wantErr:       false,
			wantResultErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewOrphanCleaner(tt.cluster, tt.targetNamespace, tt.cleanerType)
			result, gotErr := c.Cleanup(context.Background())
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("Cleanup() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("Cleanup() succeeded unexpectedly")
			}
			if tt.wantResultErr {
				assert.True(t, slices.ContainsFunc(result, containsResultError))
				return
			}
			repoList := &sourcev1.OCIRepositoryList{}
			require.NoError(t, tt.cluster.GetClient().List(context.Background(), repoList))
			assert.Len(t, tt.want, len(repoList.Items))
			for _, repo := range repoList.Items {
				assert.True(t, slices.ContainsFunc(tt.want, func(r sourcev1.OCIRepository) bool {
					return r.Name == repo.Name && r.Namespace == repo.Namespace
				}))
			}
			assert.False(t, slices.ContainsFunc(result, containsResultError))
		})
	}
}

var containsResultError = func(r Result) bool { return r.Error != nil }

func createOCIRepoCleanerType(objectsToKeep ...corev1.LocalObjectReference) cleanerType[*sourcev1.OCIRepositoryList] {
	return cleanerType[*sourcev1.OCIRepositoryList]{
		EmptyList: func() *sourcev1.OCIRepositoryList {
			return &sourcev1.OCIRepositoryList{}
		},
		ObjectsToKeep: objectsToKeep,
	}
}

func createOCIRepo(name, namespace string, managedByEso bool) *sourcev1.OCIRepository {
	cm := &sourcev1.OCIRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if managedByEso {
		cm.Labels = map[string]string{
			LabelManagedBy: LabelManagedByValue,
		}
	}
	return cm
}

var _ ManagedCluster = &fakeCluster{}

type fakeCluster struct {
	managedCluster
	fakeClient client.Client
}

// GetClient implements [ManagedCluster].
func (f *fakeCluster) GetClient() client.Client {
	return f.fakeClient
}

func createFakeCluster(client client.Client) ManagedCluster {
	return &fakeCluster{
		fakeClient: client,
	}
}

func createFakeClient(clusterObjects []client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = sourcev1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithObjects(clusterObjects...).WithScheme(scheme).Build()
}

type listErrorClient struct {
	client.Client
}

func (l listErrorClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return errors.New("list failed")
}

type deleteErrorClient struct {
	client.Client
	fakeOCIRepo sourcev1.OCIRepository
}

func (d deleteErrorClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	repoList := list.(*sourcev1.OCIRepositoryList)
	repoList.Items = []sourcev1.OCIRepository{d.fakeOCIRepo}
	return nil
}

func (d deleteErrorClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return errors.New("delete failed")
}
