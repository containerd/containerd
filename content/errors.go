package content

import "github.com/pkg/errors"

type contentExistsErr struct {
	desc string
}

type contentNotFoundErr struct {
	desc string
}

type contentLockedErr struct {
	desc string
}

// ErrExists is returned when something exists when it may not be expected.
func ErrExists(msg string) error {
	if msg == "" {
		msg = "content: exists"
	}
	return errors.WithStack(contentExistsErr{
		desc: msg,
	})
}

// ErrNotFound is returned when an item is not found.
func ErrNotFound(msg string) error {
	if msg == "" {
		msg = "content: not found"
	}
	return errors.WithStack(contentNotFoundErr{
		desc: msg,
	})
}

// ErrLocked is returned when content is actively being uploaded, this
// indicates that another process is attempting to upload the same content.
func ErrLocked(msg string) error {
	if msg == "" {
		msg = "content: locked"
	}
	return errors.WithStack(contentLockedErr{
		desc: msg,
	})
}

func (c contentExistsErr) Error() string {
	return c.desc
}
func (c contentNotFoundErr) Error() string {
	return c.desc
}
func (c contentLockedErr) Error() string {
	return c.desc
}

func (c contentExistsErr) Exists() bool {
	return true
}

func (c contentNotFoundErr) NotFound() bool {
	return true
}

func (c contentLockedErr) Locked() bool {
	return true
}

// IsNotFound returns true if the error is due to a not found content item
func IsNotFound(err error) bool {
	if err, ok := errors.Cause(err).(interface {
		NotFound() bool
	}); ok {
		return err.NotFound()
	}
	return false
}

// IsExists returns true if the error is due to an already existing content item
func IsExists(err error) bool {
	if err, ok := errors.Cause(err).(interface {
		Exists() bool
	}); ok {
		return err.Exists()
	}
	return false
}

// IsLocked returns true if the error is due to a currently locked content item
func IsLocked(err error) bool {
	if err, ok := errors.Cause(err).(interface {
		Locked() bool
	}); ok {
		return err.Locked()
	}
	return false
}
