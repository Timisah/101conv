package repositorymanager

import "errors"

var (
	ErrRepositoryDisabled = errors.New("repository is disabled")
	ErrPlanExpired        = errors.New("installation plan is missing or expired")
	ErrStalePlan          = errors.New("installation preview is stale; create a new preview")
	ErrSignature          = errors.New("repository signature verification failed")
	ErrExpiredMetadata    = errors.New("repository metadata has expired")
	ErrRollback           = errors.New("repository metadata rollback was rejected")
	ErrMissingDependency  = errors.New("required engine dependency is unavailable")
	ErrIncompatible       = errors.New("repository package is incompatible")
	ErrInterruptedInstall = errors.New("installation did not commit")
	ErrUnsafePackage      = errors.New("repository package failed safe extraction checks")
)

type CodedError struct {
	Code string
	Err  error
}

func (e *CodedError) Error() string { return e.Err.Error() }
func (e *CodedError) Unwrap() error { return e.Err }

func coded(code string, err error) error {
	if err == nil {
		return nil
	}
	return &CodedError{Code: code, Err: err}
}
