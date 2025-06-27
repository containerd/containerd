// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fuse provides APIs to implement filesystems in
// userspace in terms of raw FUSE protocol.
//
// A filesystem is implemented by implementing its server that provides a
// RawFileSystem interface. Typically the server embeds
// NewDefaultRawFileSystem() and implements only subset of filesystem methods:
//
//	type MyFS struct {
//		fuse.RawFileSystem
//		...
//	}
//
//	func NewMyFS() *MyFS {
//		return &MyFS{
//			RawFileSystem: fuse.NewDefaultRawFileSystem(),
//			...
//		}
//	}
//
//	// Mkdir implements "mkdir" request handler.
//	//
//	// For other requests - not explicitly implemented by MyFS - ENOSYS
//	// will be typically returned to client.
//	func (fs *MyFS) Mkdir(...) {
//		...
//	}
//
// Then the filesystem can be mounted and served to a client (typically OS
// kernel) by creating Server:
//
//	fs := NewMyFS() // implements RawFileSystem
//	fssrv, err := fuse.NewServer(fs, mountpoint, &fuse.MountOptions{...})
//	if err != nil {
//		...
//	}
//
// and letting the server do its work:
//
//	// either synchronously - .Serve() blocks until the filesystem is unmounted.
//	fssrv.Serve()
//
//	// or in the background - .Serve() is spawned in another goroutine, but
//	// before interacting with fssrv from current context we have to wait
//	// until the filesystem mounting is complete.
//	go fssrv.Serve()
//	err = fssrv.WaitMount()
//	if err != nil {
//		...
//	}
//
// The server will serve clients by dispatching their requests to the
// filesystem implementation and conveying responses back. For example "mkdir"
// FUSE request dispatches to call
//
//	fs.Mkdir(*MkdirIn, ..., *EntryOut)
//
// "stat" to call
//
//	fs.GetAttr(*GetAttrIn, *AttrOut)
//
// etc. Please refer to RawFileSystem documentation for details.
//
// Typically, each call of the API happens in its own
// goroutine, so take care to make the file system thread-safe.
//
// Be careful when you access the FUSE mount from the same process. An access can
// tie up two OS threads (one on the request side and one on the FUSE server side).
// This can deadlock if there is no free thread to handle the FUSE server side.
// Run your program with GOMAXPROCS=1 to make the problem easier to reproduce,
// see https://github.com/hanwen/go-fuse/issues/261 for an example of that
// problem.
//
// # Higher level interfaces
//
// As said above this packages provides way to implement filesystems in terms of
// raw FUSE protocol.
//
// Package github.com/hanwen/go-fuse/v2/fs provides way to implement
// filesystems in terms of paths and/or inodes.
//
// # Mount styles
//
// The NewServer() handles mounting the filesystem, which
// involves opening `/dev/fuse` and calling the
// `mount(2)` syscall. The latter needs root permissions.
// This is handled in one of three ways:
//
// 1) go-fuse opens `/dev/fuse` and executes the `fusermount`
// setuid-root helper to call `mount(2)` for us. This is the default.
// Does not need root permissions but needs `fusermount` installed.
//
// 2) If `MountOptions.DirectMount` is set, go-fuse calls `mount(2)` itself.
// Needs root permissions, but works without `fusermount`.
//
// 3) If `mountPoint` has the magic `/dev/fd/N` syntax, it means that that a
// privileged parent process:
//
// * Opened /dev/fuse
//
// * Called mount(2) on a real mountpoint directory that we don't know about
//
// * Inherited the fd to /dev/fuse to us
//
// * Informs us about the fd number via /dev/fd/N
//
// This magic syntax originates from libfuse [1] and allows the FUSE server to
// run without any privileges and without needing `fusermount`, as the parent
// process performs all privileged operations.
//
// The "privileged parent" is usually a container manager like Singularity [2],
// but for testing, it can also be  the `mount.fuse3` helper with the
// `drop_privileges,setuid=$USER` flags. Example below for gocryptfs:
//
//	$ sudo mount.fuse3 "/usr/local/bin/gocryptfs#/tmp/cipher" /tmp/mnt -o drop_privileges,setuid=$USER
//
// [1] https://github.com/libfuse/libfuse/commit/64e11073b9347fcf9c6d1eea143763ba9e946f70
//
// [2] https://sylabs.io/guides/3.7/user-guide/bind_paths_and_mounts.html#fuse-mounts
//
// # Aborting a file system
//
// A caller that has an open file in a buggy or crashed FUSE
// filesystem will be hung. The easiest way to clean up this situation
// is through the fusectl filesystem. By writing into
// /sys/fs/fuse/connection/$ID/abort, reads from the FUSE device fail,
// and all callers receive ENOTCONN (transport endpoint not connected)
// on their pending syscalls.  The FUSE connection ID can be found as
// the Dev field in the Stat_t result for a file in the mount.
package fuse

import "log"

// Types for users to implement.

// The result of Read is an array of bytes, but for performance
// reasons, we can also return data as a file-descriptor/offset/size
// tuple.  If the backing store for a file is another filesystem, this
// reduces the amount of copying between the kernel and the FUSE
// server.  The ReadResult interface captures both cases.
type ReadResult interface {
	// Returns the raw bytes for the read, possibly using the
	// passed buffer. The buffer should be larger than the return
	// value from Size.
	Bytes(buf []byte) ([]byte, Status)

	// Size returns how many bytes this return value takes at most.
	Size() int

	// Done() is called after sending the data to the kernel.
	Done()
}

type MountOptions struct {
	AllowOther bool

	// Options are passed as -o string to fusermount.
	Options []string

	// Default is _DEFAULT_BACKGROUND_TASKS, 12.  This numbers
	// controls the allowed number of requests that relate to
	// async I/O.  Concurrency for synchronous I/O is not limited.
	MaxBackground int

	// MaxWrite is the max size for read and write requests. If 0, use
	// go-fuse default (currently 64 kiB).
	// This number is internally capped at MAX_KERNEL_WRITE (higher values don't make
	// sense).
	//
	// Non-direct-io reads are mostly served via kernel readahead, which is
	// additionally subject to the MaxReadAhead limit.
	//
	// Implementation notes:
	//
	// There's four values the Linux kernel looks at when deciding the request size:
	// * MaxWrite, passed via InitOut.MaxWrite. Limits the WRITE size.
	// * max_read, passed via a string mount option. Limits the READ size.
	//   go-fuse sets max_read equal to MaxWrite.
	//   You can see the current max_read value in /proc/self/mounts .
	// * MaxPages, passed via InitOut.MaxPages. In Linux 4.20 and later, the value
	//   can go up to 1 MiB and go-fuse calculates the MaxPages value acc.
	//   to MaxWrite, rounding up.
	//   On older kernels, the value is fixed at 128 kiB and the
	//   passed value is ignored. No request can be larger than MaxPages, so
	//   READ and WRITE are effectively capped at MaxPages.
	// * MaxReadAhead, passed via InitOut.MaxReadAhead.
	MaxWrite int

	// MaxReadAhead is the max read ahead size to use. It controls how much data the
	// kernel reads in advance to satisfy future read requests from applications.
	// How much exactly is subject to clever heuristics in the kernel
	// (see https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/tree/mm/readahead.c?h=v6.2-rc5#n375
	// if you are brave) and hence also depends on the kernel version.
	//
	// If 0, use kernel default. This number is capped at the kernel maximum
	// (128 kiB on Linux) and cannot be larger than MaxWrite.
	//
	// MaxReadAhead only affects buffered reads (=non-direct-io), but even then, the
	// kernel can and does send larger reads to satisfy read reqests from applications
	// (up to MaxWrite or VM_READAHEAD_PAGES=128 kiB, whichever is less).
	MaxReadAhead int

	// If IgnoreSecurityLabels is set, all security related xattr
	// requests will return NO_DATA without passing through the
	// user defined filesystem.  You should only set this if you
	// file system implements extended attributes, and you are not
	// interested in security labels.
	IgnoreSecurityLabels bool // ignoring labels should be provided as a fusermount mount option.

	// If RememberInodes is set, we will never forget inodes.
	// This may be useful for NFS.
	RememberInodes bool

	// Values shown in "df -T" and friends
	// First column, "Filesystem"
	FsName string

	// Second column, "Type", will be shown as "fuse." + Name
	Name string

	// If set, wrap the file system in a single-threaded locking wrapper.
	SingleThreaded bool

	// If set, return ENOSYS for Getxattr calls, so the kernel does not issue any
	// Xattr operations at all.
	DisableXAttrs bool

	// If set, print debugging information.
	Debug bool

	// If set, sink for debug statements.
	//
	// To increase signal/noise ratio Go-FUSE uses abbreviations in its debug log
	// output. Here is how to read it:
	//
	// - `iX` means `inode X`;
	// - `gX` means `generation X`;
	// - `tA` and `tE` means timeout for attributes and directory entry correspondingly;
	// - `[<off> +<size>)` means data range from `<off>` inclusive till `<off>+<size>` exclusive;
	// - `Xb` means `X bytes`.
	// - `pX` means the request originated from PID `x`. 0 means the request originated from the kernel.
	//
	// Every line is prefixed with either `rx <unique>` (receive from kernel) or `tx <unique>` (send to kernel)
	//
	// Example debug log output:
	//
	//     rx 2: LOOKUP i1 [".wcfs"] 6b p5874
	//     tx 2:     OK, {i3 g2 tE=1s tA=1s {M040755 SZ=0 L=0 1000:1000 B0*0 i0:3 A 0.000000 M 0.000000 C 0.000000}}
	//     rx 3: LOOKUP i3 ["zurl"] 5b p5874
	//     tx 3:     OK, {i4 g3 tE=1s tA=1s {M0100644 SZ=33 L=1 1000:1000 B0*0 i0:4 A 0.000000 M 0.000000 C 0.000000}}
	//     rx 4: OPEN i4 {O_RDONLY,0x8000} p5874
	//     tx 4:     38=function not implemented, {Fh 0 }
	//     rx 5: READ i4 {Fh 0 [0 +4096)  L 0 RDONLY,0x8000} p5874
	//     tx 5:     OK,  33b data "file:///"...
	//     rx 6: GETATTR i4 {Fh 0} p5874
	//     tx 6:     OK, {tA=1s {M0100644 SZ=33 L=1 1000:1000 B0*0 i0:4 A 0.000000 M 0.000000 C 0.000000}}
	//     rx 7: FLUSH i4 {Fh 0} p5874
	//     tx 7:     OK
	//     rx 8: LOOKUP i1 ["head"] 5b p5874
	//     tx 8:     OK, {i5 g4 tE=1s tA=1s {M040755 SZ=0 L=0 1000:1000 B0*0 i0:5 A 0.000000 M 0.000000 C 0.000000}}
	//     rx 9: LOOKUP i5 ["bigfile"] 8b p5874
	//     tx 9:     OK, {i6 g5 tE=1s tA=1s {M040755 SZ=0 L=0 1000:1000 B0*0 i0:6 A 0.000000 M 0.000000 C 0.000000}}
	//     rx 10: FLUSH i4 {Fh 0} p5874
	//     tx 10:     OK
	//     rx 11: GETATTR i1 {Fh 0} p5874
	//     tx 11:     OK, {tA=1s {M040755 SZ=0 L=1 1000:1000 B0*0 i0:1 A 0.000000 M 0.000000 C 0.000000}}
	Logger *log.Logger

	// If set, ask kernel to forward file locks to FUSE. If using,
	// you must implement the GetLk/SetLk/SetLkw methods.
	EnableLocks bool

	// If set, the kernel caches all Readlink return values. The
	// filesystem must use content notification to force the
	// kernel to issue a new Readlink call.
	EnableSymlinkCaching bool

	// If set, ask kernel not to do automatic data cache invalidation.
	// The filesystem is fully responsible for invalidating data cache.
	ExplicitDataCacheControl bool

	// SyncRead is off by default, which means that go-fuse enable the
	// FUSE_CAP_ASYNC_READ capability.
	// The kernel then submits multiple concurrent reads to service
	// userspace requests and kernel readahead.
	//
	// Setting SyncRead disables the FUSE_CAP_ASYNC_READ capability.
	// The kernel then only sends one read request per file handle at a time,
	// and orders the requests by offset.
	//
	// This is useful if reading out of order or concurrently is expensive for
	// (example: Amazon Cloud Drive).
	//
	// See the comment to FUSE_CAP_ASYNC_READ in
	// https://github.com/libfuse/libfuse/blob/master/include/fuse_common.h
	// for more details.
	SyncRead bool

	// If set, fuse will first attempt to use syscall.Mount instead of
	// fusermount to mount the filesystem. This will not update /etc/mtab
	// but might be needed if fusermount is not available.
	// Also, Server.Unmount will attempt syscall.Unmount before calling
	// fusermount.
	DirectMount bool

	// DirectMountStrict is like DirectMount but no fallback to fusermount is
	// performed. If both DirectMount and DirectMountStrict are set,
	// DirectMountStrict wins.
	DirectMountStrict bool

	// DirectMountFlags are the mountflags passed to syscall.Mount. If zero, the
	// default value used by fusermount are used: syscall.MS_NOSUID|syscall.MS_NODEV.
	//
	// If you actually *want* zero flags, pass syscall.MS_MGC_VAL, which is ignored
	// by the kernel. See `man 2 mount` for details about MS_MGC_VAL.
	DirectMountFlags uintptr

	// EnableAcls enables kernel ACL support.
	//
	// See the comments to FUSE_CAP_POSIX_ACL
	// in https://github.com/libfuse/libfuse/blob/master/include/fuse_common.h
	// for details.
	EnableAcl bool

	// Disable ReadDirPlus capability so ReadDir is used instead. Simple
	// directory queries (i.e. 'ls' without '-l') can be faster with
	// ReadDir, as no per-file stat calls are needed
	DisableReadDirPlus bool

	// Disable splicing from files to the FUSE device.
	DisableSplice bool

	// Maximum stacking depth for passthrough files. Defaults to 1.
	MaxStackDepth int

	// Enable ID-mapped mount if the Kernel supports it.
	// ID-mapped mount allows the device to be mounted on the system
	// with the IDs remapped (via mount_setattr, move_mount syscalls) to
	// those of the user on the local system.
	//
	// Enabling this flag automatically sets the "default_permissions"
	// mount option. This is required by FUSE to delegate the UID/GID-based
	// permission checks to the kernel. For requests that create new inodes,
	// FUSE will send the mapped UID/GIDs. For all other requests, FUSE
	// will send "-1".
	IDMappedMount bool
}

// RawFileSystem is an interface close to the FUSE wire protocol.
//
// Unless you really know what you are doing, you should not implement
// this, but rather the interfaces associated with
// fs.InodeEmbedder. The details of getting interactions with open
// files, renames, and threading right etc. are somewhat tricky and
// not very interesting.
//
// Each FUSE request results in a corresponding method called by Server.
// Several calls may be made simultaneously, because the server typically calls
// each method in separate goroutine.
//
// A null implementation is provided by NewDefaultRawFileSystem.
//
// After a successful FUSE API call returns, you may not read input or
// write output data: for performance reasons, memory is reused for
// following requests, and reading/writing the request data will lead
// to race conditions.  If you spawn a background routine from a FUSE
// API call, any incoming request data it wants to reference should be
// copied over.
//
// If a FUSE API call is canceled (which is signaled by closing the
// `cancel` channel), the API call should return EINTR. In this case,
// the outstanding request data is not reused, so the API call may
// return EINTR without ensuring that child contexts have successfully
// completed.
type RawFileSystem interface {
	String() string

	// If called, provide debug output through the log package.
	SetDebug(debug bool)

	// Lookup is called by the kernel when the VFS wants to know
	// about a file inside a directory. Many lookup calls can
	// occur in parallel, but only one call happens for each (dir,
	// name) pair.
	Lookup(cancel <-chan struct{}, header *InHeader, name string, out *EntryOut) (status Status)

	// Forget is called when the kernel discards entries from its
	// dentry cache. This happens on unmount, and when the kernel
	// is short on memory. Since it is not guaranteed to occur at
	// any moment, and since there is no return value, Forget
	// should not do I/O, as there is no channel to report back
	// I/O errors.
	Forget(nodeid, nlookup uint64)

	// Attributes.
	GetAttr(cancel <-chan struct{}, input *GetAttrIn, out *AttrOut) (code Status)
	SetAttr(cancel <-chan struct{}, input *SetAttrIn, out *AttrOut) (code Status)

	// Modifying structure.
	Mknod(cancel <-chan struct{}, input *MknodIn, name string, out *EntryOut) (code Status)
	Mkdir(cancel <-chan struct{}, input *MkdirIn, name string, out *EntryOut) (code Status)
	Unlink(cancel <-chan struct{}, header *InHeader, name string) (code Status)
	Rmdir(cancel <-chan struct{}, header *InHeader, name string) (code Status)
	Rename(cancel <-chan struct{}, input *RenameIn, oldName string, newName string) (code Status)
	Link(cancel <-chan struct{}, input *LinkIn, filename string, out *EntryOut) (code Status)

	Symlink(cancel <-chan struct{}, header *InHeader, pointedTo string, linkName string, out *EntryOut) (code Status)
	Readlink(cancel <-chan struct{}, header *InHeader) (out []byte, code Status)
	Access(cancel <-chan struct{}, input *AccessIn) (code Status)

	// Extended attributes.

	// GetXAttr reads an extended attribute, and should return the
	// number of bytes. If the buffer is too small, return ERANGE,
	// with the required buffer size.
	GetXAttr(cancel <-chan struct{}, header *InHeader, attr string, dest []byte) (sz uint32, code Status)

	// ListXAttr lists extended attributes as '\0' delimited byte
	// slice, and return the number of bytes. If the buffer is too
	// small, return ERANGE, with the required buffer size.
	ListXAttr(cancel <-chan struct{}, header *InHeader, dest []byte) (uint32, Status)

	// SetAttr writes an extended attribute.
	SetXAttr(cancel <-chan struct{}, input *SetXAttrIn, attr string, data []byte) Status

	// RemoveXAttr removes an extended attribute.
	RemoveXAttr(cancel <-chan struct{}, header *InHeader, attr string) (code Status)

	// File handling.
	Create(cancel <-chan struct{}, input *CreateIn, name string, out *CreateOut) (code Status)
	Open(cancel <-chan struct{}, input *OpenIn, out *OpenOut) (status Status)
	Read(cancel <-chan struct{}, input *ReadIn, buf []byte) (ReadResult, Status)
	Lseek(cancel <-chan struct{}, in *LseekIn, out *LseekOut) Status

	// File locking
	GetLk(cancel <-chan struct{}, input *LkIn, out *LkOut) (code Status)
	SetLk(cancel <-chan struct{}, input *LkIn) (code Status)
	SetLkw(cancel <-chan struct{}, input *LkIn) (code Status)

	Release(cancel <-chan struct{}, input *ReleaseIn)
	Write(cancel <-chan struct{}, input *WriteIn, data []byte) (written uint32, code Status)
	CopyFileRange(cancel <-chan struct{}, input *CopyFileRangeIn) (written uint32, code Status)
	Ioctl(cancel <-chan struct{}, input *IoctlIn, inbuf []byte, output *IoctlOut, outbuf []byte) (code Status)

	Flush(cancel <-chan struct{}, input *FlushIn) Status
	Fsync(cancel <-chan struct{}, input *FsyncIn) (code Status)
	Fallocate(cancel <-chan struct{}, input *FallocateIn) (code Status)

	// Directory handling
	OpenDir(cancel <-chan struct{}, input *OpenIn, out *OpenOut) (status Status)
	ReadDir(cancel <-chan struct{}, input *ReadIn, out *DirEntryList) Status
	ReadDirPlus(cancel <-chan struct{}, input *ReadIn, out *DirEntryList) Status
	ReleaseDir(input *ReleaseIn)
	FsyncDir(cancel <-chan struct{}, input *FsyncIn) (code Status)

	StatFs(cancel <-chan struct{}, input *InHeader, out *StatfsOut) (code Status)

	Statx(cancel <-chan struct{}, input *StatxIn, out *StatxOut) (code Status)
	// This is called on processing the first request. The
	// filesystem implementation can use the server argument to
	// talk back to the kernel (through notify methods).
	Init(*Server)

	// Called after processing the last request.
	OnUnmount()
}
