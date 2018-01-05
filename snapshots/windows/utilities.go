// +build windows

package windows

import (
	"context"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots/storage"
)

func rollbackWithLogging(ctx context.Context, t storage.Transactor) {
	if err := t.Rollback(); err != nil {
		log.G(ctx).WithError(err).Warn("failed to rollback transaction")
	}
}
