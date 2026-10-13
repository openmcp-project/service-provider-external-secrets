/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/openmcp-project/controller-utils/pkg/clusters"
	"github.com/openmcp-project/opencontrolplane-runtime/pkg/serviceprovider/clusteraccess"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	api "github.com/openmcp-project/service-provider-external-secrets/api/v1alpha1"
	"github.com/openmcp-project/service-provider-external-secrets/pkg/externalsecrets"
)

func TestDeletionReportsOrphanCleanupResult(t *testing.T) {
	for _, placement := range []Placement{PlacementMCP, PlacementPlatform} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fail=%t", placement, fail), func(t *testing.T) {
				scheme := runtime.NewScheme()
				for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, helmv2.AddToScheme, sourcev1.AddToScheme, api.AddToScheme} {
					if err := add(scheme); err != nil {
						t.Fatal(err)
					}
				}
				calls := 0
				listErr := errors.New("injected orphan list failure")
				cli := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
					List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if _, ok := list.(*corev1.SecretList); ok {
							calls++
							if fail {
								return listErr
							}
						}
						return c.List(ctx, list, opts...)
					},
				}).Build()
				cluster := clusters.NewTestClusterFromClient("test", cli).WithRESTConfig(&rest.Config{Host: "https://test.invalid"})
				reconciler := ExternalSecretsOperatorReconciler{PlatformCluster: cluster, PodNamespace: "provider", Placement: placement}
				now := metav1.Now()
				obj := &api.ExternalSecretsOperator{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "tenant", DeletionTimestamp: &now}}
				obj.Spec.Version = "test"
				chart := "oci://registry.example/chart"
				pc := &api.ProviderConfig{Spec: api.ProviderConfigSpec{PollInterval: &metav1.Duration{Duration: time.Minute}, Versions: []api.ExternalSecretsVersion{{Version: "test", ChartVersion: "1.0.0", ChartURL: &chart}}}}
				result, err := reconciler.Delete(context.Background(), obj, pc, clusteraccess.ClusterContext{MCPCluster: cluster})
				if calls == 0 {
					t.Fatal("orphan cleaner was not reached")
				}
				if fail && !errors.Is(err, externalsecrets.ErrOrphanCleanup) {
					t.Fatalf("expected orphan cleanup failure, got result=%+v, error=%v", result, err)
				}
				if !fail && (err != nil || result.RequeueAfter != 0) {
					t.Fatalf("completed cleanup did not finish: result=%+v, error=%v", result, err)
				}
			})
		}
	}
}
