package fuse

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

func callMountFuseFs(mountPoint string, opts *MountOptions) (devFuseFd int, err error) {
	bin, err := fusermountBinary()
	if err != nil {
		return -1, err
	}

	// Convenience for cleaning up open FDs in the event of an error.
	closeFd := func(fd int) {
		if err != nil {
			syscall.Close(fd)
		}
	}

	// Use syscall.Open instead of os.OpenFile to avoid Go garbage collecting an
	// [os.File], which will close the FD later, but we need to keep it open for
	// the life of the FUSE mount.
	const devFuse = "/dev/fuse"
	devFuseFd, err = syscall.Open(devFuse, syscall.O_RDWR, 0)
	if err != nil {
		return -1, fmt.Errorf(
			"failed to get a RDWR file descriptor for %s: %w",
			devFuse, err)
	}
	defer closeFd(devFuseFd)
	// We will also defensively direct the forked process's stdin to the null
	// device to prevent any unintended side effects.
	devNullFd, err := syscall.Open(os.DevNull, syscall.O_RDONLY, 0)
	if err != nil {
		return -1, fmt.Errorf(
			"failed to get a RDONLY file descriptor for %s: %w",
			os.DevNull, err)
	}
	defer closeFd(devNullFd)

	// Use syscall.ForkExec directly instead of exec.Command because we need to
	// pass raw file descriptors to the child, rather than a garbage collected
	// os.File.
	env := []string{"MOUNT_FUSEFS_CALL_BY_LIB=1"}
	argv := []string{
		bin,
		"--safe",
		"-o", strings.Join(opts.optionsStrings(), ","),
		"3",
		mountPoint,
	}
	fds := []uintptr{
		uintptr(devNullFd),
		uintptr(os.Stdout.Fd()),
		uintptr(os.Stderr.Fd()),
		uintptr(devFuseFd),
	}
	pid, err := syscall.ForkExec(bin, argv, &syscall.ProcAttr{
		Env:   env,
		Files: fds,
	})
	if err != nil {
		return -1, fmt.Errorf("failed to fork mount_fusefs: %w", err)
	}

	var ws syscall.WaitStatus
	_, err = syscall.Wait4(pid, &ws, 0, nil)
	if err != nil {
		return -1, fmt.Errorf(
			"failed to wait for exit status of mount_fusefs: %w", err)
	}
	if ws.ExitStatus() != 0 {
		return -1, fmt.Errorf(
			"mount_fusefs: exited with status %d", ws.ExitStatus())
	}

	return devFuseFd, nil
}

func mount(mountPoint string, opts *MountOptions, ready chan<- error) (fd int, err error) {
	// Note: opts.DirectMount is not supported in FreeBSD, but the intended
	// behavior is to *attempt* a direct mount when it's set, not to return an
	// error. So in this case, we just ignore it and use the binary from
	// fusermountBinary().

	// Using the same logic from libfuse to prevent chaos
	for {
		f, err := os.OpenFile("/dev/null", os.O_RDWR, 0o000)
		if err != nil {
			return -1, err
		}
		if f.Fd() > 2 {
			f.Close()
			break
		}
	}

	// Magic `/dev/fd/N` mountpoint. See the docs for NewServer() for how this
	// works.

	fd = parseFuseFd(mountPoint)
	if fd >= 0 {
		if opts.Debug {
			opts.Logger.Printf("mount: magic mountpoint %q, using fd %d", mountPoint, fd)
		}
	} else {
		// Usual case: mount via the `fusermount` suid helper
		fd, err = callMountFuseFs(mountPoint, opts)
		if err != nil {
			return
		}
	}
	// golang sets CLOEXEC on file descriptors when they are
	// acquired through normal operations (e.g. open).
	// However, for raw FDs, we have to set CLOEXEC manually.
	syscall.CloseOnExec(fd)

	close(ready)
	return fd, err
}

func unmount(mountPoint string, opts *MountOptions) (err error) {
	_ = opts
	return syscall.Unmount(mountPoint, 0)
}

func fusermountBinary() (string, error) {
	binPaths := []string{
		"/sbin/mount_fusefs",
	}

	for _, path := range binPaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("no FUSE mount utility found")
}
