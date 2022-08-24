//go:build windows

package runhcs

import (
	"context"
)

// Pause suspends all processes inside the container.
func (r *Runhcs) Pause(context context.Context, id string) error {
	return r.runOrError(r.command(context, "pause", id))
}
