// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"syscall"
)

func unixgramSocketpair() (l, r *os.File, err error) {
	fd, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_SEQPACKET, 0)
	if err != nil {
		return nil, nil, os.NewSyscallError("socketpair",
			err.(syscall.Errno))
	}
	l = os.NewFile(uintptr(fd[0]), "socketpair-half1")
	r = os.NewFile(uintptr(fd[1]), "socketpair-half2")
	return
}

// Create a FUSE FS on the specified mount point without using
// fusermount.
func mountDirect(mountPoint string, opts *MountOptions, ready chan<- error) (fd int, err error) {
	fd, err = syscall.Open("/dev/fuse", os.O_RDWR, 0) // use syscall.Open since we want an int fd
	if err != nil {
		return
	}

	// managed to open dev/fuse, attempt to mount
	source := opts.FsName
	if source == "" {
		source = opts.Name
	}

	var flags uintptr = syscall.MS_NOSUID | syscall.MS_NODEV
	if opts.DirectMountFlags != 0 {
		flags = opts.DirectMountFlags
	}

	var st syscall.Stat_t
	err = syscall.Stat(mountPoint, &st)
	if err != nil {
		return
	}

	// some values we need to pass to mount - we do as fusermount does.
	// override possible since opts.Options comes after.
	//
	// Only the options listed below are understood by the kernel:
	// https://elixir.bootlin.com/linux/v6.14.2/source/fs/fuse/inode.c#L772
	// https://elixir.bootlin.com/linux/v6.14.2/source/fs/fs_context.c#L41
	// https://elixir.bootlin.com/linux/v6.14.2/source/fs/fs_context.c#L50
	// Everything else will cause an EINVAL error from syscall.Mount() and
	// a corresponding error message in the kernel logs.
	var r = []string{
		fmt.Sprintf("fd=%d", fd),
		fmt.Sprintf("rootmode=%o", st.Mode&syscall.S_IFMT),
		fmt.Sprintf("user_id=%d", os.Geteuid()),
		fmt.Sprintf("group_id=%d", os.Getegid()),
		// match what we do with fusermount
		fmt.Sprintf("max_read=%d", opts.MaxWrite),
	}

	// In syscall.Mount(), [no]dev/suid/exec must be
	// passed as bits in the flags parameter, not as strings.
	for _, o := range opts.Options {
		switch o {
		case "nodev":
			flags |= syscall.MS_NODEV
		case "dev":
			flags &^= syscall.MS_NODEV
		case "nosuid":
			flags |= syscall.MS_NOSUID
		case "suid":
			flags &^= syscall.MS_NOSUID
		case "noexec":
			flags |= syscall.MS_NOEXEC
		case "exec":
			flags &^= syscall.MS_NOEXEC
		default:
			r = append(r, o)
		}
	}

	if opts.AllowOther {
		r = append(r, "allow_other")
	}
	if opts.IDMappedMount && !opts.containsOption("default_permissions") {
		r = append(r, "default_permissions")
	}

	if opts.Debug {
		opts.Logger.Printf("mountDirect: calling syscall.Mount(%q, %q, %q, %#x, %q)",
			source, mountPoint, "fuse."+opts.Name, flags, strings.Join(r, ","))
	}
	err = syscall.Mount(source, mountPoint, "fuse."+opts.Name, flags, strings.Join(r, ","))
	if err != nil {
		syscall.Close(fd)
		return
	}

	// success
	close(ready)
	return
}

// callFusermount calls the `fusermount` suid helper with the right options so
// that it:
// * opens `/dev/fuse`
// * mount()s this file descriptor to `mountPoint`
// * passes this file descriptor back to us via a unix domain socket
// This file descriptor is returned as `fd`.
func callFusermount(mountPoint string, opts *MountOptions) (fd int, err error) {
	local, remote, err := unixgramSocketpair()
	if err != nil {
		return
	}

	defer local.Close()
	defer remote.Close()

	bin, err := fusermountBinary()
	if err != nil {
		return 0, err
	}

	cmd := []string{bin, mountPoint}
	if s := opts.optionsStrings(); len(s) > 0 {
		cmd = append(cmd, "-o", strings.Join(s, ","))
	}
	if opts.Debug {
		opts.Logger.Printf("callFusermount: executing %q", cmd)
	}
	proc, err := os.StartProcess(bin,
		cmd,
		&os.ProcAttr{
			Env:   []string{"_FUSE_COMMFD=3"},
			Files: []*os.File{os.Stdin, os.Stdout, os.Stderr, remote}})

	if err != nil {
		return
	}

	w, err := proc.Wait()
	if err != nil {
		return
	}
	if !w.Success() {
		err = fmt.Errorf("fusermount exited with code %v\n", w.Sys())
		return
	}

	fd, err = getConnection(local)
	if err != nil {
		return -1, err
	}

	return
}

// Create a FUSE FS on the specified mount point.  The returned
// mount point is always absolute.
func mount(mountPoint string, opts *MountOptions, ready chan<- error) (fd int, err error) {
	if opts.DirectMount || opts.DirectMountStrict {
		fd, err := mountDirect(mountPoint, opts, ready)
		if err == nil {
			return fd, nil
		} else if opts.Debug {
			opts.Logger.Printf("mount: failed to do direct mount: %s", err)
		}
		if opts.DirectMountStrict {
			return -1, err
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
		fd, err = callFusermount(mountPoint, opts)
		if err != nil {
			return
		}
	}
	// golang sets CLOEXEC on file descriptors when they are
	// acquired through normal operations (e.g. open).
	// Buf for fd, we have to set CLOEXEC manually
	syscall.CloseOnExec(fd)
	close(ready)
	return fd, err
}

func unmount(mountPoint string, opts *MountOptions) (err error) {
	if opts.DirectMount || opts.DirectMountStrict {
		// Attempt to directly unmount, if fails fallback to fusermount method
		err := syscall.Unmount(mountPoint, 0)
		if err == nil {
			return nil
		}
		if opts.DirectMountStrict {
			return err
		}
	}

	bin, err := fusermountBinary()
	if err != nil {
		return err
	}
	errBuf := bytes.Buffer{}
	cmd := exec.Command(bin, "-u", mountPoint)
	cmd.Stderr = &errBuf
	if opts.Debug {
		opts.Logger.Printf("unmount: executing %q", cmd.Args)
	}
	err = cmd.Run()
	if errBuf.Len() > 0 {
		return fmt.Errorf("%s (code %v)\n",
			errBuf.String(), err)
	}
	return err
}

// lookPathFallback - search binary in PATH and, if that fails,
// in fallbackDir. This is useful if PATH is possible empty.
func lookPathFallback(file string, fallbackDir string) (string, error) {
	binPath, err := exec.LookPath(file)
	if err == nil {
		return binPath, nil
	}

	abs := path.Join(fallbackDir, file)
	return exec.LookPath(abs)
}

// fusermountBinary returns the path to the `fusermount3` binary, or, if not
// found, the `fusermount` binary.
func fusermountBinary() (string, error) {
	if path, err := lookPathFallback("fusermount3", "/bin"); err == nil {
		return path, nil
	}
	return lookPathFallback("fusermount", "/bin")
}

func umountBinary() (string, error) {
	return lookPathFallback("umount", "/bin")
}
