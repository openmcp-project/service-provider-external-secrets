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
	"testing"

	"github.com/stretchr/testify/assert"

	apiv1alpha1 "github.com/openmcp-project/service-provider-external-secrets/api/v1alpha1"
)

func Test_selectExternalSecretsVersion(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		requestedVersion string
		pc               *apiv1alpha1.ProviderConfig
		want             apiv1alpha1.ExternalSecretsVersion
		wantErr          bool
	}{
		{
			name:             "version is available",
			requestedVersion: "v1",
			pc: &apiv1alpha1.ProviderConfig{
				Spec: apiv1alpha1.ProviderConfigSpec{
					Versions: []apiv1alpha1.ExternalSecretsVersion{{Version: "v1"}, {Version: "v2"}},
				},
			},
			want: apiv1alpha1.ExternalSecretsVersion{
				Version: "v1",
			},
			wantErr: false,
		},
		{
			name:             "version is not available",
			requestedVersion: "v3",
			pc: &apiv1alpha1.ProviderConfig{
				Spec: apiv1alpha1.ProviderConfigSpec{
					Versions: []apiv1alpha1.ExternalSecretsVersion{{Version: "v1"}, {Version: "v2"}},
				},
			},
			want:    apiv1alpha1.ExternalSecretsVersion{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := selectExternalSecretsVersion(tt.requestedVersion, tt.pc)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("selectExternalSecretsVersion() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("selectExternalSecretsVersion() succeeded unexpectedly")
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
