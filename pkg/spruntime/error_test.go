package spruntime_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/openmcp-project/service-provider-external-secrets/pkg/spruntime"
)

func TestIgnoreFunctionalError(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		err     error
		wantErr bool
	}{
		{
			name:    "functional error is ignored",
			err:     spruntime.NewFunctionalError(errors.New("functional error")),
			wantErr: false,
		},
		{
			name:    "non-functional error is returned",
			err:     errors.New("non-functional error"),
			wantErr: true,
		},
		{
			name:    "nil is nil",
			err:     nil,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotErr := spruntime.IgnoreFunctionalError(tt.err)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("IgnoreFunctionalError() failed: %v", gotErr)
				}
				assert.Equal(t, tt.err, gotErr)
				return
			}
			assert.False(t, tt.wantErr)
		})
	}
}
