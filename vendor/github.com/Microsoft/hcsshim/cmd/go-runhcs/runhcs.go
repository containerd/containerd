package runhcs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/containerd/go-runc"
)

// Format is the type of log formatting options available.
type Format string

const (
	none Format = ""
	// Text is the default text log ouput.
	Text Format = "text"
	// JSON is the JSON formatted log output.
	JSON Format = "json"

	command = "runhcs"
)

var bytesBufferPool = sync.Pool{
	New: func() interface{} {
		return bytes.NewBuffer(nil)
	},
}

func getBuf() *bytes.Buffer {
	return bytesBufferPool.Get().(*bytes.Buffer)
}

func putBuf(b *bytes.Buffer) {
	b.Reset()
	bytesBufferPool.Put(b)
}

// Runhcs is the client to the runhcs cli
type Runhcs struct {
	// Debug enables debug output for logging.
	Debug bool
	// Log sets the log file path where internal debug information is written.
	Log string
	// LogFormat sets the format used by logs.
	LogFormat Format
	// Owner sets the compute system owner property.
	Owner string
	// Root is the registry key root for storage of runhcs container state.
	Root string
}

func (r *Runhcs) args() []string {
	var out []string
	if r.Debug {
		out = append(out, "--debug")
	}
	if r.Log != "" {
		// TODO: JTERRY75 - Should we do abs here?
		out = append(out, "--log", r.Log)
	}
	if r.LogFormat != none {
		out = append(out, "--log-format", string(r.LogFormat))
	}
	if r.Owner != "" {
		out = append(out, "--owner", r.Owner)
	}
	if r.Root != "" {
		out = append(out, "--root", r.Root)
	}
	return out
}

func (r *Runhcs) command(context context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(context, command, append(r.args(), args...)...)
	cmd.Env = os.Environ()
	return cmd
}

// runOrError will run the provided command.  If an error is
// encountered and neither Stdout or Stderr was set the error and the
// stderr of the command will be returned in the format of <error>:
// <stderr>
func (r *Runhcs) runOrError(cmd *exec.Cmd) error {
	if cmd.Stdout != nil || cmd.Stderr != nil {
		ec, err := runc.Monitor.Start(cmd)
		if err != nil {
			return err
		}
		status, err := runc.Monitor.Wait(cmd, ec)
		if err == nil && status != 0 {
			err = fmt.Errorf("%s did not terminate sucessfully", cmd.Args[0])
		}
		return err
	}
	data, err := cmdOutput(cmd, true)
	if err != nil {
		return fmt.Errorf("%s: %s", err, data)
	}
	return nil
}

func cmdOutput(cmd *exec.Cmd, combined bool) ([]byte, error) {
	b := getBuf()
	defer putBuf(b)

	cmd.Stdout = b
	if combined {
		cmd.Stderr = b
	}
	ec, err := runc.Monitor.Start(cmd)
	if err != nil {
		return nil, err
	}

	status, err := runc.Monitor.Wait(cmd, ec)
	if err == nil && status != 0 {
		err = fmt.Errorf("%s did not terminate sucessfully", cmd.Args[0])
	}

	return b.Bytes(), err
}
