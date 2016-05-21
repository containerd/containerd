package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/docker/containerd/specs"
	"github.com/docker/containerd/subreaper"
	"github.com/docker/containerd/subreaper/exec"
	"github.com/docker/docker/pkg/term"
	"github.com/opencontainers/runc/libcontainer"
)

type directProcess struct {
	*process
	sync.WaitGroup

	io          stdio
	console     libcontainer.Console
	consolePath string
	exec        bool
	checkpoint  string
	specs       *specs.Spec
}

func newDirectProcess(config *processConfig) (*directProcess, error) {
	lp, err := newProcess(config)
	if err != nil {
		return nil, err
	}

	return &directProcess{
		specs:      config.spec,
		process:    lp,
		exec:       config.exec,
		checkpoint: config.checkpoint,
	}, nil
}

func (d *directProcess) CloseStdin() error {
	if d.io.stdin != nil {
		return d.io.stdin.Close()
	}
	return nil
}

func (d *directProcess) Resize(w, h int) error {
	if d.console == nil {
		return nil
	}
	ws := term.Winsize{
		Width:  uint16(w),
		Height: uint16(h),
	}
	return term.SetWinsize(d.console.Fd(), &ws)
}

func (d *directProcess) openIO() (*os.File, *os.File, *os.File, error) {
	uid, gid, err := getRootIDs(d.specs)
	if err != nil {
		return nil, nil, nil, err
	}

	if d.spec.Terminal {
		console, err := libcontainer.NewConsole(uid, gid)
		if err != nil {
			return nil, nil, nil, err
		}
		d.console = console
		d.consolePath = console.Path()
		stdin, err := os.OpenFile(d.stdio.Stdin, syscall.O_RDONLY, 0)
		if err != nil {
			return nil, nil, nil, err
		}
		go io.Copy(console, stdin)
		stdout, err := os.OpenFile(d.stdio.Stdout, syscall.O_RDWR, 0)
		if err != nil {
			return nil, nil, nil, err
		}
		d.Add(1)
		go func() {
			io.Copy(stdout, console)
			console.Close()
			d.Done()
		}()
		d.io.stdin = stdin
		d.io.stdout = stdout
		d.io.stderr = stdout
		return nil, nil, nil, nil
	}

	stdin, err := os.OpenFile(d.stdio.Stdin, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, nil, err
	}

	stdout, err := os.OpenFile(d.stdio.Stdout, syscall.O_RDWR, 0)
	if err != nil {
		return nil, nil, nil, err
	}

	stderr, err := os.OpenFile(d.stdio.Stderr, syscall.O_RDWR, 0)
	if err != nil {
		return nil, nil, nil, err
	}

	d.io.stdin = stdin
	d.io.stdout = stdout
	d.io.stderr = stderr
	return stdin, stdout, stderr, nil
}

func (d *directProcess) loadCheckpoint(bundle string) (*Checkpoint, error) {
	if d.checkpoint == "" {
		return nil, nil
	}

	f, err := os.Open(filepath.Join(bundle, "checkpoints", d.checkpoint, "config.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cpt Checkpoint
	if err := json.NewDecoder(f).Decode(&cpt); err != nil {
		return nil, err
	}
	return &cpt, nil
}

func (d *directProcess) Start() error {
	cwd, err := filepath.Abs(d.root)
	if err != nil {
		return err
	}

	stdin, stdout, stderr, err := d.openIO()
	if err != nil {
		return nil
	}

	checkpoint, err := d.loadCheckpoint(d.container.bundle)
	if err != nil {
		return err
	}

	logPath := filepath.Join(cwd, "log.json")
	args := append([]string{
		"--log", logPath,
		"--log-format", "json",
	}, d.container.runtimeArgs...)
	if d.exec {
		args = append(args, "exec",
			"--process", filepath.Join(cwd, "process.json"),
			"--console", d.consolePath,
		)
	} else if checkpoint != nil {
		args = append(args, "restore",
			"--image-path", filepath.Join(d.container.bundle, "checkpoints", checkpoint.Name),
		)
		add := func(flags ...string) {
			args = append(args, flags...)
		}
		if checkpoint.Shell {
			add("--shell-job")
		}
		if checkpoint.Tcp {
			add("--tcp-established")
		}
		if checkpoint.UnixSockets {
			add("--ext-unix-sk")
		}
		if d.container.noPivotRoot {
			add("--no-pivot")
		}
	} else {
		args = append(args, "start",
			"--bundle", d.container.bundle,
			"--console", d.consolePath,
		)
		if d.container.noPivotRoot {
			args = append(args, "--no-pivot")
		}
	}
	args = append(args,
		"-d",
		"--pid-file", filepath.Join(cwd, "pid"),
		d.container.id,
	)
	cmd := exec.Command(d.container.runtime, args...)
	cmd.Dir = d.container.bundle
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// set the parent death signal to SIGKILL so that if containerd dies the container
	// process also dies
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}

	exitSubscription := subreaper.Subscribe()
	err = d.startCmd(cmd)
	if err != nil {
		subreaper.Unsubscribe(exitSubscription)
		d.delete()
		return err
	}

	go d.watch(cmd, exitSubscription)

	return nil
}

func (d *directProcess) watch(cmd *exec.Cmd, exitSubscription *subreaper.Subscription) {
	defer subreaper.Unsubscribe(exitSubscription)
	defer d.delete()

	f, err := os.OpenFile(path.Join(d.root, ExitFile), syscall.O_WRONLY, 0)
	if err == nil {
		defer f.Close()
	}

	exitCode := 0
	if err = cmd.Wait(); err != nil {
		if exitError, ok := err.(exec.ExitCodeError); ok {
			exitCode = exitError.Code
		}
	}

	if exitCode == 0 {
		pid, err := d.getPidFromFile()
		if err != nil {
			return
		}
		exitSubscription.SetPid(pid)
		exitCode = exitSubscription.Wait()
	}

	writeInt(path.Join(d.root, ExitStatusFile), exitCode)
}

func (d *directProcess) delete() {
	if d.console != nil {
		d.console.Close()
	}
	d.io.Close()
	d.Wait()
	if !d.exec {
		exec.Command(d.container.runtime, append(d.container.runtimeArgs, "delete", d.container.id)...).Run()
	}
}

func writeInt(path string, i int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%d", i)
	return err
}

type stdio struct {
	stdin  *os.File
	stdout *os.File
	stderr *os.File
}

func (s stdio) Close() error {
	err := s.stdin.Close()
	if oerr := s.stdout.Close(); err == nil {
		err = oerr
	}
	if oerr := s.stderr.Close(); err == nil {
		err = oerr
	}
	return err
}
