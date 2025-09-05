// See the file LICENSE for copyright and licensing information.

// Derived from FUSE's fuse_kernel.h, which carries this notice:
/*
   This file defines the kernel interface of FUSE
   Copyright (C) 2001-2007  Miklos Szeredi <miklos@szeredi.hu>


   This -- and only this -- header file may also be distributed under
   the terms of the BSD Licence as follows:

   Copyright (C) 2001-2007 Miklos Szeredi. All rights reserved.

   Redistribution and use in source and binary forms, with or without
   modification, are permitted provided that the following conditions
   are met:
   1. Redistributions of source code must retain the above copyright
      notice, this list of conditions and the following disclaimer.
   2. Redistributions in binary form must reproduce the above copyright
      notice, this list of conditions and the following disclaimer in the
      documentation and/or other materials provided with the distribution.

   THIS SOFTWARE IS PROVIDED BY AUTHOR AND CONTRIBUTORS ``AS IS'' AND
   ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
   IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
   ARE DISCLAIMED.  IN NO EVENT SHALL AUTHOR OR CONTRIBUTORS BE LIABLE
   FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
   DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS
   OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION)
   HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT
   LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY
   OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF
   SUCH DAMAGE.
*/

package fuse

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The FUSE version implemented by the package.
const (
	protoVersionMinMajor = 7
	protoVersionMinMinor = 17
	protoVersionMaxMajor = 7
	protoVersionMaxMinor = 33
)

const (
	rootID = 1
)

// AttrFlags are bit flags that can be seen in Attr.Flags.
type AttrFlags uint32

const (
	// Node is a submount root.
	//
	// Don't use unless `Conn.Features` includes `InitSubMounts`.
	//
	// This doesn't seem to be usable outside of `virtio_fs``.
	attrSubMount AttrFlags = 1 << 0
)

var attrFlagsNames = []flagName{
	{uint32(attrSubMount), "AttrSubMount"},
}

func (fl AttrFlags) String() string {
	return flagString(uint32(fl), attrFlagsNames)
}

type attr struct {
	Ino       uint64
	Size      uint64
	Blocks    uint64
	Atime     uint64
	Mtime     uint64
	Ctime     uint64
	AtimeNsec uint32
	MtimeNsec uint32
	CtimeNsec uint32
	Mode      uint32
	Nlink     uint32
	Uid       uint32
	Gid       uint32
	Rdev      uint32
	Blksize   uint32
	Flags     uint32
}

type kstatfs struct {
	Blocks  uint64
	Bfree   uint64
	Bavail  uint64
	Files   uint64
	Ffree   uint64
	Bsize   uint32
	Namelen uint32
	Frsize  uint32
	_       uint32
	Spare   [6]uint32
}

// GetattrFlags are bit flags that can be seen in GetattrRequest.
type GetattrFlags uint32

const (
	// Indicates the handle is valid.
	GetattrFh GetattrFlags = 1 << 0
)

var getattrFlagsNames = []flagName{
	{uint32(GetattrFh), "GetattrFh"},
}

func (fl GetattrFlags) String() string {
	return flagString(uint32(fl), getattrFlagsNames)
}

// The SetattrValid are bit flags describing which fields in the SetattrRequest
// are included in the change.
type SetattrValid uint32

const (
	SetattrMode        SetattrValid = 1 << 0
	SetattrUid         SetattrValid = 1 << 1
	SetattrGid         SetattrValid = 1 << 2
	SetattrSize        SetattrValid = 1 << 3
	SetattrAtime       SetattrValid = 1 << 4
	SetattrMtime       SetattrValid = 1 << 5
	SetattrHandle      SetattrValid = 1 << 6
	SetattrAtimeNow    SetattrValid = 1 << 7
	SetattrMtimeNow    SetattrValid = 1 << 8
	SetattrLockOwner   SetattrValid = 1 << 9 // http://www.mail-archive.com/git-commits-head@vger.kernel.org/msg27852.html
	SetattrCTime       SetattrValid = 1 << 10
	SetattrKillSUIDGID SetattrValid = 1 << 11
)

func (fl SetattrValid) Mode() bool               { return fl&SetattrMode != 0 }
func (fl SetattrValid) Uid() bool                { return fl&SetattrUid != 0 }
func (fl SetattrValid) Gid() bool                { return fl&SetattrGid != 0 }
func (fl SetattrValid) Size() bool               { return fl&SetattrSize != 0 }
func (fl SetattrValid) Atime() bool              { return fl&SetattrAtime != 0 }
func (fl SetattrValid) Mtime() bool              { return fl&SetattrMtime != 0 }
func (fl SetattrValid) Handle() bool             { return fl&SetattrHandle != 0 }
func (fl SetattrValid) AtimeNow() bool           { return fl&SetattrAtimeNow != 0 }
func (fl SetattrValid) MtimeNow() bool           { return fl&SetattrMtimeNow != 0 }
func (fl SetattrValid) LockOwner() bool          { return fl&SetattrLockOwner != 0 }
func (fl SetattrValid) SetattrCTime() bool       { return fl&SetattrCTime != 0 }
func (fl SetattrValid) SetattrKillSUIDGID() bool { return fl&SetattrKillSUIDGID != 0 }

func (fl SetattrValid) String() string {
	return flagString(uint32(fl), setattrValidNames)
}

var setattrValidNames = []flagName{
	{uint32(SetattrMode), "SetattrMode"},
	{uint32(SetattrUid), "SetattrUid"},
	{uint32(SetattrGid), "SetattrGid"},
	{uint32(SetattrSize), "SetattrSize"},
	{uint32(SetattrAtime), "SetattrAtime"},
	{uint32(SetattrMtime), "SetattrMtime"},
	{uint32(SetattrHandle), "SetattrHandle"},
	{uint32(SetattrAtimeNow), "SetattrAtimeNow"},
	{uint32(SetattrMtimeNow), "SetattrMtimeNow"},
	{uint32(SetattrLockOwner), "SetattrLockOwner"},
	{uint32(SetattrCTime), "SetattrCTime"},
	{uint32(SetattrKillSUIDGID), "SetattrKillSUIDGID"},
}

// Flags that can be seen in OpenRequest.Flags.
const (
	// Access modes. These are not 1-bit flags, but alternatives where
	// only one can be chosen. See the IsReadOnly etc convenience
	// methods.
	OpenReadOnly  OpenFlags = syscall.O_RDONLY
	OpenWriteOnly OpenFlags = syscall.O_WRONLY
	OpenReadWrite OpenFlags = syscall.O_RDWR

	// File was opened in append-only mode, all writes will go to end
	// of file. FreeBSD does not provide this information.
	OpenAppend    OpenFlags = syscall.O_APPEND
	OpenCreate    OpenFlags = syscall.O_CREAT
	OpenDirectory OpenFlags = syscall.O_DIRECTORY
	OpenExclusive OpenFlags = syscall.O_EXCL
	OpenNonblock  OpenFlags = syscall.O_NONBLOCK
	OpenSync      OpenFlags = syscall.O_SYNC
	OpenTruncate  OpenFlags = syscall.O_TRUNC
)

// OpenAccessModeMask is a bitmask that separates the access mode
// from the other flags in OpenFlags.
const OpenAccessModeMask OpenFlags = syscall.O_ACCMODE

// OpenFlags are the O_FOO flags passed to open/create/etc calls. For
// example, os.O_WRONLY | os.O_APPEND.
type OpenFlags uint32

func (fl OpenFlags) String() string {
	// O_RDONLY, O_RWONLY, O_RDWR are not flags
	s := accModeName(fl & OpenAccessModeMask)
	flags := uint32(fl &^ OpenAccessModeMask)
	if flags != 0 {
		s = s + "+" + flagString(flags, openFlagNames)
	}
	return s
}

// Return true if OpenReadOnly is set.
func (fl OpenFlags) IsReadOnly() bool {
	return fl&OpenAccessModeMask == OpenReadOnly
}

// Return true if OpenWriteOnly is set.
func (fl OpenFlags) IsWriteOnly() bool {
	return fl&OpenAccessModeMask == OpenWriteOnly
}

// Return true if OpenReadWrite is set.
func (fl OpenFlags) IsReadWrite() bool {
	return fl&OpenAccessModeMask == OpenReadWrite
}

func accModeName(flags OpenFlags) string {
	switch flags {
	case OpenReadOnly:
		return "OpenReadOnly"
	case OpenWriteOnly:
		return "OpenWriteOnly"
	case OpenReadWrite:
		return "OpenReadWrite"
	default:
		return ""
	}
}

var openFlagNames = []flagName{
	{uint32(OpenAppend), "OpenAppend"},
	{uint32(OpenCreate), "OpenCreate"},
	{uint32(OpenDirectory), "OpenDirectory"},
	{uint32(OpenExclusive), "OpenExclusive"},
	{uint32(OpenNonblock), "OpenNonblock"},
	{uint32(OpenSync), "OpenSync"},
	{uint32(OpenTruncate), "OpenTruncate"},
}

// OpenRequestFlags are the FUSE-specific flags in an OpenRequest (as opposed to the flags from filesystem client `open(2)` flags argument).
type OpenRequestFlags uint32

const (
	OpenKillSUIDGID OpenRequestFlags = 1 << 0
)

func (fl OpenRequestFlags) String() string {
	return flagString(uint32(fl), openRequestFlagNames)
}

var openRequestFlagNames = []flagName{
	{uint32(OpenKillSUIDGID), "OpenKillSUIDGID"},
}

// The OpenResponseFlags are returned in the OpenResponse.
type OpenResponseFlags uint32

const (
	OpenDirectIO    OpenResponseFlags = 1 << 0 // bypass page cache for this open file
	OpenKeepCache   OpenResponseFlags = 1 << 1 // don't invalidate the data cache on open
	OpenNonSeekable OpenResponseFlags = 1 << 2 // mark the file as non-seekable (not supported on FreeBSD)
	OpenCacheDir    OpenResponseFlags = 1 << 3 // allow caching directory contents
)

func (fl OpenResponseFlags) String() string {
	return flagString(uint32(fl), openResponseFlagNames)
}

var openResponseFlagNames = []flagName{
	{uint32(OpenDirectIO), "OpenDirectIO"},
	{uint32(OpenKeepCache), "OpenKeepCache"},
	{uint32(OpenNonSeekable), "OpenNonSeekable"},
	{uint32(OpenCacheDir), "OpenCacheDir"},
}

// The InitFlags are used in the Init exchange.
type InitFlags uint32

const (
	InitAsyncRead     InitFlags = 1 << 0
	InitPOSIXLocks    InitFlags = 1 << 1
	InitFileOps       InitFlags = 1 << 2
	InitAtomicTrunc   InitFlags = 1 << 3
	InitExportSupport InitFlags = 1 << 4
	InitBigWrites     InitFlags = 1 << 5
	// Do not mask file access modes with umask.
	InitDontMask        InitFlags = 1 << 6
	InitSpliceWrite     InitFlags = 1 << 7
	InitSpliceMove      InitFlags = 1 << 8
	InitSpliceRead      InitFlags = 1 << 9
	InitFlockLocks      InitFlags = 1 << 10
	InitHasIoctlDir     InitFlags = 1 << 11
	InitAutoInvalData   InitFlags = 1 << 12
	InitDoReaddirplus   InitFlags = 1 << 13
	InitReaddirplusAuto InitFlags = 1 << 14
	InitAsyncDIO        InitFlags = 1 << 15
	InitWritebackCache  InitFlags = 1 << 16
	InitNoOpenSupport   InitFlags = 1 << 17
	InitParallelDirOps  InitFlags = 1 << 18
	// Deprecated: Use `InitHandleKillPriv2`.
	InitHandleKillPriv InitFlags = 1 << 19
	InitPosixACL       InitFlags = 1 << 20
	InitAbortError     InitFlags = 1 << 21
	InitMaxPages       InitFlags = 1 << 22
	InitCacheSymlinks  InitFlags = 1 << 23
	// Kernel supports zero-message OpenDir.
	InitNoOpenDirSupport InitFlags = 1 << 24
	// Only invalidate cached pages on explicit request, instead of e.g. at every file size change.
	InitExplicitInvalidateData InitFlags = 1 << 25
	InitMapAlignment           InitFlags = 1 << 26
	InitSubMounts              InitFlags = 1 << 27
	// Filesystem promises to remove SUID/SGID/cap on writes and `chown`.
	InitHandleKillPrivV2 InitFlags = 1 << 28
	InitSetxattrExt      InitFlags = 1 << 29
)

type flagName struct {
	bit  uint32
	name string
}

var initFlagNames = []flagName{
	{uint32(InitAsyncRead), "InitAsyncRead"},
	{uint32(InitPOSIXLocks), "InitPOSIXLocks"},
	{uint32(InitFileOps), "InitFileOps"},
	{uint32(InitAtomicTrunc), "InitAtomicTrunc"},
	{uint32(InitExportSupport), "InitExportSupport"},
	{uint32(InitBigWrites), "InitBigWrites"},
	{uint32(InitDontMask), "InitDontMask"},
	{uint32(InitSpliceWrite), "InitSpliceWrite"},
	{uint32(InitSpliceMove), "InitSpliceMove"},
	{uint32(InitSpliceRead), "InitSpliceRead"},
	{uint32(InitFlockLocks), "InitFlockLocks"},
	{uint32(InitHasIoctlDir), "InitHasIoctlDir"},
	{uint32(InitAutoInvalData), "InitAutoInvalData"},
	{uint32(InitDoReaddirplus), "InitDoReaddirplus"},
	{uint32(InitReaddirplusAuto), "InitReaddirplusAuto"},
	{uint32(InitAsyncDIO), "InitAsyncDIO"},
	{uint32(InitWritebackCache), "InitWritebackCache"},
	{uint32(InitNoOpenSupport), "InitNoOpenSupport"},
	{uint32(InitParallelDirOps), "InitParallelDirOps"},
	{uint32(InitHandleKillPriv), "InitHandleKillPriv"},
	{uint32(InitPosixACL), "InitPosixACL"},
	{uint32(InitAbortError), "InitAbortError"},
	{uint32(InitMaxPages), "InitMaxPages"},
	{uint32(InitCacheSymlinks), "InitCacheSymlinks"},
	{uint32(InitNoOpenDirSupport), "InitNoOpenDirSupport"},
	{uint32(InitExplicitInvalidateData), "InitExplicitInvalidateData"},
	{uint32(InitMapAlignment), "InitMapAlignment"},
	{uint32(InitSubMounts), "InitSubMounts"},
	{uint32(InitHandleKillPrivV2), "InitHandleKillPrivV2"},
	{uint32(InitSetxattrExt), "InitSetxattrExt"},
}

func (fl InitFlags) String() string {
	return flagString(uint32(fl), initFlagNames)
}

func flagString(f uint32, names []flagName) string {
	var s string

	if f == 0 {
		return "0"
	}

	for _, n := range names {
		if f&n.bit != 0 {
			s += "+" + n.name
			f &^= n.bit
		}
	}
	if f != 0 {
		s += fmt.Sprintf("%+#x", f)
	}
	return s[1:]
}

// The ReleaseFlags are used in the Release exchange.
type ReleaseFlags uint32

const (
	ReleaseFlush       ReleaseFlags = 1 << 0
	ReleaseFlockUnlock ReleaseFlags = 1 << 1
)

func (fl ReleaseFlags) String() string {
	return flagString(uint32(fl), releaseFlagNames)
}

var releaseFlagNames = []flagName{
	{uint32(ReleaseFlush), "ReleaseFlush"},
	{uint32(ReleaseFlockUnlock), "ReleaseFlockUnlock"},
}

// Opcodes
const (
	opLookup        = 1
	opForget        = 2 // no reply
	opGetattr       = 3
	opSetattr       = 4
	opReadlink      = 5
	opSymlink       = 6
	opMknod         = 8
	opMkdir         = 9
	opUnlink        = 10
	opRmdir         = 11
	opRename        = 12
	opLink          = 13
	opOpen          = 14
	opRead          = 15
	opWrite         = 16
	opStatfs        = 17
	opRelease       = 18
	opFsync         = 20
	opSetxattr      = 21
	opGetxattr      = 22
	opListxattr     = 23
	opRemovexattr   = 24
	opFlush         = 25
	opInit          = 26
	opOpendir       = 27
	opReaddir       = 28
	opReleasedir    = 29
	opFsyncdir      = 30
	opGetlk         = 31
	opSetlk         = 32
	opSetlkw        = 33
	opAccess        = 34
	opCreate        = 35
	opInterrupt     = 36
	opBmap          = 37
	opDestroy       = 38
	opIoctl         = 39
	opPoll          = 40
	opNotifyReply   = 41
	opBatchForget   = 42
	opFAllocate     = 43
	opReadDirPlus   = 44
	opRename2       = 45
	opLSeek         = 46
	opCopyFileRange = 47
	opSetupMapping  = 48
	opRemoveMapping = 49
)

type entryOut struct {
	Nodeid         uint64 // Inode ID
	Generation     uint64 // Inode generation
	EntryValid     uint64 // Cache timeout for the name
	AttrValid      uint64 // Cache timeout for the attributes
	EntryValidNsec uint32
	AttrValidNsec  uint32
	Attr           attr
}

func entryOutSize(p Protocol) uintptr {
	switch {
	case p.LT(Protocol{7, 9}):
		return unsafe.Offsetof(entryOut{}.Attr) + unsafe.Offsetof(entryOut{}.Attr.Blksize)
	default:
		return unsafe.Sizeof(entryOut{})
	}
}

type forgetIn struct {
	Nlookup uint64
}

type forgetOne struct {
	NodeID  uint64
	Nlookup uint64
}

type batchForgetIn struct {
	Count uint32
	_     uint32
}

type getattrIn struct {
	GetattrFlags uint32
	_            uint32
	Fh           uint64
}

type attrOut struct {
	AttrValid     uint64 // Cache timeout for the attributes
	AttrValidNsec uint32
	_             uint32
	Attr          attr
}

func attrOutSize(p Protocol) uintptr {
	switch {
	case p.LT(Protocol{7, 9}):
		return unsafe.Offsetof(attrOut{}.Attr) + unsafe.Offsetof(attrOut{}.Attr.Blksize)
	default:
		return unsafe.Sizeof(attrOut{})
	}
}

type mknodIn struct {
	Mode  uint32
	Rdev  uint32
	Umask uint32
	_     uint32
	// "filename\x00" follows.
}

func mknodInSize(p Protocol) uintptr {
	switch {
	case p.LT(Protocol{7, 12}):
		return unsafe.Offsetof(mknodIn{}.Umask)
	default:
		return unsafe.Sizeof(mknodIn{})
	}
}

type mkdirIn struct {
	Mode  uint32
	Umask uint32
	// filename follows
}

func mkdirInSize(p Protocol) uintptr {
	switch {
	case p.LT(Protocol{7, 12}):
		return unsafe.Offsetof(mkdirIn{}.Umask) + 4
	default:
		return unsafe.Sizeof(mkdirIn{})
	}
}

type renameIn struct {
	Newdir uint64
	// "oldname\x00newname\x00" follows
}

type linkIn struct {
	Oldnodeid uint64
}

type setattrIn struct {
	Valid     uint32
	_         uint32
	Fh        uint64
	Size      uint64
	LockOwner uint64
	Atime     uint64
	Mtime     uint64
	Ctime     uint64
	AtimeNsec uint32
	MtimeNsec uint32
	CtimeNsec uint32
	Mode      uint32
	Unused4   uint32
	Uid       uint32
	Gid       uint32
	Unused5   uint32
}

type openIn struct {
	Flags     uint32
	OpenFlags uint32
}

type openOut struct {
	Fh        uint64
	OpenFlags uint32
	_         uint32
}

type createIn struct {
	Flags uint32
	Mode  uint32
	Umask uint32
	_     uint32
}

func createInSize(p Protocol) uintptr {
	switch {
	case p.LT(Protocol{7, 12}):
		return unsafe.Offsetof(createIn{}.Umask)
	default:
		return unsafe.Sizeof(createIn{})
	}
}

type releaseIn struct {
	Fh           uint64
	Flags        uint32
	ReleaseFlags uint32
	LockOwner    uint64
}

type flushIn struct {
	Fh        uint64
	_         uint32
	_         uint32
	LockOwner uint64
}

type readIn struct {
	Fh        uint64
	Offset    uint64
	Size      uint32
	ReadFlags uint32
	LockOwner uint64
	Flags     uint32
	_         uint32
}

func readInSize(p Protocol) uintptr {
	switch {
	case p.LT(Protocol{7, 9}):
		return unsafe.Offsetof(readIn{}.ReadFlags) + 4
	default:
		return unsafe.Sizeof(readIn{})
	}
}

// The ReadFlags are passed in ReadRequest.
type ReadFlags uint32

const (
	// LockOwner field is valid.
	ReadLockOwner ReadFlags = 1 << 1
)

var readFlagNames = []flagName{
	{uint32(ReadLockOwner), "ReadLockOwner"},
}

func (fl ReadFlags) String() string {
	return flagString(uint32(fl), readFlagNames)
}

type writeIn struct {
	Fh         uint64
	Offset     uint64
	Size       uint32
	WriteFlags uint32
	LockOwner  uint64
	Flags      uint32
	_          uint32
}

func writeInSize(p Protocol) uintptr {
	switch {
	case p.LT(Protocol{7, 9}):
		return unsafe.Offsetof(writeIn{}.LockOwner)
	default:
		return unsafe.Sizeof(writeIn{})
	}
}

type writeOut struct {
	Size uint32
	_    uint32
}

// The WriteFlags are passed in WriteRequest.
type WriteFlags uint32

const (
	WriteCache WriteFlags = 1 << 0
	// LockOwner field is valid.
	WriteLockOwner WriteFlags = 1 << 1
	// Remove SUID and GID bits.
	WriteKillSUIDGID WriteFlags = 1 << 2
)

var writeFlagNames = []flagName{
	{uint32(WriteCache), "WriteCache"},
	{uint32(WriteLockOwner), "WriteLockOwner"},
	{uint32(WriteKillSUIDGID), "WriteKillSUIDGID"},
}

func (fl WriteFlags) String() string {
	return flagString(uint32(fl), writeFlagNames)
}

type statfsOut struct {
	St kstatfs
}

type fsyncIn struct {
	Fh         uint64
	FsyncFlags uint32
	_          uint32
}

// SetxattrFlags re passed in SetxattrRequest.SetxattrFlags.
type SetxattrFlags uint32

const (
	SetxattrACLKillSGID SetxattrFlags = 1 << 0
)

var setxattrFlagNames = []flagName{
	{uint32(SetxattrACLKillSGID), "SetxattrACLKillSGID"},
}

func (fl SetxattrFlags) String() string {
	return flagString(uint32(fl), setxattrFlagNames)
}

type setxattrIn struct {
	Size          uint32
	Flags         uint32
	SetxattrFlags SetxattrFlags
	_             uint32
}

func setxattrInSize(fl InitFlags) uintptr {
	if fl&InitSetxattrExt == 0 {
		return unsafe.Offsetof(setxattrIn{}.SetxattrFlags)
	}
	return unsafe.Sizeof(setxattrIn{})
}

type getxattrIn struct {
	Size uint32
	_    uint32
}

type getxattrOut struct {
	Size uint32
	_    uint32
}

// The LockFlags are passed in LockRequest or LockWaitRequest.
type LockFlags uint32

const (
	// BSD-style flock lock (not POSIX lock)
	LockFlock LockFlags = 1 << 0
)

var lockFlagNames = []flagName{
	{uint32(LockFlock), "LockFlock"},
}

func (fl LockFlags) String() string {
	return flagString(uint32(fl), lockFlagNames)
}

type LockType uint32

const (
	// It seems FreeBSD FUSE passes these through using its local
	// values, not whatever Linux enshrined into the protocol. It's
	// unclear what the intended behavior is.

	LockRead   LockType = unix.F_RDLCK
	LockWrite  LockType = unix.F_WRLCK
	LockUnlock LockType = unix.F_UNLCK
)

var lockTypeNames = map[LockType]string{
	LockRead:   "LockRead",
	LockWrite:  "LockWrite",
	LockUnlock: "LockUnlock",
}

func (l LockType) String() string {
	s, ok := lockTypeNames[l]
	if ok {
		return s
	}
	return fmt.Sprintf("LockType(%d)", l)
}

type fileLock struct {
	Start uint64
	End   uint64
	Type  uint32
	PID   uint32
}

type lkIn struct {
	Fh      uint64
	Owner   uint64
	Lk      fileLock
	LkFlags uint32
	_       uint32
}

type lkOut struct {
	Lk fileLock
}

type accessIn struct {
	Mask uint32
	_    uint32
}

type initIn struct {
	Major        uint32
	Minor        uint32
	MaxReadahead uint32
	Flags        uint32
}

const initInSize = int(unsafe.Sizeof(initIn{}))

type initOut struct {
	Major               uint32
	Minor               uint32
	MaxReadahead        uint32
	Flags               uint32
	MaxBackground       uint16
	CongestionThreshold uint16
	MaxWrite            uint32

	// end of protocol 7.22 fields

	// Granularity of timestamps, in nanoseconds.
	// Maximum value 1e9 (one second).
	TimeGran uint32
	// Maximum number of pages of data in one read or write request.
	// Set initOut.Flags.InitMaxPages when valid.
	MaxPages     uint16
	MapAlignment uint16
	_            [8]uint32
}

type interruptIn struct {
	Unique uint64
}

type bmapIn struct {
	Block     uint64
	BlockSize uint32
	_         uint32
}

type bmapOut struct {
	Block uint64
}

type inHeader struct {
	Len    uint32
	Opcode uint32
	Unique uint64
	Nodeid uint64
	Uid    uint32
	Gid    uint32
	Pid    uint32
	_      uint32
}

const inHeaderSize = int(unsafe.Sizeof(inHeader{}))

type outHeader struct {
	Len    uint32
	Error  int32
	Unique uint64
}

type dirent struct {
	Ino     uint64
	Off     uint64
	Namelen uint32
	Type    uint32
}

const direntSize = 8 + 8 + 4 + 4

const (
	notifyCodePoll       int32 = 1
	notifyCodeInvalInode int32 = 2
	notifyCodeInvalEntry int32 = 3
	notifyCodeStore      int32 = 4
	notifyCodeRetrieve   int32 = 5
	notifyCodeDelete     int32 = 6
)

type notifyInvalInodeOut struct {
	Ino uint64
	Off int64
	Len int64
}

type notifyInvalEntryOut struct {
	Parent  uint64
	Namelen uint32
	_       uint32
}

type notifyDeleteOut struct {
	Parent  uint64
	Child   uint64
	Namelen uint32
	_       uint32
}

type notifyStoreOut struct {
	Nodeid uint64
	Offset uint64
	Size   uint32
	_      uint32
}

type notifyRetrieveOut struct {
	NotifyUnique uint64
	Nodeid       uint64
	Offset       uint64
	Size         uint32
	_            uint32
}

type notifyRetrieveIn struct {
	// matches writeIn

	_      uint64
	Offset uint64
	Size   uint32
	_      uint32
	_      uint64
	_      uint64
}

// PollFlags are passed in PollRequest.Flags
type PollFlags uint32

const (
	// PollScheduleNotify requests that a poll notification is done
	// once the node is ready.
	PollScheduleNotify PollFlags = 1 << 0
)

var pollFlagNames = []flagName{
	{uint32(PollScheduleNotify), "PollScheduleNotify"},
}

func (fl PollFlags) String() string {
	return flagString(uint32(fl), pollFlagNames)
}

type PollEvents uint32

const (
	PollIn       PollEvents = 0x0000_0001
	PollPriority PollEvents = 0x0000_0002
	PollOut      PollEvents = 0x0000_0004
	PollError    PollEvents = 0x0000_0008
	PollHangup   PollEvents = 0x0000_0010
	// PollInvalid doesn't seem to be used in the FUSE protocol.
	PollInvalid        PollEvents = 0x0000_0020
	PollReadNormal     PollEvents = 0x0000_0040
	PollReadOutOfBand  PollEvents = 0x0000_0080
	PollWriteNormal    PollEvents = 0x0000_0100
	PollWriteOutOfBand PollEvents = 0x0000_0200
	PollMessage        PollEvents = 0x0000_0400
	PollReadHangup     PollEvents = 0x0000_2000

	DefaultPollMask = PollIn | PollOut | PollReadNormal | PollWriteNormal
)

var pollEventNames = []flagName{
	{uint32(PollIn), "PollIn"},
	{uint32(PollPriority), "PollPriority"},
	{uint32(PollOut), "PollOut"},
	{uint32(PollError), "PollError"},
	{uint32(PollHangup), "PollHangup"},
	{uint32(PollInvalid), "PollInvalid"},
	{uint32(PollReadNormal), "PollReadNormal"},
	{uint32(PollReadOutOfBand), "PollReadOutOfBand"},
	{uint32(PollWriteNormal), "PollWriteNormal"},
	{uint32(PollWriteOutOfBand), "PollWriteOutOfBand"},
	{uint32(PollMessage), "PollMessage"},
	{uint32(PollReadHangup), "PollReadHangup"},
}

func (fl PollEvents) String() string {
	return flagString(uint32(fl), pollEventNames)
}

type pollIn struct {
	Fh     uint64
	Kh     uint64
	Flags  uint32
	Events uint32
}

type pollOut struct {
	REvents uint32
	_       uint32
}

type notifyPollWakeupOut struct {
	Kh uint64
}

type FAllocateFlags uint32

const (
	FAllocateKeepSize  FAllocateFlags = unix.FALLOC_FL_KEEP_SIZE
	FAllocatePunchHole FAllocateFlags = unix.FALLOC_FL_PUNCH_HOLE

	// Only including constants supported by FUSE kernel implementation.
	//
	// FAllocateCollapseRange FAllocateFlags = unix.FALLOC_FL_COLLAPSE_RANGE
	// FAllocateInsertRange   FAllocateFlags = unix.FALLOC_FL_INSERT_RANGE
	// FAllocateNoHideStale   FAllocateFlags = unix.FALLOC_FL_NO_HIDE_STALE
	// FAllocateUnshareRange  FAllocateFlags = unix.FALLOC_FL_UNSHARE_RANGE
	// FAllocateZeroRange     FAllocateFlags = unix.FALLOC_FL_ZERO_RANGE
)

var fAllocateFlagsNames = []flagName{
	{uint32(FAllocatePunchHole), "FAllocatePunchHole"},
	{uint32(FAllocateKeepSize), "FAllocateKeepSize"},

	// Only including constants supported by FUSE kernel implementation.
	//
	// {uint32(FAllocateCollapseRange), "FAllocateCollapseRange"},
	// {uint32(FAllocateInsertRange), "FAllocateInsertRange"},
	// {uint32(FAllocateNoHideStale), "FAllocateNoHideStale"},
	// {uint32(FAllocateUnshareRange), "FAllocateUnshareRange"},
	// {uint32(FAllocateZeroRange), "FAllocateZeroRange"},
}

func (fl FAllocateFlags) String() string {
	return flagString(uint32(fl), fAllocateFlagsNames)
}

type fAllocateIn struct {
	Fh     uint64
	Offset uint64
	Length uint64
	Mode   uint32
	_      uint32
}
