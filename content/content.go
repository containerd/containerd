package content

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/opencontainers/go-digest"
)

var (
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
	Digest    digest.Digest
	Size      int64
	CreatedAt time.Time
	UpdatedAt time.Time
	Labels    map[string]string
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

	// Update updates mutable information related to content.
	// If one or more fieldpaths are provided, only those
	// fields will be updated.
	// Mutable fields:
	//  labels.*
	Update(ctx context.Context, info Info, fieldpaths ...string) (Info, error)

	// Walk will call fn for each item in the content store which
	// match the provided filters. If no filters are given all
	// items will be walked.
	Walk(ctx context.Context, fn WalkFunc, filters ...string) error

	// Delete removes the content from the store.
	Delete(ctx context.Context, dgst digest.Digest) error
}

// IngestManager provides methods for managing ingests.
type IngestManager interface {
	// Status returns the status of the provided ref.
	Status(ctx context.Context, ref string) (Status, error)

	// ListStatuses returns the status of any active ingestions whose ref match the
	// provided regular expression. If empty, all active ingestions will be
	// returned.
	ListStatuses(ctx context.Context, filters ...string) ([]Status, error)

	// Abort completely cancels the ingest operation targeted by ref.
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
	Provider
	IngestManager
	Ingester
}
