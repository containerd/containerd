//go:build windows

package runhcs

import (
	"context"
)

// Start will start an already created container.
func (r *Runhcs) Start(context context.Context, id string) error {
	return r.runOrError(r.command(context, "start", id))
}
