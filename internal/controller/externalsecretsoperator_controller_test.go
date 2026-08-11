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
	"errors"
	"fmt"
	"testing"

	ctrlerrors "github.com/openmcp-project/controller-utils/pkg/errors"
	manager "github.com/openmcp-project/controller-utils/pkg/manager"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1alpha1 "github.com/openmcp-project/service-provider-external-secrets/api/v1alpha1"
)

func Test_selectExternalSecretsVersion(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		requestedVersion string
		pc               *apiv1alpha1.ProviderConfig
		want             apiv1alpha1.RequestedVersion
		wantErr          bool
	}{
		{
			name:             "version is available",
			requestedVersion: "v1",
			pc: &apiv1alpha1.ProviderConfig{
				Spec: apiv1alpha1.ProviderConfigSpec{
					Versions: []apiv1alpha1.RequestedVersion{{Version: "v1"}, {Version: "v2"}},
				},
			},
			want: apiv1alpha1.RequestedVersion{
				Version: "v1",
			},
			wantErr: false,
		},
		{
			name:             "version is not available",
			requestedVersion: "v3",
			pc: &apiv1alpha1.ProviderConfig{
				Spec: apiv1alpha1.ProviderConfigSpec{
					Versions: []apiv1alpha1.RequestedVersion{{Version: "v1"}, {Version: "v2"}},
				},
			},
			want:    apiv1alpha1.RequestedVersion{},
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
				assert.Nil(t, ctrlerrors.IgnoreInvalidUserInput(gotErr))
				return
			}
			if tt.wantErr {
				t.Fatal("selectExternalSecretsVersion() succeeded unexpectedly")
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_updateStatusError(t *testing.T) {
	tests := []struct {
		name            string
		obj             *apiv1alpha1.ExternalSecretsOperator
		err             error
		wantMessage     string
		wantIgnoreError bool
	}{
		{
			name:        "resource error",
			obj:         &apiv1alpha1.ExternalSecretsOperator{},
			err:         manager.ErrManagedResourcesFailed,
			wantMessage: manager.ErrManagedResourcesFailed.Error(),
		},
		{
			name:        "cleanup error",
			obj:         &apiv1alpha1.ExternalSecretsOperator{},
			err:         manager.ErrOrphanCleanup,
			wantMessage: manager.ErrOrphanCleanup.Error(),
		},
		{
			name:        "combined resource and cleanup error",
			obj:         &apiv1alpha1.ExternalSecretsOperator{},
			err:         fmt.Errorf("%w: %w", manager.ErrManagedResourcesFailed, manager.ErrOrphanCleanup),
			wantMessage: fmt.Sprintf("%s; %s", manager.ErrManagedResourcesFailed.Error(), manager.ErrOrphanCleanup.Error()),
		},
		{
			name:        "resource error wrapping non-user-facing error",
			obj:         &apiv1alpha1.ExternalSecretsOperator{},
			err:         fmt.Errorf("%w: %w", manager.ErrManagedResourcesFailed, errors.New("internal detail")),
			wantMessage: manager.ErrManagedResourcesFailed.Error(),
		},
		{
			name:        "non-framework error surfaces generic fallback message",
			obj:         &apiv1alpha1.ExternalSecretsOperator{},
			err:         errors.New("internal detail"),
			wantMessage: "internal reconcile error — check controller logs",
		},
		{
			name:            "invalid user input is ignored",
			obj:             &apiv1alpha1.ExternalSecretsOperator{},
			err:             fmt.Errorf("%w: %w", manager.ErrManagedResourcesFailed, fmt.Errorf("%w: value out of range", ctrlerrors.ErrInvalidUserInput)),
			wantMessage:     manager.ErrManagedResourcesFailed.Error(),
			wantIgnoreError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotErr := updateStatusError(tt.obj, tt.err)
			cond := tt.obj.Status.Conditions[0]
			assert.Equal(t, tt.wantMessage, cond.Message)
			assert.Equal(t, metav1.ConditionFalse, cond.Status)
			assert.Equal(t, conditionReasonError, cond.Reason)
			assert.Equal(t, tt.wantMessage, cond.Message)
			if tt.wantIgnoreError {
				assert.Nil(t, gotErr)
				return
			}
			assert.Error(t, gotErr)
		})
	}
}

func TestIfnilIsEmpty(t *testing.T) {
	t.Run("test nilIfEmpty function", func(t *testing.T) {
		s := "test"
		assert.Empty(t, nilIfEmpty(""), "Correct string is empty")
		assert.Equal(t, nilIfEmpty(s), &s, "Points to equal value")
	})
}
