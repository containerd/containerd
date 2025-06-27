// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

import (
	"bytes"
	"fmt"
	"log"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	_OP_LOOKUP          = uint32(1)
	_OP_FORGET          = uint32(2)
	_OP_GETATTR         = uint32(3)
	_OP_SETATTR         = uint32(4)
	_OP_READLINK        = uint32(5)
	_OP_SYMLINK         = uint32(6)
	_OP_MKNOD           = uint32(8)
	_OP_MKDIR           = uint32(9)
	_OP_UNLINK          = uint32(10)
	_OP_RMDIR           = uint32(11)
	_OP_RENAME          = uint32(12)
	_OP_LINK            = uint32(13)
	_OP_OPEN            = uint32(14)
	_OP_READ            = uint32(15)
	_OP_WRITE           = uint32(16)
	_OP_STATFS          = uint32(17)
	_OP_RELEASE         = uint32(18)
	_OP_FSYNC           = uint32(20)
	_OP_SETXATTR        = uint32(21)
	_OP_GETXATTR        = uint32(22)
	_OP_LISTXATTR       = uint32(23)
	_OP_REMOVEXATTR     = uint32(24)
	_OP_FLUSH           = uint32(25)
	_OP_INIT            = uint32(26)
	_OP_OPENDIR         = uint32(27)
	_OP_READDIR         = uint32(28)
	_OP_RELEASEDIR      = uint32(29)
	_OP_FSYNCDIR        = uint32(30)
	_OP_GETLK           = uint32(31)
	_OP_SETLK           = uint32(32)
	_OP_SETLKW          = uint32(33)
	_OP_ACCESS          = uint32(34)
	_OP_CREATE          = uint32(35)
	_OP_INTERRUPT       = uint32(36)
	_OP_BMAP            = uint32(37)
	_OP_DESTROY         = uint32(38)
	_OP_IOCTL           = uint32(39)
	_OP_POLL            = uint32(40)
	_OP_NOTIFY_REPLY    = uint32(41)
	_OP_BATCH_FORGET    = uint32(42)
	_OP_FALLOCATE       = uint32(43) // protocol version 19.
	_OP_READDIRPLUS     = uint32(44) // protocol version 21.
	_OP_RENAME2         = uint32(45) // protocol version 23.
	_OP_LSEEK           = uint32(46) // protocol version 24
	_OP_COPY_FILE_RANGE = uint32(47) // protocol version 28.

	_OP_SETUPMAPPING  = 48
	_OP_REMOVEMAPPING = 49
	_OP_SYNCFS        = 50
	_OP_TMPFILE       = 51
	_OP_STATX         = 52

	// The following entries don't have to be compatible across Go-FUSE versions.
	_OP_NOTIFY_INVAL_ENTRY    = uint32(100)
	_OP_NOTIFY_INVAL_INODE    = uint32(101)
	_OP_NOTIFY_STORE_CACHE    = uint32(102)
	_OP_NOTIFY_RETRIEVE_CACHE = uint32(103)
	_OP_NOTIFY_DELETE         = uint32(104) // protocol version 18

	_OPCODE_COUNT = uint32(105)

	// Constants from Linux kernel fs/fuse/fuse_i.h
	// Default MaxPages value in all kernel versions
	_FUSE_DEFAULT_MAX_PAGES_PER_REQ = 32
	// Upper MaxPages limit in Linux v4.20+ (v4.19 and older: 32)
	_FUSE_MAX_MAX_PAGES = 256
)

////////////////////////////////////////////////////////////////

func doInit(server *protocolServer, req *request) {
	input := (*InitIn)(req.inData())
	if input.Major != _FUSE_KERNEL_VERSION {
		log.Printf("Major versions does not match. Given %d, want %d\n", input.Major, _FUSE_KERNEL_VERSION)
		req.status = EIO
		return
	}
	if input.Minor < _MINIMUM_MINOR_VERSION {
		log.Printf("Minor version is less than we support. Given %d, want at least %d\n", input.Minor, _MINIMUM_MINOR_VERSION)
		req.status = EIO
		return
	}

	kernelFlags := input.Flags64()
	server.kernelSettings = *input
	kernelFlags &= (CAP_ASYNC_READ | CAP_BIG_WRITES | CAP_FILE_OPS |
		CAP_READDIRPLUS | CAP_NO_OPEN_SUPPORT | CAP_PARALLEL_DIROPS | CAP_MAX_PAGES | CAP_RENAME_SWAP | CAP_PASSTHROUGH | CAP_ALLOW_IDMAP)

	if server.opts.EnableLocks {
		kernelFlags |= CAP_FLOCK_LOCKS | CAP_POSIX_LOCKS
	}
	if server.opts.EnableSymlinkCaching {
		kernelFlags |= CAP_CACHE_SYMLINKS
	}
	if server.opts.EnableAcl {
		kernelFlags |= CAP_POSIX_ACL
	}
	if server.opts.SyncRead {
		// Clear CAP_ASYNC_READ
		kernelFlags &= ^uint64(CAP_ASYNC_READ)
	}
	if server.opts.DisableReadDirPlus {
		// Clear CAP_READDIRPLUS
		kernelFlags &= ^uint64(CAP_READDIRPLUS)
	}
	if !server.opts.IDMappedMount {
		// Clear CAP_ALLOW_IDMAP
		kernelFlags &= ^uint64(CAP_ALLOW_IDMAP)
	}

	if server.opts.ExplicitDataCacheControl {
		// we don't want CAP_AUTO_INVAL_DATA even if we cannot go into fully explicit mode
		kernelFlags |= input.Flags64() & CAP_EXPLICIT_INVAL_DATA
	} else {
		kernelFlags |= input.Flags64() & CAP_AUTO_INVAL_DATA
	}

	// maxPages is the maximum request size we want the kernel to use, in units of
	// memory pages (usually 4kiB). Linux v4.19 and older ignore this and always use
	// 128kiB.
	maxPages := (server.opts.MaxWrite-1)/syscall.Getpagesize() + 1 // Round up

	out := (*InitOut)(req.outData())
	*out = InitOut{
		Major:               _FUSE_KERNEL_VERSION,
		Minor:               _OUR_MINOR_VERSION,
		MaxReadAhead:        input.MaxReadAhead,
		MaxWrite:            uint32(server.opts.MaxWrite),
		CongestionThreshold: uint16(server.opts.MaxBackground * 3 / 4),
		MaxBackground:       uint16(server.opts.MaxBackground),
		MaxPages:            uint16(maxPages),
		MaxStackDepth:       uint32(server.opts.MaxStackDepth),
	}
	out.setFlags(kernelFlags)
	if server.opts.MaxReadAhead != 0 && uint32(server.opts.MaxReadAhead) < out.MaxReadAhead {
		out.MaxReadAhead = uint32(server.opts.MaxReadAhead)
	}
	if out.Minor > input.Minor {
		out.Minor = input.Minor
	}

	req.status = OK
}

func doOpen(server *protocolServer, req *request) {
	out := (*OpenOut)(req.outData())
	status := server.fileSystem.Open(req.cancel, (*OpenIn)(req.inData()), out)
	req.status = status
	if status != OK {
		return
	}
}

func doCreate(server *protocolServer, req *request) {
	out := (*CreateOut)(req.outData())
	status := server.fileSystem.Create(req.cancel, (*CreateIn)(req.inData()), req.filename(), out)
	req.status = status
}

func doReadDir(server *protocolServer, req *request) {
	in := (*ReadIn)(req.inData())
	out := NewDirEntryList(req.outPayload, uint64(in.Offset))
	code := server.fileSystem.ReadDir(req.cancel, in, out)
	req.outPayload = out.bytes()
	req.status = code
}

func doReadDirPlus(server *protocolServer, req *request) {
	in := (*ReadIn)(req.inData())
	out := NewDirEntryList(req.outPayload, uint64(in.Offset))

	code := server.fileSystem.ReadDirPlus(req.cancel, in, out)
	req.outPayload = out.bytes()
	req.status = code
}

func doOpenDir(server *protocolServer, req *request) {
	out := (*OpenOut)(req.outData())
	status := server.fileSystem.OpenDir(req.cancel, (*OpenIn)(req.inData()), out)
	req.status = status
}

func doSetattr(server *protocolServer, req *request) {
	out := (*AttrOut)(req.outData())
	req.status = server.fileSystem.SetAttr(req.cancel, (*SetAttrIn)(req.inData()), out)
}

func doWrite(server *protocolServer, req *request) {
	n, status := server.fileSystem.Write(req.cancel, (*WriteIn)(req.inData()), req.inPayload)
	o := (*WriteOut)(req.outData())
	o.Size = n
	req.status = status
}

func doNotifyReply(server *protocolServer, req *request) {
	reply := (*NotifyRetrieveIn)(req.inData())
	server.retrieveMu.Lock()
	reading := server.retrieveTab[reply.Unique]
	delete(server.retrieveTab, reply.Unique)
	server.retrieveMu.Unlock()

	badf := func(format string, argv ...interface{}) {
		server.opts.Logger.Printf("notify reply: "+format, argv...)
	}

	if reading == nil {
		badf("unexpected unique - ignoring")
		return
	}

	reading.n = 0
	reading.st = EIO
	defer close(reading.ready)

	if reading.nodeid != reply.NodeId {
		badf("inode mismatch: expected %s, got %s", reading.nodeid, reply.NodeId)
		return
	}

	if reading.offset != reply.Offset {
		badf("offset mismatch: expected @%d, got @%d", reading.offset, reply.Offset)
		return
	}

	if len(reading.dest) < len(req.inPayload) {
		badf("too much data: requested %db, got %db (will use only %db)", len(reading.dest), len(req.inPayload), len(reading.dest))
	}

	reading.n = copy(reading.dest, req.inPayload)
	reading.st = OK
}

const _SECURITY_CAPABILITY = "security.capability"
const _SECURITY_ACL = "system.posix_acl_access"
const _SECURITY_ACL_DEFAULT = "system.posix_acl_default"

func doGetXAttr(server *protocolServer, req *request) {
	if server.opts.DisableXAttrs {
		req.status = ENOSYS
		return
	}

	if server.opts.IgnoreSecurityLabels && req.inHeader().Opcode == _OP_GETXATTR {
		fn := req.filename()
		if fn == _SECURITY_CAPABILITY || fn == _SECURITY_ACL_DEFAULT ||
			fn == _SECURITY_ACL {
			req.status = ENOATTR
			return
		}
	}

	input := (*GetXAttrIn)(req.inData())
	out := (*GetXAttrOut)(req.outData())

	var n uint32
	switch req.inHeader().Opcode {
	case _OP_GETXATTR:
		n, req.status = server.fileSystem.GetXAttr(req.cancel, req.inHeader(), req.filename(), req.outPayload)
	case _OP_LISTXATTR:
		n, req.status = server.fileSystem.ListXAttr(req.cancel, req.inHeader(), req.outPayload)
	default:
		req.status = ENOSYS
	}

	if input.Size == 0 && req.status == ERANGE {
		// For input.size==0, returning ERANGE is an error.
		req.status = OK
		out.Size = n
	} else if req.status.Ok() {
		// ListXAttr called with an empty buffer returns the current size of
		// the list but does not touch the buffer (see man 2 listxattr).
		if len(req.outPayload) > 0 {
			req.outPayload = req.outPayload[:n]
		}
		out.Size = n
	} else {
		req.outPayload = req.outPayload[:0]
	}
}

func doGetAttr(server *protocolServer, req *request) {
	out := (*AttrOut)(req.outData())
	s := server.fileSystem.GetAttr(req.cancel, (*GetAttrIn)(req.inData()), out)
	req.status = s
}

// doForget - forget one NodeId
func doForget(server *protocolServer, req *request) {
	if !server.opts.RememberInodes {
		server.fileSystem.Forget(req.inHeader().NodeId, (*ForgetIn)(req.inData()).Nlookup)
	}
}

// doBatchForget - forget a list of NodeIds
func doBatchForget(server *protocolServer, req *request) {
	in := (*_BatchForgetIn)(req.inData())
	wantBytes := uintptr(in.Count) * unsafe.Sizeof(_ForgetOne{})
	if uintptr(len(req.inPayload)) < wantBytes {
		// We have no return value to complain, so log an error.
		server.opts.Logger.Printf("Too few bytes for batch forget. Got %d bytes, want %d (%d entries)",
			len(req.inPayload), wantBytes, in.Count)
	}

	forgets := unsafe.Slice((*_ForgetOne)(unsafe.Pointer(&req.inPayload[0])), in.Count)
	for i, f := range forgets {
		if server.opts.Debug {
			server.opts.Logger.Printf("doBatchForget: rx %d %d/%d: FORGET n%d {Nlookup=%d}",
				req.inHeader().Unique, i+1, len(forgets), f.NodeId, f.Nlookup)
		}
		if f.NodeId == pollHackInode {
			continue
		}
		server.fileSystem.Forget(f.NodeId, f.Nlookup)
	}
}

func doReadlink(server *protocolServer, req *request) {
	req.outPayload, req.status = server.fileSystem.Readlink(req.cancel, req.inHeader())
}

func doLookup(server *protocolServer, req *request) {
	out := (*EntryOut)(req.outData())
	req.status = server.fileSystem.Lookup(req.cancel, req.inHeader(), req.filename(), out)
}

func doMknod(server *protocolServer, req *request) {
	out := (*EntryOut)(req.outData())

	req.status = server.fileSystem.Mknod(req.cancel, (*MknodIn)(req.inData()), req.filename(), out)
}

func doMkdir(server *protocolServer, req *request) {
	out := (*EntryOut)(req.outData())
	req.status = server.fileSystem.Mkdir(req.cancel, (*MkdirIn)(req.inData()), req.filename(), out)
}

func doUnlink(server *protocolServer, req *request) {
	req.status = server.fileSystem.Unlink(req.cancel, req.inHeader(), req.filename())
}

func doRmdir(server *protocolServer, req *request) {
	req.status = server.fileSystem.Rmdir(req.cancel, req.inHeader(), req.filename())
}

func doLink(server *protocolServer, req *request) {
	out := (*EntryOut)(req.outData())
	req.status = server.fileSystem.Link(req.cancel, (*LinkIn)(req.inData()), req.filename(), out)
}

func doRead(server *protocolServer, req *request) {
	in := (*ReadIn)(req.inData())
	req.readResult, req.status = server.fileSystem.Read(req.cancel, in, req.outPayload)
	if fd, ok := req.readResult.(*readResultFd); ok {
		req.fdData = fd
	} else if req.readResult != nil && req.status.Ok() {
		req.outPayload, req.status = req.readResult.Bytes(req.outPayload)
	}
}

func doFlush(server *protocolServer, req *request) {
	req.status = server.fileSystem.Flush(req.cancel, (*FlushIn)(req.inData()))
}

func doRelease(server *protocolServer, req *request) {
	server.fileSystem.Release(req.cancel, (*ReleaseIn)(req.inData()))
}

func doFsync(server *protocolServer, req *request) {
	req.status = server.fileSystem.Fsync(req.cancel, (*FsyncIn)(req.inData()))
}

func doReleaseDir(server *protocolServer, req *request) {
	server.fileSystem.ReleaseDir((*ReleaseIn)(req.inData()))
}

func doFsyncDir(server *protocolServer, req *request) {
	req.status = server.fileSystem.FsyncDir(req.cancel, (*FsyncIn)(req.inData()))
}

func doSetXAttr(server *protocolServer, req *request) {
	i := bytes.IndexByte(req.inPayload, 0)
	req.status = server.fileSystem.SetXAttr(req.cancel, (*SetXAttrIn)(req.inData()), string(req.inPayload[:i]), req.inPayload[i+1:])
}

func doRemoveXAttr(server *protocolServer, req *request) {
	req.status = server.fileSystem.RemoveXAttr(req.cancel, req.inHeader(), req.filename())
}

func doAccess(server *protocolServer, req *request) {
	req.status = server.fileSystem.Access(req.cancel, (*AccessIn)(req.inData()))
}

func doSymlink(server *protocolServer, req *request) {
	out := (*EntryOut)(req.outData())
	n1, n2 := req.filenames()

	req.status = server.fileSystem.Symlink(req.cancel, req.inHeader(), n2, n1, out)
}

func doRename(server *protocolServer, req *request) {
	if server.kernelSettings.supportsRenameSwap() {
		doRename2(server, req)
		return
	}
	in1 := (*Rename1In)(req.inData())
	in := RenameIn{
		InHeader: in1.InHeader,
		Newdir:   in1.Newdir,
	}
	n1, n2 := req.filenames()
	req.status = server.fileSystem.Rename(req.cancel, &in, n1, n2)
}

func doRename2(server *protocolServer, req *request) {
	n1, n2 := req.filenames()
	req.status = server.fileSystem.Rename(req.cancel, (*RenameIn)(req.inData()), n1, n2)
}

func doStatFs(server *protocolServer, req *request) {
	out := (*StatfsOut)(req.outData())
	req.status = server.fileSystem.StatFs(req.cancel, req.inHeader(), out)
	if req.status == ENOSYS && runtime.GOOS == "darwin" {
		// OSX FUSE requires Statfs to be implemented for the
		// mount to succeed.
		*out = StatfsOut{}
		req.status = OK
	}
}

func doIoctl(server *protocolServer, req *request) {
	req.status = server.fileSystem.Ioctl(req.cancel, (*IoctlIn)(req.inData()), req.inPayload, (*IoctlOut)(req.outData()),
		req.outPayload)
}

func doDestroy(server *protocolServer, req *request) {
	req.status = OK
}

func doFallocate(server *protocolServer, req *request) {
	req.status = server.fileSystem.Fallocate(req.cancel, (*FallocateIn)(req.inData()))
}

func doGetLk(server *protocolServer, req *request) {
	req.status = server.fileSystem.GetLk(req.cancel, (*LkIn)(req.inData()), (*LkOut)(req.outData()))
}

func doSetLk(server *protocolServer, req *request) {
	req.status = server.fileSystem.SetLk(req.cancel, (*LkIn)(req.inData()))
}

func doSetLkw(server *protocolServer, req *request) {
	req.status = server.fileSystem.SetLkw(req.cancel, (*LkIn)(req.inData()))
}

func doLseek(server *protocolServer, req *request) {
	in := (*LseekIn)(req.inData())
	out := (*LseekOut)(req.outData())
	req.status = server.fileSystem.Lseek(req.cancel, in, out)
}

func doCopyFileRange(server *protocolServer, req *request) {
	in := (*CopyFileRangeIn)(req.inData())
	out := (*WriteOut)(req.outData())

	out.Size, req.status = server.fileSystem.CopyFileRange(req.cancel, in)
}

func doInterrupt(server *protocolServer, req *request) {
	input := (*InterruptIn)(req.inData())
	req.status = server.interruptRequest(input.Unique)
}

////////////////////////////////////////////////////////////////

type operationFunc func(*protocolServer, *request)
type castPointerFunc func(unsafe.Pointer) interface{}

type operationHandler struct {
	Name       string
	Func       operationFunc
	InputSize  uintptr
	OutputSize uintptr

	InType      interface{}
	OutType     interface{}
	FileNames   int
	FileNameOut bool
}

var operationHandlers []*operationHandler

func operationName(op uint32) string {
	h := getHandler(op)
	if h == nil {
		return "unknown"
	}
	return h.Name
}

func getHandler(o uint32) *operationHandler {
	if o >= _OPCODE_COUNT {
		return nil
	}
	return operationHandlers[o]
}

// maximum size of all input headers
var maxInputSize uintptr

func init() {
	operationHandlers = make([]*operationHandler, _OPCODE_COUNT)
	for i := range operationHandlers {
		operationHandlers[i] = &operationHandler{Name: fmt.Sprintf("OPCODE-%d", i)}
	}

	fileOps := []uint32{_OP_READLINK, _OP_NOTIFY_INVAL_ENTRY, _OP_NOTIFY_DELETE}
	for _, op := range fileOps {
		operationHandlers[op].FileNameOut = true
	}

	for op, v := range map[uint32]string{
		_OP_LOOKUP:                "LOOKUP",
		_OP_FORGET:                "FORGET",
		_OP_BATCH_FORGET:          "BATCH_FORGET",
		_OP_GETATTR:               "GETATTR",
		_OP_SETATTR:               "SETATTR",
		_OP_READLINK:              "READLINK",
		_OP_SYMLINK:               "SYMLINK",
		_OP_MKNOD:                 "MKNOD",
		_OP_MKDIR:                 "MKDIR",
		_OP_UNLINK:                "UNLINK",
		_OP_RMDIR:                 "RMDIR",
		_OP_RENAME:                "RENAME",
		_OP_LINK:                  "LINK",
		_OP_OPEN:                  "OPEN",
		_OP_READ:                  "READ",
		_OP_WRITE:                 "WRITE",
		_OP_STATFS:                "STATFS",
		_OP_RELEASE:               "RELEASE",
		_OP_FSYNC:                 "FSYNC",
		_OP_SETXATTR:              "SETXATTR",
		_OP_GETXATTR:              "GETXATTR",
		_OP_LISTXATTR:             "LISTXATTR",
		_OP_REMOVEXATTR:           "REMOVEXATTR",
		_OP_FLUSH:                 "FLUSH",
		_OP_INIT:                  "INIT",
		_OP_OPENDIR:               "OPENDIR",
		_OP_READDIR:               "READDIR",
		_OP_RELEASEDIR:            "RELEASEDIR",
		_OP_FSYNCDIR:              "FSYNCDIR",
		_OP_GETLK:                 "GETLK",
		_OP_SETLK:                 "SETLK",
		_OP_SETLKW:                "SETLKW",
		_OP_ACCESS:                "ACCESS",
		_OP_CREATE:                "CREATE",
		_OP_INTERRUPT:             "INTERRUPT",
		_OP_BMAP:                  "BMAP",
		_OP_DESTROY:               "DESTROY",
		_OP_IOCTL:                 "IOCTL",
		_OP_POLL:                  "POLL",
		_OP_NOTIFY_REPLY:          "NOTIFY_REPLY",
		_OP_NOTIFY_INVAL_ENTRY:    "NOTIFY_INVAL_ENTRY",
		_OP_NOTIFY_INVAL_INODE:    "NOTIFY_INVAL_INODE",
		_OP_NOTIFY_STORE_CACHE:    "NOTIFY_STORE",
		_OP_NOTIFY_RETRIEVE_CACHE: "NOTIFY_RETRIEVE",
		_OP_NOTIFY_DELETE:         "NOTIFY_DELETE",
		_OP_FALLOCATE:             "FALLOCATE",
		_OP_READDIRPLUS:           "READDIRPLUS",
		_OP_RENAME2:               "RENAME2",
		_OP_LSEEK:                 "LSEEK",
		_OP_COPY_FILE_RANGE:       "COPY_FILE_RANGE",
		_OP_SETUPMAPPING:          "SETUPMAPPING",
		_OP_REMOVEMAPPING:         "REMOVEMAPPING",
		_OP_SYNCFS:                "SYNCFS",
		_OP_TMPFILE:               "TMPFILE",
	} {
		operationHandlers[op].Name = v
	}

	for op, v := range map[uint32]operationFunc{
		_OP_OPEN:            doOpen,
		_OP_READDIR:         doReadDir,
		_OP_WRITE:           doWrite,
		_OP_OPENDIR:         doOpenDir,
		_OP_CREATE:          doCreate,
		_OP_SETATTR:         doSetattr,
		_OP_GETXATTR:        doGetXAttr,
		_OP_LISTXATTR:       doGetXAttr,
		_OP_GETATTR:         doGetAttr,
		_OP_FORGET:          doForget,
		_OP_BATCH_FORGET:    doBatchForget,
		_OP_READLINK:        doReadlink,
		_OP_INIT:            doInit,
		_OP_LOOKUP:          doLookup,
		_OP_MKNOD:           doMknod,
		_OP_MKDIR:           doMkdir,
		_OP_UNLINK:          doUnlink,
		_OP_RMDIR:           doRmdir,
		_OP_LINK:            doLink,
		_OP_READ:            doRead,
		_OP_FLUSH:           doFlush,
		_OP_RELEASE:         doRelease,
		_OP_FSYNC:           doFsync,
		_OP_RELEASEDIR:      doReleaseDir,
		_OP_FSYNCDIR:        doFsyncDir,
		_OP_SETXATTR:        doSetXAttr,
		_OP_REMOVEXATTR:     doRemoveXAttr,
		_OP_GETLK:           doGetLk,
		_OP_SETLK:           doSetLk,
		_OP_SETLKW:          doSetLkw,
		_OP_ACCESS:          doAccess,
		_OP_SYMLINK:         doSymlink,
		_OP_RENAME:          doRename,
		_OP_STATFS:          doStatFs,
		_OP_IOCTL:           doIoctl,
		_OP_DESTROY:         doDestroy,
		_OP_NOTIFY_REPLY:    doNotifyReply,
		_OP_FALLOCATE:       doFallocate,
		_OP_READDIRPLUS:     doReadDirPlus,
		_OP_RENAME2:         doRename2,
		_OP_INTERRUPT:       doInterrupt,
		_OP_COPY_FILE_RANGE: doCopyFileRange,
		_OP_LSEEK:           doLseek,
	} {
		operationHandlers[op].Func = v
	}

	// Outputs.
	for op, f := range map[uint32]interface{}{
		_OP_BMAP:                  _BmapOut{},
		_OP_COPY_FILE_RANGE:       WriteOut{},
		_OP_CREATE:                CreateOut{},
		_OP_GETATTR:               AttrOut{},
		_OP_GETLK:                 LkOut{},
		_OP_GETXATTR:              GetXAttrOut{},
		_OP_INIT:                  InitOut{},
		_OP_IOCTL:                 IoctlOut{},
		_OP_LINK:                  EntryOut{},
		_OP_LISTXATTR:             GetXAttrOut{},
		_OP_LOOKUP:                EntryOut{},
		_OP_LSEEK:                 LseekOut{},
		_OP_MKDIR:                 EntryOut{},
		_OP_MKNOD:                 EntryOut{},
		_OP_NOTIFY_DELETE:         NotifyInvalDeleteOut{},
		_OP_NOTIFY_INVAL_ENTRY:    NotifyInvalEntryOut{},
		_OP_NOTIFY_INVAL_INODE:    NotifyInvalInodeOut{},
		_OP_NOTIFY_RETRIEVE_CACHE: NotifyRetrieveOut{},
		_OP_NOTIFY_STORE_CACHE:    NotifyStoreOut{},
		_OP_OPEN:                  OpenOut{},
		_OP_OPENDIR:               OpenOut{},
		_OP_POLL:                  _PollOut{},
		_OP_SETATTR:               AttrOut{},
		_OP_STATFS:                StatfsOut{},
		_OP_SYMLINK:               EntryOut{},
		_OP_WRITE:                 WriteOut{},
	} {
		operationHandlers[op].OutType = f
		operationHandlers[op].OutputSize = typSize(f)
	}

	// Inputs.
	for op, f := range map[uint32]interface{}{
		_OP_ACCESS:          AccessIn{},
		_OP_BATCH_FORGET:    _BatchForgetIn{},
		_OP_BMAP:            _BmapIn{},
		_OP_COPY_FILE_RANGE: CopyFileRangeIn{},
		_OP_CREATE:          CreateIn{},
		_OP_FALLOCATE:       FallocateIn{},
		_OP_FLUSH:           FlushIn{},
		_OP_FORGET:          ForgetIn{},
		_OP_FSYNC:           FsyncIn{},
		_OP_FSYNCDIR:        FsyncIn{},
		_OP_GETATTR:         GetAttrIn{},
		_OP_GETLK:           LkIn{},
		_OP_GETXATTR:        GetXAttrIn{},
		_OP_INIT:            InitIn{},
		_OP_INTERRUPT:       InterruptIn{},
		_OP_IOCTL:           IoctlIn{},
		_OP_LINK:            LinkIn{},
		_OP_LISTXATTR:       GetXAttrIn{},
		_OP_LSEEK:           LseekIn{},
		_OP_MKDIR:           MkdirIn{},
		_OP_MKNOD:           MknodIn{},
		_OP_NOTIFY_REPLY:    NotifyRetrieveIn{},
		_OP_OPEN:            OpenIn{},
		_OP_OPENDIR:         OpenIn{},
		_OP_POLL:            _PollIn{},
		_OP_READ:            ReadIn{},
		_OP_READDIR:         ReadIn{},
		_OP_READDIRPLUS:     ReadIn{},
		_OP_RELEASE:         ReleaseIn{},
		_OP_RELEASEDIR:      ReleaseIn{},
		_OP_RENAME2:         RenameIn{},
		_OP_RENAME:          Rename1In{},
		_OP_SETATTR:         SetAttrIn{},
		_OP_SETLK:           LkIn{},
		_OP_SETLKW:          LkIn{},
		_OP_SETXATTR:        SetXAttrIn{},
		_OP_WRITE:           WriteIn{},
	} {
		operationHandlers[op].InType = f
		sz := typSize(f)
		operationHandlers[op].InputSize = sz
		if maxInputSize < sz {
			maxInputSize = sz
		}
	}

	// File name args.
	for op, count := range map[uint32]int{
		_OP_CREATE:      1,
		_OP_SETXATTR:    1,
		_OP_GETXATTR:    1,
		_OP_LINK:        1,
		_OP_LOOKUP:      1,
		_OP_MKDIR:       1,
		_OP_MKNOD:       1,
		_OP_REMOVEXATTR: 1,
		_OP_RENAME:      2,
		_OP_RENAME2:     2,
		_OP_RMDIR:       1,
		_OP_SYMLINK:     2,
		_OP_UNLINK:      1,
	} {
		operationHandlers[op].FileNames = count
	}

	checkFixedBufferSize()
}

func checkFixedBufferSize() {
	var r requestAlloc
	sizeOfOutHeader := unsafe.Sizeof(OutHeader{})
	for code, h := range operationHandlers {
		if h.OutputSize+sizeOfOutHeader > unsafe.Sizeof(r.outBuf) {
			log.Panicf("request output buffer too small: code %v, sz %d + %d %v", code, h.OutputSize, sizeOfOutHeader, h)
		}
	}
}
