package jobs

import "errors"

var (
	ErrNotFound            = errors.New("job or upload not found")
	ErrConflict            = errors.New("job request conflicts with existing state")
	ErrUnsupported         = errors.New("requested conversion is unsupported")
	ErrInvalidInput        = errors.New("invalid input file")
	ErrTooLarge            = errors.New("file size limit exceeded")
	ErrCanceled            = errors.New("job canceled")
	ErrQuotaExceeded       = errors.New("account quota exceeded")
	ErrLeaseLost           = errors.New("worker lease lost")
	ErrInsufficientStorage = errors.New("insufficient application storage")
	ErrFileExpired         = errors.New("file retention period has expired")
	ErrFileMissing         = errors.New("file is missing from storage")
)
