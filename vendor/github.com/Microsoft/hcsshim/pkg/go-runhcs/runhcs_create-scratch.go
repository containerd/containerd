package runhcs

import (
	"context"
)

// CreateScratch creates a scratch vhdx at 'destpath' that is ext4 formatted.
func (r *Runhcs) CreateScratch(context context.Context, destpath string) error {
	return r.runOrError(r.command(context, "create-scratch", "--destpath", destpath))
}
