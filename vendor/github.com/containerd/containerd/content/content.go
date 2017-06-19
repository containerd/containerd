package content

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

var (
	// ErrNotFound is returned when an item is not found.
	//
	// Use IsNotFound(err) to detect this condition.
	ErrNotFound = errors.New("content: not found")

	// ErrExists is returned when something exists when it may not be expected.
	//
	// Use IsExists(err) to detect this condition.
	ErrExists = errors.New("content: exists")

	// ErrLocked is returned when content is actively being uploaded, this
	// indicates that another process is attempting to upload the same content.
	//
	// Use IsLocked(err) to detect this condition.
	ErrLocked = errors.New("content: locked")

	bufPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 1<<20)
		},
	}
)

type Provider interface {
	Reader(ctx context.Context, dgst digest.Digest) (io.ReadCloser, error)
	ReaderAt(ctx context.Context, dgst digest.Digest) (io.ReaderAt, error)
}

type Ingester interface {
	Writer(ctx context.Context, ref string, size int64, expected digest.Digest) (Writer, error)
}

// TODO(stevvooe): Consider a very different name for this struct. Info is way
// to general. It also reads very weird in certain context, like pluralization.
type Info struct {
	Digest      digest.Digest
	Size        int64
	CommittedAt time.Time
}

type Status struct {
	Ref       string
	Offset    int64
	Total     int64
	Expected  digest.Digest
	StartedAt time.Time
	UpdatedAt time.Time
}

// WalkFunc defines the callback for a blob walk.
type WalkFunc func(Info) error

// Manager provides methods for inspecting, listing and removing content.
type Manager interface {
	// Info will return metadata about content available in the content store.
	//
	// If the content is not present, ErrNotFound will be returned.
	Info(ctx context.Context, dgst digest.Digest) (Info, error)

	// Walk will call fn for each item in the content store.
	Walk(ctx context.Context, fn WalkFunc) error

	// Delete removes the content from the store.
	Delete(ctx context.Context, dgst digest.Digest) error

	// Status returns the status of any active ingestions whose ref match the
	// provided regular expression. If empty, all active ingestions will be
	// returned.
	//
	// TODO(stevvooe): Status may be slighly out of place here. If this remains
	// here, we should remove Manager and just define these on store.
	Status(ctx context.Context, re string) ([]Status, error)

	// Abort completely cancels the ingest operation targeted by ref.
	//
	// TODO(stevvooe): Same consideration as above. This should really be
	// restricted to an ingest management interface.
	Abort(ctx context.Context, ref string) error
}

type Writer interface {
	io.WriteCloser
	Status() (Status, error)
	Digest() digest.Digest
	Commit(size int64, expected digest.Digest) error
	Truncate(size int64) error
}

// Store combines the methods of content-oriented interfaces into a set that
// are commonly provided by complete implementations.
type Store interface {
	Manager
	Ingester
	Provider
}

func IsNotFound(err error) bool {
	return errors.Cause(err) == ErrNotFound
}

func IsExists(err error) bool {
	return errors.Cause(err) == ErrExists
}

func IsLocked(err error) bool {
	return errors.Cause(err) == ErrLocked
}
