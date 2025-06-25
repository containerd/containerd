// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

var (
	isTest         bool
	writeFlagNames = newFlagNames([]flagNameEntry{
		{WRITE_CACHE, "CACHE"},
		{WRITE_LOCKOWNER, "LOCKOWNER"},
	})
	readFlagNames = newFlagNames([]flagNameEntry{
		{READ_LOCKOWNER, "LOCKOWNER"},
	})
	initFlagNames = newFlagNames([]flagNameEntry{
		{CAP_ASYNC_READ, "ASYNC_READ"},
		{CAP_POSIX_LOCKS, "POSIX_LOCKS"},
		{CAP_FILE_OPS, "FILE_OPS"},
		{CAP_ATOMIC_O_TRUNC, "ATOMIC_O_TRUNC"},
		{CAP_EXPORT_SUPPORT, "EXPORT_SUPPORT"},
		{CAP_BIG_WRITES, "BIG_WRITES"},
		{CAP_DONT_MASK, "DONT_MASK"},
		{CAP_SPLICE_WRITE, "SPLICE_WRITE"},
		{CAP_SPLICE_MOVE, "SPLICE_MOVE"},
		{CAP_SPLICE_READ, "SPLICE_READ"},
		{CAP_FLOCK_LOCKS, "FLOCK_LOCKS"},
		{CAP_IOCTL_DIR, "IOCTL_DIR"},
		{CAP_AUTO_INVAL_DATA, "AUTO_INVAL_DATA"},
		{CAP_READDIRPLUS, "READDIRPLUS"},
		{CAP_READDIRPLUS_AUTO, "READDIRPLUS_AUTO"},
		{CAP_ASYNC_DIO, "ASYNC_DIO"},
		{CAP_WRITEBACK_CACHE, "WRITEBACK_CACHE"},
		{CAP_NO_OPEN_SUPPORT, "NO_OPEN_SUPPORT"},
		{CAP_PARALLEL_DIROPS, "PARALLEL_DIROPS"},
		{CAP_POSIX_ACL, "POSIX_ACL"},
		{CAP_HANDLE_KILLPRIV, "HANDLE_KILLPRIV"},
		{CAP_ABORT_ERROR, "ABORT_ERROR"},
		{CAP_MAX_PAGES, "MAX_PAGES"},
		{CAP_CACHE_SYMLINKS, "CACHE_SYMLINKS"},
		{CAP_SECURITY_CTX, "SECURITY_CTX"},
		{CAP_HAS_INODE_DAX, "HAS_INODE_DAX"},
		{CAP_CREATE_SUPP_GROUP, "CREATE_SUPP_GROUP"},
		{CAP_HAS_EXPIRE_ONLY, "HAS_EXPIRE_ONLY"},
		{CAP_DIRECT_IO_ALLOW_MMAP, "DIRECT_IO_ALLOW_MMAP"},
		{CAP_PASSTHROUGH, "PASSTHROUGH"},
		{CAP_NO_EXPORT_SUPPORT, "NO_EXPORT_SUPPORT"},
		{CAP_HAS_RESEND, "HAS_RESEND"},
		{CAP_ALLOW_IDMAP, "ALLOW_IDMAP"},
	})
	releaseFlagNames = newFlagNames([]flagNameEntry{
		{RELEASE_FLUSH, "FLUSH"},
	})
	openFlagNames = newFlagNames([]flagNameEntry{
		{int64(os.O_WRONLY), "WRONLY"},
		{int64(os.O_RDWR), "RDWR"},
		{int64(os.O_APPEND), "APPEND"},
		{int64(syscall.O_ASYNC), "ASYNC"},
		{int64(os.O_CREATE), "CREAT"},
		{int64(os.O_EXCL), "EXCL"},
		{int64(syscall.O_NOCTTY), "NOCTTY"},
		{int64(syscall.O_NONBLOCK), "NONBLOCK"},
		{int64(os.O_SYNC), "SYNC"},
		{int64(os.O_TRUNC), "TRUNC"},
		{int64(syscall.O_CLOEXEC), "CLOEXEC"},
		{int64(syscall.O_DIRECTORY), "DIRECTORY"},
	})
	fuseOpenFlagNames = newFlagNames([]flagNameEntry{
		{FOPEN_DIRECT_IO, "DIRECT"},
		{FOPEN_KEEP_CACHE, "CACHE"},
		{FOPEN_NONSEEKABLE, "NONSEEK"},
		{FOPEN_CACHE_DIR, "CACHE_DIR"},
		{FOPEN_STREAM, "STREAM"},
		{FOPEN_NOFLUSH, "NOFLUSH"},
		{FOPEN_PARALLEL_DIRECT_WRITES, "PARALLEL_DIRECT_WRITES"},
		{FOPEN_PASSTHROUGH, "PASSTHROUGH"},
	})
	ioctlFlagNames = newFlagNames([]flagNameEntry{
		{IOCTL_COMPAT, "COMPAT"},
		{IOCTL_UNRESTRICTED, "UNRESTRICTED"},
		{IOCTL_RETRY, "RETRY"},
		{IOCTL_DIR, "DIR"},
	})
	accessFlagName = newFlagNames([]flagNameEntry{
		{X_OK, "x"},
		{W_OK, "w"},
		{R_OK, "r"},
	})
	getAttrFlagNames = newFlagNames([]flagNameEntry{
		{FUSE_GETATTR_FH, "FH"},
	})
)

// flagNames associate flag bits to their names.
type flagNames [64]flagNameEntry

// flagNameEntry describes one flag value.
//
// Usually a flag constitues only one bit, but, for example at least O_SYNC and
// O_TMPFILE are represented by a value with two bits set. To handle such
// situations we map all bits of a flag to the same flagNameEntry.
type flagNameEntry struct {
	bits int64
	name string
}

// newFlagNames creates flagNames from flag->name map.
func newFlagNames(names []flagNameEntry) *flagNames {
	var v flagNames
	for _, e := range names {
		v.set(e.bits, e.name)
	}
	return &v
}

// set associates flag value with name.
func (names *flagNames) set(flag int64, name string) {
	entry := flagNameEntry{bits: flag, name: name}
	for i := 0; i < 64; i++ {
		if flag&(1<<i) != 0 {
			if ie := names[i]; ie.bits != 0 && isTest {
				panic(fmt.Sprintf("%s (%x) overlaps with %s (%x)", name, flag, ie.name, ie.bits))
			}
			names[i] = entry
		}
	}
}

func flagString(names *flagNames, fl int64, def string) string {
	s := []string{}
	// emit flags in their numeric order
	for i := range names {
		entry := &names[i]
		if entry.bits == 0 {
			continue
		}
		if fl&entry.bits == entry.bits {
			s = append(s, entry.name)
			fl ^= entry.bits
			if fl == 0 {
				break
			}
		}
	}
	if fl != 0 {
		s = append(s, fmt.Sprintf("0x%x", fl))
	}
	if len(s) == 0 && def != "" {
		return def
	}

	return strings.Join(s, ",")
}

func (in *ForgetIn) string() string {
	return fmt.Sprintf("{Nlookup=%d}", in.Nlookup)
}

func (in *_BatchForgetIn) string() string {
	return fmt.Sprintf("{Count=%d}", in.Count)
}

func (in *MkdirIn) string() string {
	return fmt.Sprintf("{0%o (mask 0%o)}", in.Mode, in.Umask)
}

func (in *Rename1In) string() string {
	return fmt.Sprintf("{i%d}", in.Newdir)
}

func (in *RenameIn) string() string {
	return fmt.Sprintf("{i%d %x}", in.Newdir, in.Flags)
}

func (in *SetAttrIn) string() string {
	s := []string{}
	if in.Valid&FATTR_MODE != 0 {
		s = append(s, fmt.Sprintf("mode 0%o", in.Mode))
	}
	if in.Valid&FATTR_UID != 0 {
		s = append(s, fmt.Sprintf("uid %d", in.Uid))
	}
	if in.Valid&FATTR_GID != 0 {
		s = append(s, fmt.Sprintf("gid %d", in.Gid))
	}
	if in.Valid&FATTR_SIZE != 0 {
		s = append(s, fmt.Sprintf("size %d", in.Size))
	}
	if in.Valid&FATTR_ATIME != 0 {
		s = append(s, fmt.Sprintf("atime %d.%09d", in.Atime, in.Atimensec))
	}
	if in.Valid&FATTR_MTIME != 0 {
		s = append(s, fmt.Sprintf("mtime %d.%09d", in.Mtime, in.Mtimensec))
	}
	if in.Valid&FATTR_FH != 0 {
		s = append(s, fmt.Sprintf("fh %d", in.Fh))
	}
	// TODO - FATTR_ATIME_NOW = (1 << 7), FATTR_MTIME_NOW = (1 << 8), FATTR_LOCKOWNER = (1 << 9)
	return fmt.Sprintf("{%s}", strings.Join(s, ", "))
}

func (in *ReleaseIn) string() string {
	return fmt.Sprintf("{Fh %d %s %s L%d}",
		in.Fh, flagString(openFlagNames, int64(in.Flags), ""),
		flagString(releaseFlagNames, int64(in.ReleaseFlags), ""),
		in.LockOwner)
}

func (in *OpenIn) string() string {
	return fmt.Sprintf("{%s}", flagString(openFlagNames, int64(in.Flags), "O_RDONLY"))
}

func (in *OpenOut) string() string {
	backing := ""
	if in.BackingID != 0 {
		backing = fmt.Sprintf("backing=%d ", in.BackingID)
	}
	return fmt.Sprintf("{Fh %d %s%s}", in.Fh, backing,
		flagString(fuseOpenFlagNames, int64(in.OpenFlags), ""))
}

func (in *InitIn) string() string {
	return fmt.Sprintf("{%d.%d Ra %d %s}",
		in.Major, in.Minor, in.MaxReadAhead,
		flagString(initFlagNames, int64(in.Flags64()), ""))
}

func (o *InitOut) string() string {
	return fmt.Sprintf("{%d.%d Ra %d %s %d/%d Wr %d Tg %d MaxPages %d MaxStack %d}",
		o.Major, o.Minor, o.MaxReadAhead,
		flagString(initFlagNames, int64(o.Flags64()), ""),
		o.CongestionThreshold, o.MaxBackground, o.MaxWrite,
		o.TimeGran, o.MaxPages, o.MaxStackDepth)
}

func (s *FsyncIn) string() string {
	return fmt.Sprintf("{Fh %d Flags %x}", s.Fh, s.FsyncFlags)
}

func (in *SetXAttrIn) string() string {
	return fmt.Sprintf("{sz %d f%o}", in.Size, in.Flags)
}

func (in *GetXAttrIn) string() string {
	return fmt.Sprintf("{sz %d}", in.Size)
}

func (o *GetXAttrOut) string() string {
	return fmt.Sprintf("{sz %d}", o.Size)
}

func (in *AccessIn) string() string {
	return fmt.Sprintf("{u=%d g=%d %s}",
		in.Uid,
		in.Gid,
		flagString(accessFlagName, int64(in.Mask), ""))
}

func (in *FlushIn) string() string {
	return fmt.Sprintf("{Fh %d}", in.Fh)
}

func (o *AttrOut) string() string {
	return fmt.Sprintf(
		"{tA=%gs %v}",
		ft(o.AttrValid, o.AttrValidNsec), &o.Attr)
}

// ft converts (seconds , nanoseconds) -> float(seconds)
func ft(tsec uint64, tnsec uint32) float64 {
	return float64(tsec) + float64(tnsec)*1e-9
}

// Returned by LOOKUP
func (o *EntryOut) string() string {
	return fmt.Sprintf("{n%d g%d tE=%gs tA=%gs %v}",
		o.NodeId, o.Generation, ft(o.EntryValid, o.EntryValidNsec),
		ft(o.AttrValid, o.AttrValidNsec), &o.Attr)
}

func (o *CreateOut) string() string {
	return fmt.Sprintf("{n%d g%d %v %v}", o.NodeId, o.Generation, &o.EntryOut, o.OpenOut.string())
}

func (o *StatfsOut) string() string {
	return fmt.Sprintf(
		"{blocks (%d,%d)/%d files %d/%d bs%d nl%d frs%d}",
		o.Bfree, o.Bavail, o.Blocks, o.Ffree, o.Files,
		o.Bsize, o.NameLen, o.Frsize)
}

func (o *NotifyInvalEntryOut) string() string {
	return fmt.Sprintf("{parent i%d sz %d}", o.Parent, o.NameLen)
}

func (o *NotifyInvalInodeOut) string() string {
	return fmt.Sprintf("{i%d [%d +%d)}", o.Ino, o.Off, o.Length)
}

func (o *NotifyInvalDeleteOut) string() string {
	return fmt.Sprintf("{parent i%d ch i%d sz %d}", o.Parent, o.Child, o.NameLen)
}

func (o *NotifyStoreOut) string() string {
	return fmt.Sprintf("{n%d [%d +%d)}", o.Nodeid, o.Offset, o.Size)
}

func (o *NotifyRetrieveOut) string() string {
	return fmt.Sprintf("{> %d: n%d [%d +%d)}", o.NotifyUnique, o.Nodeid, o.Offset, o.Size)
}

func (i *NotifyRetrieveIn) string() string {
	return fmt.Sprintf("{[%d +%d)}", i.Offset, i.Size)
}

func (f *FallocateIn) string() string {
	return fmt.Sprintf("{Fh %d [%d +%d) mod 0%o}",
		f.Fh, f.Offset, f.Length, f.Mode)
}

func (f *LinkIn) string() string {
	return fmt.Sprintf("{Oldnodeid: n%d}", f.Oldnodeid)
}

func (o *WriteOut) string() string {
	return fmt.Sprintf("{%db }", o.Size)

}
func (i *CopyFileRangeIn) string() string {
	return fmt.Sprintf("{Fh %d [%d +%d) => n%d Fh %d [%d, %d)}",
		i.FhIn, i.OffIn, i.Len, i.NodeIdOut, i.FhOut, i.OffOut, i.Len)
}

func (in *InterruptIn) string() string {
	return fmt.Sprintf("{ix %d}", in.Unique)
}

var seekNames = map[uint32]string{
	0: "SET",
	1: "CUR",
	2: "END",
	3: "DATA",
	4: "HOLE",
}

func (in *LseekIn) string() string {
	return fmt.Sprintf("{Fh %d [%s +%d)}", in.Fh,
		seekNames[in.Whence], in.Offset)
}

func (o *LseekOut) string() string {
	return fmt.Sprintf("{%d}", o.Offset)
}

func (p *_PollIn) string() string {
	return fmt.Sprintf("Fh %d Kh %d Flags 0x%x", p.Fh, p.Kh, p.Flags)
}

// Print pretty prints FUSE data types for kernel communication
func Print(obj interface{}) string {
	t, ok := obj.(interface {
		string() string
	})
	if ok {
		return t.string()
	}
	return fmt.Sprintf("%T: %v", obj, obj)
}

func (a *Attr) string() string {
	return fmt.Sprintf(
		"{M0%o SZ=%d L=%d "+
			"%d:%d "+
			"B%d*%d i%d:%d "+
			"A %f "+
			"M %f "+
			"C %f}",
		a.Mode, a.Size, a.Nlink,
		a.Uid, a.Gid,
		a.Blocks, a.Blksize,
		a.Rdev, a.Ino, ft(a.Atime, a.Atimensec), ft(a.Mtime, a.Mtimensec),
		ft(a.Ctime, a.Ctimensec))
}

func (m *BackingMap) string() string {
	return fmt.Sprintf("{fd %d, flags 0x%x}", m.Fd, m.Flags)
}

func (o *IoctlIn) string() string {
	return fmt.Sprintf("{Fh %d Flags %s Cmd 0x%x Arg 0x%x, insz %d outsz %d}",
		o.Fh,
		flagString(ioctlFlagNames, int64(o.Flags), ""),
		o.Cmd, o.Arg, o.InSize, o.OutSize)
}

func (o *IoctlOut) string() string {
	return fmt.Sprintf("{Result %d Flags %s Iovs %d/%d",
		o.Result,
		flagString(ioctlFlagNames, int64(o.Flags), ""),
		o.InIovs, o.OutIovs)
}
