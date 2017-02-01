package fs

import (
	"io"
	"os"
	"strconv"
	"syscall"

	"github.com/pkg/errors"
	"github.com/stevvooe/continuity/sysx"
)

var (
	kernel, major int
)

func init() {
	uts := &syscall.Utsname{}

	err := syscall.Uname(uts)
	if err != nil {
		panic(err)
	}

	p := [2][]byte{{}, {}}

	release := uts.Release
	i := 0
	for pi := 0; pi < len(p); pi++ {
		for release[i] != 0 {
			c := byte(release[i])
			i++
			if c == '.' || c == '-' {
				break
			}
			p[pi] = append(p[pi], c)
		}
	}
	kernel, err = strconv.Atoi(string(p[0]))
	if err != nil {
		panic(err)
	}
	major, err = strconv.Atoi(string(p[1]))
	if err != nil {
		panic(err)
	}
}

func copyFileInfo(fi os.FileInfo, name string) error {
	st := fi.Sys().(*syscall.Stat_t)
	if err := os.Lchown(name, int(st.Uid), int(st.Gid)); err != nil {
		return errors.Wrapf(err, "failed to chown %s", name)
	}

	if (fi.Mode() & os.ModeSymlink) != os.ModeSymlink {
		if err := os.Chmod(name, fi.Mode()); err != nil {
			return errors.Wrapf(err, "failed to chmod %s", name)
		}
	}

	if err := syscall.UtimesNano(name, []syscall.Timespec{st.Atim, st.Mtim}); err != nil {
		return errors.Wrapf(err, "failed to utime %s", name)
	}

	return nil
}

func copyFileContent(dst, src *os.File) error {
	if checkKernel(4, 5) {
		// Use copy_file_range to do in kernel copying
		// See https://lwn.net/Articles/659523/
		// 326 on x86_64
		st, err := src.Stat()
		if err != nil {
			return errors.Wrap(err, "unable to stat source")
		}
		n, _, e1 := syscall.Syscall6(326, src.Fd(), 0, dst.Fd(), 0, uintptr(st.Size()), 0)
		if e1 != 0 {
			return errors.Wrap(err, "copy_file_range failed")
		}
		if int64(n) != st.Size() {
			return errors.Wrapf(err, "short copy: %d of %d", int64(n), st.Size())
		}
		return nil
	}
	_, err := io.Copy(dst, src)
	return err
}

func checkKernel(k, m int) bool {
	return (kernel == k && major >= m) || kernel > k
}

func copyXAttrs(dst, src string) error {
	xattrKeys, err := sysx.LListxattr(src)
	if err != nil {
		return errors.Wrapf(err, "failed to list xattrs on %s", src)
	}
	for _, xattr := range xattrKeys {
		data, err := sysx.LGetxattr(src, xattr)
		if err != nil {
			return errors.Wrapf(err, "failed to get xattr %q on %s", xattr, src)
		}
		if err := sysx.LSetxattr(dst, xattr, data, 0); err != nil {
			return errors.Wrapf(err, "failed to set xattr %q on %s", xattr, dst)
		}
	}

	return nil
}

func copyDevice(dst string, fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("unsupported stat type")
	}
	return syscall.Mknod(dst, uint32(fi.Mode().Perm()), int(st.Rdev))
}
