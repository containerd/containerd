package runc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// Format is the type of log formatting options avaliable
type Format string

const (
	none Format = ""
	JSON Format = "json"
	Text Format = "text"
	// DefaultCommand is the default command for Runc
	DefaultCommand = "runc"
)

// Runc is the client to the runc cli
type Runc struct {
	//If command is empty, DefaultCommand is used
	Command      string
	Root         string
	Debug        bool
	Log          string
	LogFormat    Format
	PdeathSignal syscall.Signal
}

// List returns all containers created inside the provided runc root directory
func (r *Runc) List(context context.Context) ([]*Container, error) {
	data, err := Monitor.Output(r.command(context, "list", "--format=json"))
	if err != nil {
		return nil, err
	}
	var out []*Container
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// State returns the state for the container provided by id
func (r *Runc) State(context context.Context, id string) (*Container, error) {
	data, err := Monitor.CombinedOutput(r.command(context, "state", id))
	if err != nil {
		return nil, fmt.Errorf("%s: %s", err, data)
	}
	var c Container
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

type ConsoleSocket interface {
	Path() string
}

type CreateOpts struct {
	IO
	// PidFile is a path to where a pid file should be created
	PidFile       string
	ConsoleSocket ConsoleSocket
	Detach        bool
	NoPivot       bool
	NoNewKeyring  bool
	ExtraFiles    []*os.File
}

func (o *CreateOpts) args() (out []string, err error) {
	if o.PidFile != "" {
		abs, err := filepath.Abs(o.PidFile)
		if err != nil {
			return nil, err
		}
		out = append(out, "--pid-file", abs)
	}
	if o.ConsoleSocket != nil {
		out = append(out, "--console-socket", o.ConsoleSocket.Path())
	}
	if o.NoPivot {
		out = append(out, "--no-pivot")
	}
	if o.NoNewKeyring {
		out = append(out, "--no-new-keyring")
	}
	if o.Detach {
		out = append(out, "--detach")
	}
	if o.ExtraFiles != nil {
		out = append(out, "--preserve-fds", strconv.Itoa(len(o.ExtraFiles)))
	}
	return out, nil
}

// Create creates a new container and returns its pid if it was created successfully
func (r *Runc) Create(context context.Context, id, bundle string, opts *CreateOpts) error {
	args := []string{"create", "--bundle", bundle}
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
	cmd.ExtraFiles = opts.ExtraFiles

	if cmd.Stdout == nil && cmd.Stderr == nil {
		data, err := Monitor.CombinedOutput(cmd)
		if err != nil {
			return fmt.Errorf("%s: %s", err, data)
		}
		return nil
	}
	if err := Monitor.Start(cmd); err != nil {
		return err
	}
	if opts != nil && opts.IO != nil {
		if c, ok := opts.IO.(StartCloser); ok {
			if err := c.CloseAfterStart(); err != nil {
				return err
			}
		}
	}
	_, err := Monitor.Wait(cmd)
	return err
}

// Start will start an already created container
func (r *Runc) Start(context context.Context, id string) error {
	return r.runOrError(r.command(context, "start", id))
}

type ExecOpts struct {
	IO
	PidFile       string
	ConsoleSocket ConsoleSocket
	Detach        bool
}

func (o *ExecOpts) args() (out []string, err error) {
	if o.ConsoleSocket != nil {
		out = append(out, "--console-socket", o.ConsoleSocket.Path())
	}
	if o.Detach {
		out = append(out, "--detach")
	}
	if o.PidFile != "" {
		abs, err := filepath.Abs(o.PidFile)
		if err != nil {
			return nil, err
		}
		out = append(out, "--pid-file", abs)
	}
	return out, nil
}

// Exec executres and additional process inside the container based on a full
// OCI Process specification
func (r *Runc) Exec(context context.Context, id string, spec specs.Process, opts *ExecOpts) error {
	f, err := ioutil.TempFile("", "runc-process")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(spec)
	f.Close()
	if err != nil {
		return err
	}
	args := []string{"exec", "--process", f.Name()}
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
		data, err := Monitor.CombinedOutput(cmd)
		if err != nil {
			return fmt.Errorf("%s: %s", err, data)
		}
		return nil
	}
	if err := Monitor.Start(cmd); err != nil {
		return err
	}
	if opts != nil && opts.IO != nil {
		if c, ok := opts.IO.(StartCloser); ok {
			if err := c.CloseAfterStart(); err != nil {
				return err
			}
		}
	}
	_, err = Monitor.Wait(cmd)
	return err
}

// Run runs the create, start, delete lifecycle of the container
// and returns its exit status after it has exited
func (r *Runc) Run(context context.Context, id, bundle string, opts *CreateOpts) (int, error) {
	args := []string{"run", "--bundle", bundle}
	if opts != nil {
		oargs, err := opts.args()
		if err != nil {
			return -1, err
		}
		args = append(args, oargs...)
	}
	cmd := r.command(context, append(args, id)...)
	if opts != nil {
		opts.Set(cmd)
	}
	if err := Monitor.Start(cmd); err != nil {
		return -1, err
	}
	return Monitor.Wait(cmd)
}

// Delete deletes the container
func (r *Runc) Delete(context context.Context, id string) error {
	return r.runOrError(r.command(context, "delete", id))
}

// KillOpts specifies options for killing a container and its processes
type KillOpts struct {
	All bool
}

func (o *KillOpts) args() (out []string) {
	if o.All {
		out = append(out, "--all")
	}
	return out
}

// Kill sends the specified signal to the container
func (r *Runc) Kill(context context.Context, id string, sig int, opts *KillOpts) error {
	args := []string{
		"kill",
	}
	if opts != nil {
		args = append(args, opts.args()...)
	}
	return r.runOrError(r.command(context, append(args, id, strconv.Itoa(sig))...))
}

// Stats return the stats for a container like cpu, memory, and io
func (r *Runc) Stats(context context.Context, id string) (*Stats, error) {
	cmd := r.command(context, "events", "--stats", id)
	rd, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	defer func() {
		rd.Close()
		Monitor.Wait(cmd)
	}()
	if err := Monitor.Start(cmd); err != nil {
		return nil, err
	}
	var e Event
	if err := json.NewDecoder(rd).Decode(&e); err != nil {
		return nil, err
	}
	return e.Stats, nil
}

// Events returns an event stream from runc for a container with stats and OOM notifications
func (r *Runc) Events(context context.Context, id string, interval time.Duration) (chan *Event, error) {
	cmd := r.command(context, "events", fmt.Sprintf("--interval=%ds", int(interval.Seconds())), id)
	rd, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := Monitor.Start(cmd); err != nil {
		rd.Close()
		return nil, err
	}
	var (
		dec = json.NewDecoder(rd)
		c   = make(chan *Event, 128)
	)
	go func() {
		defer func() {
			close(c)
			rd.Close()
			Monitor.Wait(cmd)
		}()
		for {
			var e Event
			if err := dec.Decode(&e); err != nil {
				if err == io.EOF {
					return
				}
				e = Event{
					Type: "error",
					Err:  err,
				}
			}
			c <- &e
		}
	}()
	return c, nil
}

// Pause the container with the provided id
func (r *Runc) Pause(context context.Context, id string) error {
	return r.runOrError(r.command(context, "pause", id))
}

// Resume the container with the provided id
func (r *Runc) Resume(context context.Context, id string) error {
	return r.runOrError(r.command(context, "resume", id))
}

// Ps lists all the processes inside the container returning their pids
func (r *Runc) Ps(context context.Context, id string) ([]int, error) {
	data, err := Monitor.CombinedOutput(r.command(context, "ps", "--format", "json", id))
	if err != nil {
		return nil, fmt.Errorf("%s: %s", err, data)
	}
	var pids []int
	if err := json.Unmarshal(data, &pids); err != nil {
		return nil, err
	}
	return pids, nil
}

func (r *Runc) args() (out []string) {
	if r.Root != "" {
		out = append(out, "--root", r.Root)
	}
	if r.Debug {
		out = append(out, "--debug")
	}
	if r.Log != "" {
		out = append(out, "--log", r.Log)
	}
	if r.LogFormat != none {
		out = append(out, "--log-format", string(r.LogFormat))
	}
	return out
}

func (r *Runc) command(context context.Context, args ...string) *exec.Cmd {
	command := r.Command
	if command == "" {
		command = DefaultCommand
	}
	cmd := exec.CommandContext(context, command, append(r.args(), args...)...)
	if r.PdeathSignal != 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Pdeathsig: r.PdeathSignal,
		}
	}
	return cmd
}

// runOrError will run the provided command.  If an error is
// encountered and neither Stdout or Stderr was set the error and the
// stderr of the command will be returned in the format of <error>:
// <stderr>
func (r *Runc) runOrError(cmd *exec.Cmd) error {
	if cmd.Stdout != nil || cmd.Stderr != nil {
		return Monitor.Run(cmd)
	}
	data, err := Monitor.CombinedOutput(cmd)
	if err != nil {
		return fmt.Errorf("%s: %s", err, data)
	}
	return nil
}
