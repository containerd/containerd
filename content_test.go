package containerd

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/testsuite"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/namespaces"
	"github.com/pkg/errors"
)

func newContentStore(ctx context.Context, root string) (context.Context, content.Store, func() error, error) {
	client, err := New(address)
	if err != nil {
		return nil, nil, nil, err
	}

	var (
		count uint64
		cs    = client.ContentStore()
		name  = testsuite.Name(ctx)
	)

	wrap := func(ctx context.Context) (context.Context, func() error, error) {
		n := atomic.AddUint64(&count, 1)
		ctx = namespaces.WithNamespace(ctx, fmt.Sprintf("%s-n%d", name, n))
		return client.WithLease(ctx)
	}

	ctx = testsuite.SetContextWrapper(ctx, wrap)

	return ctx, cs, func() error {
		for i := uint64(1); i <= count; i++ {
			ctx = namespaces.WithNamespace(ctx, fmt.Sprintf("%s-n%d", name, i))
			statuses, err := cs.ListStatuses(ctx)
			if err != nil {
				return err
			}
			for _, st := range statuses {
				if err := cs.Abort(ctx, st.Ref); err != nil {
					return errors.Wrapf(err, "failed to abort %s", st.Ref)
				}
			}
			err = cs.Walk(ctx, func(info content.Info) error {
				if err := cs.Delete(ctx, info.Digest); err != nil {
					if errdefs.IsNotFound(err) {
						return nil
					}

					return err
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil

	}, nil
}

func TestContentClient(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	testsuite.ContentSuite(t, "ContentClient", newContentStore)
}
