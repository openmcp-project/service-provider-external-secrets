package spruntime

import (
	"errors"
)

// FunctionalError identifies functional errors that are used to update status conditions but ignored in the reconcile result.
// (similar to runtime.Error).
type FunctionalError interface {
	error
	// Functional is a no-op function but serves to distinguish types that are functional errors
	Functional()
}

type functionalError struct {
	error
}

var _ FunctionalError = &functionalError{}

// Functional implements [FunctionalError].
func (f *functionalError) Functional() {}

// NewFunctionalError creates a FunctionalError with the given error
func NewFunctionalError(err error) FunctionalError {
	return &functionalError{err}
}

// IgnoreFunctionalError returns nil on functional errors
func IgnoreFunctionalError(err error) error {
	if _, isFunctionalError := errors.AsType[FunctionalError](err); isFunctionalError {
		return nil
	}
	return err
}
