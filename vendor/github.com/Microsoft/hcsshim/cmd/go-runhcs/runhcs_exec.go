package runhcs

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/containerd/go-runc"
)

// ExecOpts is set of options that can be used with the Exec command.
type ExecOpts struct {
	runc.IO
	// Detach from the container's process.
	Detach bool
	// PidFile is the path to the file to write the process id to.
	PidFile string
	// ShimLog is the path to the log file for the launched shim process.
	ShimLog string
}

func (opt *ExecOpts) args() ([]string, error) {
	var out []string
	if opt.Detach {
		out = append(out, "--detach")
	}
	if opt.PidFile != "" {
		abs, err := filepath.Abs(opt.PidFile)
		if err != nil {
			return nil, err
		}
		out = append(out, "--pid-file", abs)
	}
	if opt.ShimLog != "" {
		abs, err := filepath.Abs(opt.ShimLog)
		if err != nil {
			return nil, err
		}
		out = append(out, "--shim-log", abs)
	}
	return out, nil
}

// Exec executes an additional process inside the container based on the
// oci.Process spec found at processFile.
func (r *Runhcs) Exec(context context.Context, id, processFile string, opts *ExecOpts) error {
	args := []string{"exec", "--process", processFile}
	if opts != nil {
		oargs, err := opts.args()
		if err != nil {
			return err
		}
		args = append(args, oargs...)
	}
	cmd := r.command(context, append(args, id)...)
	if opts != nil && opts.IO != nil {
		opts.Set(cmd)
	}
	if cmd.Stdout == nil && cmd.Stderr == nil {
		data, err := cmdOutput(cmd, true)
		if err != nil {
			return fmt.Errorf("%s: %s", err, data)
		}
		return nil
	}
	ec, err := runc.Monitor.Start(cmd)
	if err != nil {
		return err
	}
	if opts != nil && opts.IO != nil {
		if c, ok := opts.IO.(runc.StartCloser); ok {
			if err := c.CloseAfterStart(); err != nil {
				return err
			}
		}
	}
	status, err := runc.Monitor.Wait(cmd, ec)
	if err == nil && status != 0 {
		err = fmt.Errorf("%s did not terminate sucessfully", cmd.Args[0])
	}
	return err
}
