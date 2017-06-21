package content

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
	return contentExistsErr{
		desc: msg,
	}
}

// ErrNotFound is returned when an item is not found.
func ErrNotFound(msg string) error {
	if msg == "" {
		msg = "content: not found"
	}
	return contentNotFoundErr{
		desc: msg,
	}
}

// ErrLocked is returned when content is actively being uploaded, this
// indicates that another process is attempting to upload the same content.
func ErrLocked(msg string) error {
	if msg == "" {
		msg = "content: locked"
	}
	return contentLockedErr{
		desc: msg,
	}
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
	if err, ok := err.(interface {
		NotFound() bool
	}); ok {
		return err.NotFound()
	}

	causal, ok := err.(interface {
		Cause() error
	})
	if !ok {
		return false
	}

	return IsNotFound(causal.Cause())
}

// IsExists returns true if the error is due to an already existing content item
func IsExists(err error) bool {
	if err, ok := err.(interface {
		Exists() bool
	}); ok {
		return err.Exists()
	}

	causal, ok := err.(interface {
		Cause() error
	})
	if !ok {
		return false
	}

	return IsExists(causal.Cause())
}

// IsLocked returns true if the error is due to a currently locked content item
func IsLocked(err error) bool {
	if err, ok := err.(interface {
		Locked() bool
	}); ok {
		return err.Locked()
	}

	causal, ok := err.(interface {
		Cause() error
	})
	if !ok {
		return false
	}

	return IsLocked(causal.Cause())
}
