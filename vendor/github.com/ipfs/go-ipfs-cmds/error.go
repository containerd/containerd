package cmds

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrorType signfies a category of errors
type ErrorType uint

// ErrorTypes convey what category of error ocurred
const (
	// ErrNormal is a normal error. The command failed for some reason that's not a bug.
	ErrNormal ErrorType = iota
	// ErrClient means the client made an invalid request.
	ErrClient
	// ErrImplementation means there's a bug in the implementation.
	ErrImplementation
	// ErrRateLimited is returned when the operation has been rate-limited.
	ErrRateLimited
	// ErrForbidden is returned when the client doesn't have permission to
	// perform the requested operation.
	ErrForbidden
)

func (e ErrorType) Error() string {
	return e.String()
}

func (e ErrorType) String() string {
	switch e {
	case ErrNormal:
		return "command failed"
	case ErrClient:
		return "invalid argument"
	case ErrImplementation:
		return "internal error"
	case ErrRateLimited:
		return "rate limited"
	case ErrForbidden:
		return "request forbidden"
	default:
		return "unknown error code"
	}
}

// Error is a struct for marshalling errors
type Error struct {
	Message string
	Code    ErrorType
}

// Errorf returns an Error with the given code and format specification
func Errorf(code ErrorType, format string, args ...interface{}) Error {
	return Error{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	}
}

func (e Error) Error() string {
	return e.Message
}

// Unwrap returns the base error (an ErrorType). Works with go 1.13 error
// helpers.
func (e Error) Unwrap() error {
	return e.Code
}

func (e Error) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Message string
		Code    ErrorType
		Type    string
	}{
		Message: e.Message,
		Code:    e.Code,
		Type:    "error",
	})
}

func (e *Error) UnmarshalJSON(data []byte) error {
	var w struct {
		Message string
		Code    ErrorType
		Type    string
	}

	err := json.Unmarshal(data, &w)
	if err != nil {
		return err
	}

	if w.Type != "error" {
		return errors.New("not of type error")
	}

	e.Message = w.Message
	e.Code = w.Code

	return nil
}
