// See the file LICENSE for copyright and licensing information.

// Adapted from Plan 9 from User Space's src/cmd/9pfuse/fuse.c,
// which carries this notice:
//
// The files in this directory are subject to the following license.
//
// The author of this software is Russ Cox.
//
//         Copyright (c) 2006 Russ Cox
//
// Permission to use, copy, modify, and distribute this software for any
// purpose without fee is hereby granted, provided that this entire notice
// is included in all copies of any software which is or includes a copy
// or modification of this software and in all copies of the supporting
// documentation for such software.
//
// THIS SOFTWARE IS BEING PROVIDED "AS IS", WITHOUT ANY EXPRESS OR IMPLIED
// WARRANTY.  IN PARTICULAR, THE AUTHOR MAKES NO REPRESENTATION OR WARRANTY
// OF ANY KIND CONCERNING THE MERCHANTABILITY OF THIS SOFTWARE OR ITS
// FITNESS FOR ANY PARTICULAR PURPOSE.

// Package fuse enables writing FUSE file systems on Linux and FreeBSD.
//
// There are two approaches to writing a FUSE file system.  The first is to speak
// the low-level message protocol, reading from a Conn using ReadRequest and
// writing using the various Respond methods.  This approach is closest to
// the actual interaction with the kernel and can be the simplest one in contexts
// such as protocol translators.
//
// Servers of synthesized file systems tend to share common
// bookkeeping abstracted away by the second approach, which is to
// call fs.Serve to serve the FUSE protocol using an implementation of
// the service methods in the interfaces FS* (file system), Node* (file
// or directory), and Handle* (opened file or directory).
// There are a daunting number of such methods that can be written,
// but few are required.
// The specific methods are described in the documentation for those interfaces.
//
// The examples/hellofs subdirectory contains a simple illustration of the fs.Serve approach.
//
// # Service Methods
//
// The required and optional methods for the FS, Node, and Handle interfaces
// have the general form
//
//	Op(ctx context.Context, req *OpRequest, resp *OpResponse) error
//
// where Op is the name of a FUSE operation. Op reads request
// parameters from req and writes results to resp. An operation whose
// only result is the error result omits the resp parameter.
//
// Multiple goroutines may call service methods simultaneously; the
// methods being called are responsible for appropriate
// synchronization.
//
// The operation must not hold on to the request or response,
// including any []byte fields such as WriteRequest.Data or
// SetxattrRequest.Xattr.
//
// # Errors
//
// Operations can return errors. The FUSE interface can only
// communicate POSIX errno error numbers to file system clients, the
// message is not visible to file system clients. The returned error
// can implement ErrorNumber to control the errno returned. Without
// ErrorNumber, a generic errno (EIO) is returned.
//
// Error messages will be visible in the debug log as part of the
// response.
//
// # Interrupted Operations
//
// In some file systems, some operations
// may take an undetermined amount of time.  For example, a Read waiting for
// a network message or a matching Write might wait indefinitely.  If the request
// is cancelled and no longer needed, the context will be cancelled.
// Blocking operations should select on a receive from ctx.Done() and attempt to
// abort the operation early if the receive succeeds (meaning the channel is closed).
// To indicate that the operation failed because it was aborted, return syscall.EINTR.
//
// If an operation does not block for an indefinite amount of time, supporting
// cancellation is not necessary.
//
// # Authentication
//
// All requests types embed a Header, meaning that the method can
// inspect req.Pid, req.Uid, and req.Gid as necessary to implement
// permission checking. The kernel FUSE layer normally prevents other
// users from accessing the FUSE file system (to change this, see
// AllowOther), but does not enforce access modes (to change this, see
// DefaultPermissions).
//
// # Mount Options
//
// Behavior and metadata of the mounted file system can be changed by
// passing MountOption values to Mount.
package fuse // import "bazil.org/fuse"

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// A Conn represents a connection to a mounted FUSE file system.
type Conn struct {
	// File handle for kernel communication. Only safe to access if
	// rio or wio is held.
	dev *os.File
	wio sync.RWMutex
	rio sync.RWMutex

	// Protocol version negotiated with initRequest/initResponse.
	proto Protocol
	// Feature flags negotiated with initRequest/initResponse.
	flags InitFlags
}

// MountpointDoesNotExistError is an error returned when the
// mountpoint does not exist.
type MountpointDoesNotExistError struct {
	Path string
}

var _ error = (*MountpointDoesNotExistError)(nil)

func (e *MountpointDoesNotExistError) Error() string {
	return fmt.Sprintf("mountpoint does not exist: %v", e.Path)
}

// Mount mounts a new FUSE connection on the named directory
// and returns a connection for reading and writing FUSE messages.
//
// After a successful return, caller must call Close to free
// resources.
func Mount(dir string, options ...MountOption) (*Conn, error) {
	conf := mountConfig{
		options:   make(map[string]string),
		initFlags: InitAsyncDIO | InitSetxattrExt,
	}
	for _, option := range options {
		if err := option(&conf); err != nil {
			return nil, err
		}
	}

	c := &Conn{}
	f, err := mount(dir, &conf)
	if err != nil {
		return nil, err
	}
	c.dev = f

	if err := initMount(c, &conf); err != nil {
		c.Close()
		_ = Unmount(dir)
		return nil, err
	}

	return c, nil
}

type OldVersionError struct {
	Kernel     Protocol
	LibraryMin Protocol
}

func (e *OldVersionError) Error() string {
	return fmt.Sprintf("kernel FUSE version is too old: %v < %v", e.Kernel, e.LibraryMin)
}

var (
	ErrClosedWithoutInit = errors.New("fuse connection closed without init")
)

func initMount(c *Conn, conf *mountConfig) error {
	req, err := c.ReadRequest()
	if err != nil {
		if err == io.EOF {
			return ErrClosedWithoutInit
		}
		return err
	}
	r, ok := req.(*initRequest)
	if !ok {
		return fmt.Errorf("missing init, got: %T", req)
	}

	min := Protocol{protoVersionMinMajor, protoVersionMinMinor}
	if r.Kernel.LT(min) {
		req.RespondError(Errno(syscall.EPROTO))
		c.Close()
		return &OldVersionError{
			Kernel:     r.Kernel,
			LibraryMin: min,
		}
	}

	proto := Protocol{protoVersionMaxMajor, protoVersionMaxMinor}
	if r.Kernel.LT(proto) {
		// Kernel doesn't support the latest version we have.
		proto = r.Kernel
	}
	c.proto = proto

	c.flags = r.Flags & (InitBigWrites | InitParallelDirOps | conf.initFlags)
	s := &initResponse{
		Library:             proto,
		MaxReadahead:        conf.maxReadahead,
		Flags:               c.flags,
		MaxBackground:       conf.maxBackground,
		CongestionThreshold: conf.congestionThreshold,
		MaxWrite:            maxWrite,
	}
	r.Respond(s)
	return nil
}

// A Request represents a single FUSE request received from the kernel.
// Use a type switch to determine the specific kind.
// A request of unrecognized type will have concrete type *Header.
type Request interface {
	// Hdr returns the Header associated with this request.
	Hdr() *Header

	// RespondError responds to the request with the given error.
	RespondError(error)

	String() string
}

// A RequestID identifies an active FUSE request.
type RequestID uint64

func (r RequestID) String() string {
	return fmt.Sprintf("%#x", uint64(r))
}

// A NodeID is a number identifying a directory or file.
// It must be unique among IDs returned in LookupResponses
// that have not yet been forgotten by ForgetRequests.
type NodeID uint64

func (n NodeID) String() string {
	return fmt.Sprintf("%#x", uint64(n))
}

// A HandleID is a number identifying an open directory or file.
// It only needs to be unique while the directory or file is open.
type HandleID uint64

func (h HandleID) String() string {
	return fmt.Sprintf("%#x", uint64(h))
}

// The RootID identifies the root directory of a FUSE file system.
const RootID NodeID = rootID

// A Header describes the basic information sent in every request.
type Header struct {
	Conn *Conn     `json:"-"` // connection this request was received on
	ID   RequestID // unique ID for request
	Node NodeID    // file or directory the request is about
	Uid  uint32    // user ID of process making request
	Gid  uint32    // group ID of process making request
	Pid  uint32    // process ID of process making request

	// for returning to reqPool
	msg *message
}

func (h *Header) String() string {
	return fmt.Sprintf("ID=%v Node=%v Uid=%d Gid=%d Pid=%d", h.ID, h.Node, h.Uid, h.Gid, h.Pid)
}

func (h *Header) Hdr() *Header {
	return h
}

func (h *Header) noResponse() {
	putMessage(h.msg)
}

func (h *Header) respond(msg []byte) {
	out := (*outHeader)(unsafe.Pointer(&msg[0]))
	out.Unique = uint64(h.ID)
	h.Conn.respond(msg)
	putMessage(h.msg)
}

// An ErrorNumber is an error with a specific error number.
//
// Operations may return an error value that implements ErrorNumber to
// control what specific error number (errno) to return.
type ErrorNumber interface {
	// Errno returns the the error number (errno) for this error.
	Errno() Errno
}

// Deprecated: Return a syscall.Errno directly. See ToErrno for exact
// rules.
const (
	// ENOSYS indicates that the call is not supported.
	ENOSYS = Errno(syscall.ENOSYS)

	// ESTALE is used by Serve to respond to violations of the FUSE protocol.
	ESTALE = Errno(syscall.ESTALE)

	ENOENT = Errno(syscall.ENOENT)
	EIO    = Errno(syscall.EIO)
	EPERM  = Errno(syscall.EPERM)

	// EINTR indicates request was interrupted by an InterruptRequest.
	// See also fs.Intr.
	EINTR = Errno(syscall.EINTR)

	ERANGE  = Errno(syscall.ERANGE)
	ENOTSUP = Errno(syscall.ENOTSUP)
	EEXIST  = Errno(syscall.EEXIST)
)

// DefaultErrno is the errno used when error returned does not
// implement ErrorNumber.
const DefaultErrno = EIO

var errnoNames = map[Errno]string{
	ENOSYS:                      "ENOSYS",
	ESTALE:                      "ESTALE",
	ENOENT:                      "ENOENT",
	EIO:                         "EIO",
	EPERM:                       "EPERM",
	EINTR:                       "EINTR",
	EEXIST:                      "EEXIST",
	Errno(syscall.ENAMETOOLONG): "ENAMETOOLONG",
}

// Errno implements Error and ErrorNumber using a syscall.Errno.
type Errno syscall.Errno

var _ ErrorNumber = Errno(0)
var _ error = Errno(0)

func (e Errno) Errno() Errno {
	return e
}

func (e Errno) String() string {
	return syscall.Errno(e).Error()
}

func (e Errno) Error() string {
	return syscall.Errno(e).Error()
}

// ErrnoName returns the short non-numeric identifier for this errno.
// For example, "EIO".
func (e Errno) ErrnoName() string {
	s := errnoNames[e]
	if s == "" {
		s = fmt.Sprint(e.Errno())
	}
	return s
}

func (e Errno) MarshalText() ([]byte, error) {
	s := e.ErrnoName()
	return []byte(s), nil
}

// ToErrno converts arbitrary errors to Errno.
//
// If the underlying type of err is syscall.Errno, it is used
// directly. No unwrapping is done, to prevent wrong errors from
// leaking via e.g. *os.PathError.
//
// If err unwraps to implement ErrorNumber, that is used.
//
// Finally, returns DefaultErrno.
func ToErrno(err error) Errno {
	if err, ok := err.(syscall.Errno); ok {
		return Errno(err)
	}
	var errnum ErrorNumber
	if errors.As(err, &errnum) {
		return Errno(errnum.Errno())
	}
	return DefaultErrno
}

func (h *Header) RespondError(err error) {
	errno := ToErrno(err)
	// FUSE uses negative errors!
	buf := newBuffer(0)
	hOut := (*outHeader)(unsafe.Pointer(&buf[0]))
	hOut.Error = -int32(errno)
	h.respond(buf)
}

// All requests read from the kernel, without data, are shorter than
// this.
var maxRequestSize = syscall.Getpagesize()
var bufSize = maxRequestSize + maxWrite

// reqPool is a pool of messages.
//
// Lifetime of a logical message is from getMessage to putMessage.
// getMessage is called by ReadRequest. putMessage is called by
// Conn.ReadRequest, Request.Respond, or Request.RespondError.
//
// Messages in the pool are guaranteed to have conn and off zeroed,
// buf allocated and len==bufSize, and hdr set.
var reqPool = sync.Pool{
	New: allocMessage,
}

func allocMessage() interface{} {
	m := &message{buf: make([]byte, bufSize)}
	m.hdr = (*inHeader)(unsafe.Pointer(&m.buf[0]))
	return m
}

func getMessage(c *Conn) *message {
	m := reqPool.Get().(*message)
	m.conn = c
	return m
}

func putMessage(m *message) {
	m.buf = m.buf[:bufSize]
	m.conn = nil
	m.off = 0
	reqPool.Put(m)
}

// a message represents the bytes of a single FUSE message
type message struct {
	conn *Conn
	buf  []byte    // all bytes
	hdr  *inHeader // header
	off  int       // offset for reading additional fields
}

func (m *message) len() uintptr {
	return uintptr(len(m.buf) - m.off)
}

func (m *message) data() unsafe.Pointer {
	var p unsafe.Pointer
	if m.off < len(m.buf) {
		p = unsafe.Pointer(&m.buf[m.off])
	}
	return p
}

func (m *message) bytes() []byte {
	return m.buf[m.off:]
}

func (m *message) Header() Header {
	h := m.hdr
	return Header{
		Conn: m.conn,
		ID:   RequestID(h.Unique),
		Node: NodeID(h.Nodeid),
		Uid:  h.Uid,
		Gid:  h.Gid,
		Pid:  h.Pid,

		msg: m,
	}
}

// fileMode returns a Go os.FileMode from a Unix mode.
func fileMode(unixMode uint32) os.FileMode {
	mode := os.FileMode(unixMode & 0o777)
	switch unixMode & syscall.S_IFMT {
	case syscall.S_IFREG:
		// nothing
	case syscall.S_IFDIR:
		mode |= os.ModeDir
	case syscall.S_IFCHR:
		mode |= os.ModeCharDevice | os.ModeDevice
	case syscall.S_IFBLK:
		mode |= os.ModeDevice
	case syscall.S_IFIFO:
		mode |= os.ModeNamedPipe
	case syscall.S_IFLNK:
		mode |= os.ModeSymlink
	case syscall.S_IFSOCK:
		mode |= os.ModeSocket
	case 0:
		// apparently there's plenty of times when the FUSE request
		// does not contain the file type
		mode |= os.ModeIrregular
	default:
		// not just unavailable in the kernel codepath; known to
		// kernel but unrecognized by us
		Debug(fmt.Sprintf("unrecognized file mode type: %04o", unixMode))
		mode |= os.ModeIrregular
	}
	if unixMode&syscall.S_ISUID != 0 {
		mode |= os.ModeSetuid
	}
	if unixMode&syscall.S_ISGID != 0 {
		mode |= os.ModeSetgid
	}
	if unixMode&syscall.S_ISVTX != 0 {
		mode |= os.ModeSticky
	}
	return mode
}

type noOpcode struct {
	Opcode uint32
}

func (m noOpcode) String() string {
	return fmt.Sprintf("No opcode %v", m.Opcode)
}

type malformedMessage struct {
}

func (malformedMessage) String() string {
	return "malformed message"
}

// Close closes the FUSE connection.
func (c *Conn) Close() error {
	c.wio.Lock()
	defer c.wio.Unlock()
	c.rio.Lock()
	defer c.rio.Unlock()
	return c.dev.Close()
}

// caller must hold wio or rio
func (c *Conn) fd() int {
	return int(c.dev.Fd())
}

func (c *Conn) Protocol() Protocol {
	return c.proto
}

// Features reports the feature flags negotiated between the kernel and
// the FUSE library. See MountOption for how to influence features
// activated.
func (c *Conn) Features() InitFlags {
	return c.flags
}

// ReadRequest returns the next FUSE request from the kernel.
//
// Caller must call either Request.Respond or Request.RespondError in
// a reasonable time. Caller must not retain Request after that call.
func (c *Conn) ReadRequest() (Request, error) {
	m := getMessage(c)
	c.rio.RLock()
	n, err := syscall.Read(c.fd(), m.buf)
	c.rio.RUnlock()
	if err != nil && err != syscall.ENODEV {
		putMessage(m)
		return nil, err
	}
	if n <= 0 {
		putMessage(m)
		return nil, io.EOF
	}
	m.buf = m.buf[:n]

	if n < inHeaderSize {
		putMessage(m)
		return nil, errors.New("fuse: message too short")
	}

	// FreeBSD FUSE sends a short length in the header
	// for FUSE_INIT even though the actual read length is correct.
	if n == inHeaderSize+initInSize && m.hdr.Opcode == opInit && m.hdr.Len < uint32(n) {
		m.hdr.Len = uint32(n)
	}

	if m.hdr.Len != uint32(n) {
		// prepare error message before returning m to pool
		err := fmt.Errorf("fuse: read %d opcode %d but expected %d", n, m.hdr.Opcode, m.hdr.Len)
		putMessage(m)
		return nil, err
	}

	m.off = inHeaderSize

	// Convert to data structures.
	// Do not trust kernel to hand us well-formed data.
	var req Request
	switch m.hdr.Opcode {
	default:
		Debug(noOpcode{Opcode: m.hdr.Opcode})
		goto unrecognized

	case opLookup:
		buf := m.bytes()
		n := len(buf)
		if n == 0 || buf[n-1] != '\x00' {
			goto corrupt
		}
		req = &LookupRequest{
			Header: m.Header(),
			Name:   string(buf[:n-1]),
		}

	case opForget:
		in := (*forgetIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		req = &ForgetRequest{
			Header: m.Header(),
			N:      in.Nlookup,
		}

	case opGetattr:
		switch {
		case c.proto.LT(Protocol{7, 9}):
			req = &GetattrRequest{
				Header: m.Header(),
			}

		default:
			in := (*getattrIn)(m.data())
			if m.len() < unsafe.Sizeof(*in) {
				goto corrupt
			}
			req = &GetattrRequest{
				Header: m.Header(),
				Flags:  GetattrFlags(in.GetattrFlags),
				Handle: HandleID(in.Fh),
			}
		}

	case opSetattr:
		in := (*setattrIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		req = &SetattrRequest{
			Header: m.Header(),
			Valid:  SetattrValid(in.Valid),
			Handle: HandleID(in.Fh),
			Size:   in.Size,
			Atime:  time.Unix(int64(in.Atime), int64(in.AtimeNsec)),
			Mtime:  time.Unix(int64(in.Mtime), int64(in.MtimeNsec)),
			Ctime:  time.Unix(int64(in.Ctime), int64(in.CtimeNsec)),
			Mode:   fileMode(in.Mode),
			Uid:    in.Uid,
			Gid:    in.Gid,
		}

	case opReadlink:
		if len(m.bytes()) > 0 {
			goto corrupt
		}
		req = &ReadlinkRequest{
			Header: m.Header(),
		}

	case opSymlink:
		// m.bytes() is "newName\0target\0"
		names := m.bytes()
		if len(names) == 0 || names[len(names)-1] != 0 {
			goto corrupt
		}
		i := bytes.IndexByte(names, '\x00')
		if i < 0 {
			goto corrupt
		}
		newName, target := names[0:i], names[i+1:len(names)-1]
		req = &SymlinkRequest{
			Header:  m.Header(),
			NewName: string(newName),
			Target:  string(target),
		}

	case opLink:
		in := (*linkIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		newName := m.bytes()[unsafe.Sizeof(*in):]
		if len(newName) < 2 || newName[len(newName)-1] != 0 {
			goto corrupt
		}
		newName = newName[:len(newName)-1]
		req = &LinkRequest{
			Header:  m.Header(),
			OldNode: NodeID(in.Oldnodeid),
			NewName: string(newName),
		}

	case opMknod:
		size := mknodInSize(c.proto)
		if m.len() < size {
			goto corrupt
		}
		in := (*mknodIn)(m.data())
		name := m.bytes()[size:]
		if len(name) < 2 || name[len(name)-1] != '\x00' {
			goto corrupt
		}
		name = name[:len(name)-1]
		r := &MknodRequest{
			Header: m.Header(),
			Mode:   fileMode(in.Mode),
			Rdev:   in.Rdev,
			Name:   string(name),
		}
		if c.proto.GE(Protocol{7, 12}) {
			r.Umask = fileMode(in.Umask) & os.ModePerm
		}
		req = r

	case opMkdir:
		size := mkdirInSize(c.proto)
		if m.len() < size {
			goto corrupt
		}
		in := (*mkdirIn)(m.data())
		name := m.bytes()[size:]
		i := bytes.IndexByte(name, '\x00')
		if i < 0 {
			goto corrupt
		}
		r := &MkdirRequest{
			Header: m.Header(),
			Name:   string(name[:i]),
			// observed on Linux: mkdirIn.Mode & syscall.S_IFMT == 0,
			// and this causes fileMode to go into it's "no idea"
			// code branch; enforce type to directory
			Mode: fileMode((in.Mode &^ syscall.S_IFMT) | syscall.S_IFDIR),
		}
		if c.proto.GE(Protocol{7, 12}) {
			r.Umask = fileMode(in.Umask) & os.ModePerm
		}
		req = r

	case opUnlink, opRmdir:
		buf := m.bytes()
		n := len(buf)
		if n == 0 || buf[n-1] != '\x00' {
			goto corrupt
		}
		req = &RemoveRequest{
			Header: m.Header(),
			Name:   string(buf[:n-1]),
			Dir:    m.hdr.Opcode == opRmdir,
		}

	case opRename:
		in := (*renameIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		newDirNodeID := NodeID(in.Newdir)
		oldNew := m.bytes()[unsafe.Sizeof(*in):]
		// oldNew should be "old\x00new\x00"
		if len(oldNew) < 4 {
			goto corrupt
		}
		if oldNew[len(oldNew)-1] != '\x00' {
			goto corrupt
		}
		i := bytes.IndexByte(oldNew, '\x00')
		if i < 0 {
			goto corrupt
		}
		oldName, newName := string(oldNew[:i]), string(oldNew[i+1:len(oldNew)-1])
		req = &RenameRequest{
			Header:  m.Header(),
			NewDir:  newDirNodeID,
			OldName: oldName,
			NewName: newName,
		}

	case opOpendir, opOpen:
		in := (*openIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		req = &OpenRequest{
			Header:    m.Header(),
			Dir:       m.hdr.Opcode == opOpendir,
			Flags:     openFlags(in.Flags),
			OpenFlags: OpenRequestFlags(in.OpenFlags),
		}

	case opRead, opReaddir:
		in := (*readIn)(m.data())
		if m.len() < readInSize(c.proto) {
			goto corrupt
		}
		r := &ReadRequest{
			Header: m.Header(),
			Dir:    m.hdr.Opcode == opReaddir,
			Handle: HandleID(in.Fh),
			Offset: int64(in.Offset),
			Size:   int(in.Size),
		}
		if c.proto.GE(Protocol{7, 9}) {
			r.Flags = ReadFlags(in.ReadFlags)
			r.LockOwner = LockOwner(in.LockOwner)
			r.FileFlags = openFlags(in.Flags)
		}
		req = r

	case opWrite:
		in := (*writeIn)(m.data())
		if m.len() < writeInSize(c.proto) {
			goto corrupt
		}
		r := &WriteRequest{
			Header: m.Header(),
			Handle: HandleID(in.Fh),
			Offset: int64(in.Offset),
			Flags:  WriteFlags(in.WriteFlags),
		}
		if c.proto.GE(Protocol{7, 9}) {
			r.LockOwner = LockOwner(in.LockOwner)
			r.FileFlags = openFlags(in.Flags)
		}
		buf := m.bytes()[writeInSize(c.proto):]
		if uint32(len(buf)) < in.Size {
			goto corrupt
		}
		r.Data = buf
		req = r

	case opStatfs:
		req = &StatfsRequest{
			Header: m.Header(),
		}

	case opRelease, opReleasedir:
		in := (*releaseIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		req = &ReleaseRequest{
			Header:       m.Header(),
			Dir:          m.hdr.Opcode == opReleasedir,
			Handle:       HandleID(in.Fh),
			Flags:        openFlags(in.Flags),
			ReleaseFlags: ReleaseFlags(in.ReleaseFlags),
			LockOwner:    LockOwner(in.LockOwner),
		}

	case opFsync, opFsyncdir:
		in := (*fsyncIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		req = &FsyncRequest{
			Dir:    m.hdr.Opcode == opFsyncdir,
			Header: m.Header(),
			Handle: HandleID(in.Fh),
			Flags:  in.FsyncFlags,
		}

	case opSetxattr:
		size := setxattrInSize(c.flags)
		if m.len() < size {
			goto corrupt
		}
		in := (*setxattrIn)(m.data())
		m.off += int(size)
		name := m.bytes()
		i := bytes.IndexByte(name, '\x00')
		if i < 0 {
			goto corrupt
		}
		xattr := name[i+1:]
		if uint32(len(xattr)) < in.Size {
			goto corrupt
		}
		xattr = xattr[:in.Size]
		r := &SetxattrRequest{
			Header: m.Header(),
			Flags:  in.Flags,
			Name:   string(name[:i]),
			Xattr:  xattr,
		}
		if c.proto.GE(Protocol{7, 32}) {
			r.SetxattrFlags = SetxattrFlags(in.SetxattrFlags)
		}
		req = r

	case opGetxattr:
		in := (*getxattrIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		name := m.bytes()[unsafe.Sizeof(*in):]
		i := bytes.IndexByte(name, '\x00')
		if i < 0 {
			goto corrupt
		}
		req = &GetxattrRequest{
			Header: m.Header(),
			Name:   string(name[:i]),
			Size:   in.Size,
		}

	case opListxattr:
		in := (*getxattrIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		req = &ListxattrRequest{
			Header: m.Header(),
			Size:   in.Size,
		}

	case opRemovexattr:
		buf := m.bytes()
		n := len(buf)
		if n == 0 || buf[n-1] != '\x00' {
			goto corrupt
		}
		req = &RemovexattrRequest{
			Header: m.Header(),
			Name:   string(buf[:n-1]),
		}

	case opFlush:
		in := (*flushIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		req = &FlushRequest{
			Header:    m.Header(),
			Handle:    HandleID(in.Fh),
			LockOwner: LockOwner(in.LockOwner),
		}

	case opInit:
		in := (*initIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		req = &initRequest{
			Header:       m.Header(),
			Kernel:       Protocol{in.Major, in.Minor},
			MaxReadahead: in.MaxReadahead,
			Flags:        InitFlags(in.Flags),
		}

	case opAccess:
		in := (*accessIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		req = &AccessRequest{
			Header: m.Header(),
			Mask:   in.Mask,
		}

	case opCreate:
		size := createInSize(c.proto)
		if m.len() < size {
			goto corrupt
		}
		in := (*createIn)(m.data())
		name := m.bytes()[size:]
		i := bytes.IndexByte(name, '\x00')
		if i < 0 {
			goto corrupt
		}
		r := &CreateRequest{
			Header: m.Header(),
			Flags:  openFlags(in.Flags),
			Mode:   fileMode(in.Mode),
			Name:   string(name[:i]),
		}
		if c.proto.GE(Protocol{7, 12}) {
			r.Umask = fileMode(in.Umask) & os.ModePerm
		}
		req = r

	case opInterrupt:
		in := (*interruptIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		req = &InterruptRequest{
			Header: m.Header(),
			IntrID: RequestID(in.Unique),
		}

	case opBmap:
		// bmap asks to map a byte offset within a file to a single
		// uint64. On Linux, it triggers only with blkdev fuse mounts,
		// that claim to be backed by an actual block device. FreeBSD
		// seems to send it for just any fuse mount, whether there's a
		// block device involved or not.
		goto unrecognized

	case opDestroy:
		req = &DestroyRequest{
			Header: m.Header(),
		}

	case opNotifyReply:
		req = &NotifyReply{
			Header: m.Header(),
			msg:    m,
		}

	case opPoll:
		in := (*pollIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		req = &PollRequest{
			Header: m.Header(),
			Handle: HandleID(in.Fh),
			kh:     in.Kh,
			Flags:  PollFlags(in.Flags),
			Events: PollEvents(in.Events),
		}

	case opBatchForget:
		in := (*batchForgetIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		m.off += int(unsafe.Sizeof(*in))
		items := make([]BatchForgetItem, 0, in.Count)
		for count := in.Count; count > 0; count-- {
			one := (*forgetOne)(m.data())
			if m.len() < unsafe.Sizeof(*one) {
				goto corrupt
			}
			m.off += int(unsafe.Sizeof(*one))
			items = append(items, BatchForgetItem{
				NodeID: NodeID(one.NodeID),
				N:      one.Nlookup,
			})
		}
		req = &BatchForgetRequest{
			Header: m.Header(),
			Forget: items,
		}

	case opSetlk, opSetlkw:
		in := (*lkIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		tmp := &LockRequest{
			Header:    m.Header(),
			Handle:    HandleID(in.Fh),
			LockOwner: LockOwner(in.Owner),
			Lock: FileLock{
				Start: in.Lk.Start,
				End:   in.Lk.End,
				Type:  LockType(in.Lk.Type),
				PID:   int32(in.Lk.PID),
			},
			LockFlags: LockFlags(in.LkFlags),
		}
		switch {
		case tmp.Lock.Type == LockUnlock:
			req = (*UnlockRequest)(tmp)
		case m.hdr.Opcode == opSetlkw:
			req = (*LockWaitRequest)(tmp)
		default:
			req = tmp
		}

	case opGetlk:
		in := (*lkIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		req = &QueryLockRequest{
			Header:    m.Header(),
			Handle:    HandleID(in.Fh),
			LockOwner: LockOwner(in.Owner),
			Lock: FileLock{
				Start: in.Lk.Start,
				End:   in.Lk.End,
				Type:  LockType(in.Lk.Type),
				// fuse.h claims this field is a uint32, but then the
				// spec talks about -1 as a value, and using int as
				// the C definition is pretty common. Make our API use
				// a signed integer.
				PID: int32(in.Lk.PID),
			},
			LockFlags: LockFlags(in.LkFlags),
		}

	case opFAllocate:
		in := (*fAllocateIn)(m.data())
		if m.len() < unsafe.Sizeof(*in) {
			goto corrupt
		}
		req = &FAllocateRequest{
			Header: m.Header(),
			Handle: HandleID(in.Fh),
			Offset: in.Offset,
			Length: in.Length,
			Mode:   FAllocateFlags(in.Mode),
		}
	}

	return req, nil

corrupt:
	Debug(malformedMessage{})
	putMessage(m)
	return nil, fmt.Errorf("fuse: malformed message")

unrecognized:
	// Unrecognized message.
	// Assume higher-level code will send a "no idea what you mean" error.
	req = &UnrecognizedRequest{
		Header: m.Header(),
		Opcode: m.hdr.Opcode,
	}
	return req, nil
}

type bugShortKernelWrite struct {
	Written int64
	Length  int64
	Error   string
	Stack   string
}

func (b bugShortKernelWrite) String() string {
	return fmt.Sprintf("short kernel write: written=%d/%d error=%q stack=\n%s", b.Written, b.Length, b.Error, b.Stack)
}

type bugKernelWriteError struct {
	Error string
	Stack string
}

func (b bugKernelWriteError) String() string {
	return fmt.Sprintf("kernel write error: error=%q stack=\n%s", b.Error, b.Stack)
}

// safe to call even with nil error
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (c *Conn) writeToKernel(msg []byte) error {
	out := (*outHeader)(unsafe.Pointer(&msg[0]))
	out.Len = uint32(len(msg))

	c.wio.RLock()
	defer c.wio.RUnlock()
	nn, err := syscall.Write(c.fd(), msg)
	if err == nil && nn != len(msg) {
		Debug(bugShortKernelWrite{
			Written: int64(nn),
			Length:  int64(len(msg)),
			Error:   errorString(err),
			Stack:   stack(),
		})
	}
	return err
}

func (c *Conn) respond(msg []byte) {
	if err := c.writeToKernel(msg); err != nil {
		Debug(bugKernelWriteError{
			Error: errorString(err),
			Stack: stack(),
		})
	}
}

type notCachedError struct{}

func (notCachedError) Error() string {
	return "node not cached"
}

var _ ErrorNumber = notCachedError{}

func (notCachedError) Errno() Errno {
	// Behave just like if the original syscall.ENOENT had been passed
	// straight through.
	return ENOENT
}

var (
	ErrNotCached = notCachedError{}
)

// sendNotify sends a notification to kernel.
//
// A returned ENOENT is translated to a friendlier error.
func (c *Conn) sendNotify(msg []byte) error {
	switch err := c.writeToKernel(msg); err {
	case syscall.ENOENT:
		return ErrNotCached
	default:
		return err
	}
}

// InvalidateNode invalidates the kernel cache of the attributes and a
// range of the data of a node.
//
// Giving offset 0 and size -1 means all data. To invalidate just the
// attributes, give offset 0 and size 0.
//
// Returns ErrNotCached if the kernel is not currently caching the
// node.
func (c *Conn) InvalidateNode(nodeID NodeID, off int64, size int64) error {
	buf := newBuffer(unsafe.Sizeof(notifyInvalInodeOut{}))
	h := (*outHeader)(unsafe.Pointer(&buf[0]))
	// h.Unique is 0
	h.Error = notifyCodeInvalInode
	out := (*notifyInvalInodeOut)(buf.alloc(unsafe.Sizeof(notifyInvalInodeOut{})))
	out.Ino = uint64(nodeID)
	out.Off = off
	out.Len = size
	return c.sendNotify(buf)
}

// InvalidateEntry invalidates the kernel cache of the directory entry
// identified by parent directory node ID and entry basename.
//
// Kernel may or may not cache directory listings. To invalidate
// those, use InvalidateNode to invalidate all of the data for a
// directory. (As of 2015-06, Linux FUSE does not cache directory
// listings.)
//
// Returns ErrNotCached if the kernel is not currently caching the
// node.
func (c *Conn) InvalidateEntry(parent NodeID, name string) error {
	const maxUint32 = ^uint32(0)
	if uint64(len(name)) > uint64(maxUint32) {
		// very unlikely, but we don't want to silently truncate
		return syscall.ENAMETOOLONG
	}
	buf := newBuffer(unsafe.Sizeof(notifyInvalEntryOut{}) + uintptr(len(name)) + 1)
	h := (*outHeader)(unsafe.Pointer(&buf[0]))
	// h.Unique is 0
	h.Error = notifyCodeInvalEntry
	out := (*notifyInvalEntryOut)(buf.alloc(unsafe.Sizeof(notifyInvalEntryOut{})))
	out.Parent = uint64(parent)
	out.Namelen = uint32(len(name))
	buf = append(buf, name...)
	buf = append(buf, '\x00')
	return c.sendNotify(buf)
}

// NotifyDelete informs the kernel that a directory entry has been deleted.
//
// Using this instead of [InvalidateEntry] races on networked systems where the directory is concurrently in use.
// See [Linux kernel commit `451d0f599934fd97faf54a5d7954b518e66192cb`] for more.
//
// `child` can be 0 to delete whatever entry is found with the given name, or set to ensure only matching entry is deleted.
//
// Only available when [Conn.Protocol] is greater than or equal to 7.18, see [Protocol.HasNotifyDelete].
//
// Errors include:
//
//   - [ENOTDIR]: `parent` does not refer to a directory
//   - [ENOENT]: no such entry found
//   - [EBUSY]: entry is a mountpoint
//   - [ENOTEMPTY]: entry is a directory, with entries inside it still cached
//
// [Linux kernel commit `451d0f599934fd97faf54a5d7954b518e66192cb`]: https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/commit/?id=451d0f599934fd97faf54a5d7954b518e66192cb
func (c *Conn) NotifyDelete(parent NodeID, child NodeID, name string) error {
	const maxUint32 = ^uint32(0)
	if uint64(len(name)) > uint64(maxUint32) {
		// very unlikely, but we don't want to silently truncate
		return syscall.ENAMETOOLONG
	}
	buf := newBuffer(unsafe.Sizeof(notifyDeleteOut{}) + uintptr(len(name)) + 1)
	h := (*outHeader)(unsafe.Pointer(&buf[0]))
	// h.Unique is 0
	h.Error = notifyCodeDelete
	out := (*notifyDeleteOut)(buf.alloc(unsafe.Sizeof(notifyDeleteOut{})))
	out.Parent = uint64(parent)
	out.Child = uint64(child)
	out.Namelen = uint32(len(name))
	buf = append(buf, name...)
	buf = append(buf, '\x00')
	return c.sendNotify(buf)
}

func (c *Conn) NotifyStore(nodeID NodeID, offset uint64, data []byte) error {
	buf := newBuffer(unsafe.Sizeof(notifyStoreOut{}) + uintptr(len(data)))
	h := (*outHeader)(unsafe.Pointer(&buf[0]))
	// h.Unique is 0
	h.Error = notifyCodeStore
	out := (*notifyStoreOut)(buf.alloc(unsafe.Sizeof(notifyStoreOut{})))
	out.Nodeid = uint64(nodeID)
	out.Offset = offset
	out.Size = uint32(len(data))
	buf = append(buf, data...)
	return c.sendNotify(buf)
}

type NotifyRetrieval struct {
	// we may want fields later, so don't let callers know it's the
	// empty struct
	_ struct{}
}

func (n *NotifyRetrieval) Finish(r *NotifyReply) []byte {
	m := r.msg
	defer putMessage(m)
	in := (*notifyRetrieveIn)(m.data())
	if m.len() < unsafe.Sizeof(*in) {
		Debug(malformedMessage{})
		return nil
	}
	m.off += int(unsafe.Sizeof(*in))
	buf := m.bytes()
	if uint32(len(buf)) < in.Size {
		Debug(malformedMessage{})
		return nil
	}

	data := make([]byte, in.Size)
	copy(data, buf)
	return data
}

func (c *Conn) NotifyRetrieve(notificationID RequestID, nodeID NodeID, offset uint64, size uint32) (*NotifyRetrieval, error) {
	// notificationID may collide with kernel-chosen requestIDs, it's
	// up to the caller to branch based on the opCode.

	buf := newBuffer(unsafe.Sizeof(notifyRetrieveOut{}))
	h := (*outHeader)(unsafe.Pointer(&buf[0]))
	// h.Unique is 0
	h.Error = notifyCodeRetrieve
	out := (*notifyRetrieveOut)(buf.alloc(unsafe.Sizeof(notifyRetrieveOut{})))
	out.NotifyUnique = uint64(notificationID)
	out.Nodeid = uint64(nodeID)
	out.Offset = offset
	// kernel constrains size to maxWrite for us
	out.Size = size
	if err := c.sendNotify(buf); err != nil {
		return nil, err
	}
	r := &NotifyRetrieval{}
	return r, nil
}

// NotifyPollWakeup sends a notification to the kernel to wake up all
// clients waiting on this node. Wakeup is a value from a PollRequest
// for a Handle or a Node currently alive (Forget has not been called
// on it).
func (c *Conn) NotifyPollWakeup(wakeup PollWakeup) error {
	if wakeup.kh == 0 {
		// likely somebody ignored the comma-ok return
		return nil
	}
	buf := newBuffer(unsafe.Sizeof(notifyPollWakeupOut{}))
	h := (*outHeader)(unsafe.Pointer(&buf[0]))
	// h.Unique is 0
	h.Error = notifyCodePoll
	out := (*notifyPollWakeupOut)(buf.alloc(unsafe.Sizeof(notifyPollWakeupOut{})))
	out.Kh = wakeup.kh
	return c.sendNotify(buf)
}

// LockOwner is a file-local opaque identifier assigned by the kernel
// to identify the owner of a particular lock.
type LockOwner uint64

func (o LockOwner) String() string {
	if o == 0 {
		return "0"
	}
	return fmt.Sprintf("%016x", uint64(o))
}

// An initRequest is the first request sent on a FUSE file system.
type initRequest struct {
	Header `json:"-"`
	Kernel Protocol
	// Maximum readahead in bytes that the kernel plans to use.
	MaxReadahead uint32
	Flags        InitFlags
}

var _ Request = (*initRequest)(nil)

func (r *initRequest) String() string {
	return fmt.Sprintf("Init [%v] %v ra=%d fl=%v", &r.Header, r.Kernel, r.MaxReadahead, r.Flags)
}

type UnrecognizedRequest struct {
	Header `json:"-"`
	Opcode uint32
}

var _ Request = (*UnrecognizedRequest)(nil)

func (r *UnrecognizedRequest) String() string {
	return fmt.Sprintf("Unrecognized [%v] opcode=%d", &r.Header, r.Opcode)
}

// An initResponse is the response to an initRequest.
type initResponse struct {
	Library Protocol
	// Maximum readahead in bytes that the kernel can use. Ignored if
	// greater than initRequest.MaxReadahead.
	MaxReadahead uint32
	Flags        InitFlags
	// Maximum number of outstanding background requests
	MaxBackground uint16
	// Number of background requests at which congestion starts
	CongestionThreshold uint16
	// Maximum size of a single write operation.
	// Linux enforces a minimum of 4 KiB.
	MaxWrite uint32
}

func (r *initResponse) String() string {
	return fmt.Sprintf("Init %v ra=%d fl=%v maxbg=%d cg=%d w=%d", r.Library, r.MaxReadahead, r.Flags, r.MaxBackground, r.CongestionThreshold, r.MaxWrite)
}

// Respond replies to the request with the given response.
func (r *initRequest) Respond(resp *initResponse) {
	buf := newBuffer(unsafe.Sizeof(initOut{}))
	out := (*initOut)(buf.alloc(unsafe.Sizeof(initOut{})))
	out.Major = resp.Library.Major
	out.Minor = resp.Library.Minor
	out.MaxReadahead = resp.MaxReadahead
	out.Flags = uint32(resp.Flags)
	out.MaxBackground = resp.MaxBackground
	out.CongestionThreshold = resp.CongestionThreshold
	out.MaxWrite = resp.MaxWrite

	// MaxWrite larger than our receive buffer would just lead to
	// errors on large writes.
	if out.MaxWrite > maxWrite {
		out.MaxWrite = maxWrite
	}

	switch {
	case resp.Library.LT(Protocol{7, 23}):
		// Protocol version 7.23 added fields to `fuse_init_out`, see `FUSE_COMPAT_22_INIT_OUT_SIZE`.
		buf = buf[:unsafe.Sizeof(outHeader{})+24]
	}
	r.respond(buf)
}

// A StatfsRequest requests information about the mounted file system.
type StatfsRequest struct {
	Header `json:"-"`
}

var _ Request = (*StatfsRequest)(nil)

func (r *StatfsRequest) String() string {
	return fmt.Sprintf("Statfs [%s]", &r.Header)
}

// Respond replies to the request with the given response.
func (r *StatfsRequest) Respond(resp *StatfsResponse) {
	buf := newBuffer(unsafe.Sizeof(statfsOut{}))
	out := (*statfsOut)(buf.alloc(unsafe.Sizeof(statfsOut{})))
	out.St = kstatfs{
		Blocks:  resp.Blocks,
		Bfree:   resp.Bfree,
		Bavail:  resp.Bavail,
		Files:   resp.Files,
		Ffree:   resp.Ffree,
		Bsize:   resp.Bsize,
		Namelen: resp.Namelen,
		Frsize:  resp.Frsize,
	}
	r.respond(buf)
}

// A StatfsResponse is the response to a StatfsRequest.
type StatfsResponse struct {
	Blocks  uint64 // Total data blocks in file system.
	Bfree   uint64 // Free blocks in file system.
	Bavail  uint64 // Free blocks in file system if you're not root.
	Files   uint64 // Total files in file system.
	Ffree   uint64 // Free files in file system.
	Bsize   uint32 // Block size
	Namelen uint32 // Maximum file name length?
	Frsize  uint32 // Fragment size, smallest addressable data size in the file system.
}

func (r *StatfsResponse) String() string {
	return fmt.Sprintf("Statfs blocks=%d/%d/%d files=%d/%d bsize=%d frsize=%d namelen=%d",
		r.Bavail, r.Bfree, r.Blocks,
		r.Ffree, r.Files,
		r.Bsize,
		r.Frsize,
		r.Namelen,
	)
}

// An AccessRequest asks whether the file can be accessed
// for the purpose specified by the mask.
type AccessRequest struct {
	Header `json:"-"`
	Mask   uint32
}

var _ Request = (*AccessRequest)(nil)

func (r *AccessRequest) String() string {
	return fmt.Sprintf("Access [%s] mask=%#x", &r.Header, r.Mask)
}

// Respond replies to the request indicating that access is allowed.
// To deny access, use RespondError.
func (r *AccessRequest) Respond() {
	buf := newBuffer(0)
	r.respond(buf)
}

// An Attr is the metadata for a single file or directory.
type Attr struct {
	Valid time.Duration // how long Attr can be cached

	Inode     uint64      // inode number
	Size      uint64      // size in bytes
	Blocks    uint64      // size in 512-byte units
	Atime     time.Time   // time of last access
	Mtime     time.Time   // time of last modification
	Ctime     time.Time   // time of last inode change
	Mode      os.FileMode // file mode
	Nlink     uint32      // number of links (usually 1)
	Uid       uint32      // owner uid
	Gid       uint32      // group gid
	Rdev      uint32      // device numbers
	BlockSize uint32      // preferred blocksize for filesystem I/O
	Flags     AttrFlags
}

func (a Attr) String() string {
	return fmt.Sprintf("valid=%v ino=%v size=%d mode=%v", a.Valid, a.Inode, a.Size, a.Mode)
}

func unixTime(t time.Time) (sec uint64, nsec uint32) {
	nano := t.UnixNano()
	sec = uint64(nano / 1e9)
	nsec = uint32(nano % 1e9)
	return
}

func (a *Attr) attr(out *attr, proto Protocol) {
	out.Ino = a.Inode
	out.Size = a.Size
	out.Blocks = a.Blocks
	out.Atime, out.AtimeNsec = unixTime(a.Atime)
	out.Mtime, out.MtimeNsec = unixTime(a.Mtime)
	out.Ctime, out.CtimeNsec = unixTime(a.Ctime)
	out.Mode = uint32(a.Mode) & 0o777
	switch {
	default:
		out.Mode |= syscall.S_IFREG
	case a.Mode&os.ModeDir != 0:
		out.Mode |= syscall.S_IFDIR
	case a.Mode&os.ModeDevice != 0:
		if a.Mode&os.ModeCharDevice != 0 {
			out.Mode |= syscall.S_IFCHR
		} else {
			out.Mode |= syscall.S_IFBLK
		}
	case a.Mode&os.ModeNamedPipe != 0:
		out.Mode |= syscall.S_IFIFO
	case a.Mode&os.ModeSymlink != 0:
		out.Mode |= syscall.S_IFLNK
	case a.Mode&os.ModeSocket != 0:
		out.Mode |= syscall.S_IFSOCK
	}
	if a.Mode&os.ModeSetuid != 0 {
		out.Mode |= syscall.S_ISUID
	}
	if a.Mode&os.ModeSetgid != 0 {
		out.Mode |= syscall.S_ISGID
	}
	out.Nlink = a.Nlink
	out.Uid = a.Uid
	out.Gid = a.Gid
	out.Rdev = a.Rdev
	if proto.GE(Protocol{7, 9}) {
		out.Blksize = a.BlockSize
	}
	if proto.GE(Protocol{7, 32}) {
		out.Flags = uint32(a.Flags & attrSubMount)
	}
}

// A GetattrRequest asks for the metadata for the file denoted by r.Node.
type GetattrRequest struct {
	Header `json:"-"`
	Flags  GetattrFlags
	Handle HandleID
}

var _ Request = (*GetattrRequest)(nil)

func (r *GetattrRequest) String() string {
	return fmt.Sprintf("Getattr [%s] %v fl=%v", &r.Header, r.Handle, r.Flags)
}

// Respond replies to the request with the given response.
func (r *GetattrRequest) Respond(resp *GetattrResponse) {
	size := attrOutSize(r.Header.Conn.proto)
	buf := newBuffer(size)
	out := (*attrOut)(buf.alloc(size))
	out.AttrValid = uint64(resp.Attr.Valid / time.Second)
	out.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	resp.Attr.attr(&out.Attr, r.Header.Conn.proto)
	r.respond(buf)
}

// A GetattrResponse is the response to a GetattrRequest.
type GetattrResponse struct {
	Attr Attr // file attributes
}

func (r *GetattrResponse) String() string {
	return fmt.Sprintf("Getattr %v", r.Attr)
}

// A GetxattrRequest asks for the extended attributes associated with r.Node.
type GetxattrRequest struct {
	Header `json:"-"`

	// Maximum size to return.
	Size uint32

	// Name of the attribute requested.
	Name string
}

var _ Request = (*GetxattrRequest)(nil)

func (r *GetxattrRequest) String() string {
	return fmt.Sprintf("Getxattr [%s] %q %d", &r.Header, r.Name, r.Size)
}

// Respond replies to the request with the given response.
func (r *GetxattrRequest) Respond(resp *GetxattrResponse) {
	if r.Size == 0 {
		buf := newBuffer(unsafe.Sizeof(getxattrOut{}))
		out := (*getxattrOut)(buf.alloc(unsafe.Sizeof(getxattrOut{})))
		out.Size = uint32(len(resp.Xattr))
		r.respond(buf)
	} else {
		buf := newBuffer(uintptr(len(resp.Xattr)))
		buf = append(buf, resp.Xattr...)
		r.respond(buf)
	}
}

// A GetxattrResponse is the response to a GetxattrRequest.
type GetxattrResponse struct {
	Xattr []byte
}

func (r *GetxattrResponse) String() string {
	return fmt.Sprintf("Getxattr %q", r.Xattr)
}

// A ListxattrRequest asks to list the extended attributes associated with r.Node.
type ListxattrRequest struct {
	Header `json:"-"`
	Size   uint32 // maximum size to return
}

var _ Request = (*ListxattrRequest)(nil)

func (r *ListxattrRequest) String() string {
	return fmt.Sprintf("Listxattr [%s] %d", &r.Header, r.Size)
}

// Respond replies to the request with the given response.
func (r *ListxattrRequest) Respond(resp *ListxattrResponse) {
	if r.Size == 0 {
		buf := newBuffer(unsafe.Sizeof(getxattrOut{}))
		out := (*getxattrOut)(buf.alloc(unsafe.Sizeof(getxattrOut{})))
		out.Size = uint32(len(resp.Xattr))
		r.respond(buf)
	} else {
		buf := newBuffer(uintptr(len(resp.Xattr)))
		buf = append(buf, resp.Xattr...)
		r.respond(buf)
	}
}

// A ListxattrResponse is the response to a ListxattrRequest.
type ListxattrResponse struct {
	Xattr []byte
}

func (r *ListxattrResponse) String() string {
	return fmt.Sprintf("Listxattr %q", r.Xattr)
}

// Append adds an extended attribute name to the response.
func (r *ListxattrResponse) Append(names ...string) {
	for _, name := range names {
		r.Xattr = append(r.Xattr, name...)
		r.Xattr = append(r.Xattr, '\x00')
	}
}

// A RemovexattrRequest asks to remove an extended attribute associated with r.Node.
type RemovexattrRequest struct {
	Header `json:"-"`
	Name   string // name of extended attribute
}

var _ Request = (*RemovexattrRequest)(nil)

func (r *RemovexattrRequest) String() string {
	return fmt.Sprintf("Removexattr [%s] %q", &r.Header, r.Name)
}

// Respond replies to the request, indicating that the attribute was removed.
func (r *RemovexattrRequest) Respond() {
	buf := newBuffer(0)
	r.respond(buf)
}

// A SetxattrRequest asks to set an extended attribute associated with a file.
type SetxattrRequest struct {
	Header `json:"-"`

	// Flags can make the request fail if attribute does/not already
	// exist. Unfortunately, the constants are platform-specific and
	// not exposed by Go1.2. Look for XATTR_CREATE, XATTR_REPLACE.
	//
	// TODO improve this later
	//
	// TODO XATTR_CREATE and exist -> EEXIST
	//
	// TODO XATTR_REPLACE and not exist -> ENODATA
	Flags uint32

	// FUSE-specific flags (as opposed to the flags from filesystem client `setxattr(2)` flags argument).
	SetxattrFlags SetxattrFlags

	Name  string
	Xattr []byte
}

var _ Request = (*SetxattrRequest)(nil)

func trunc(b []byte, max int) ([]byte, string) {
	if len(b) > max {
		return b[:max], "..."
	}
	return b, ""
}

func (r *SetxattrRequest) String() string {
	xattr, tail := trunc(r.Xattr, 16)
	return fmt.Sprintf("Setxattr [%s] %q %q%s fl=%v", &r.Header, r.Name, xattr, tail, r.Flags)
}

// Respond replies to the request, indicating that the extended attribute was set.
func (r *SetxattrRequest) Respond() {
	buf := newBuffer(0)
	r.respond(buf)
}

// A LookupRequest asks to look up the given name in the directory named by r.Node.
type LookupRequest struct {
	Header `json:"-"`
	Name   string
}

var _ Request = (*LookupRequest)(nil)

func (r *LookupRequest) String() string {
	return fmt.Sprintf("Lookup [%s] %q", &r.Header, r.Name)
}

// Respond replies to the request with the given response.
func (r *LookupRequest) Respond(resp *LookupResponse) {
	size := entryOutSize(r.Header.Conn.proto)
	buf := newBuffer(size)
	out := (*entryOut)(buf.alloc(size))
	out.Nodeid = uint64(resp.Node)
	out.Generation = resp.Generation
	out.EntryValid = uint64(resp.EntryValid / time.Second)
	out.EntryValidNsec = uint32(resp.EntryValid % time.Second / time.Nanosecond)
	out.AttrValid = uint64(resp.Attr.Valid / time.Second)
	out.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	resp.Attr.attr(&out.Attr, r.Header.Conn.proto)
	r.respond(buf)
}

// A LookupResponse is the response to a LookupRequest.
type LookupResponse struct {
	Node       NodeID
	Generation uint64
	EntryValid time.Duration
	Attr       Attr
}

func (r *LookupResponse) string() string {
	return fmt.Sprintf("%v gen=%d valid=%v attr={%v}", r.Node, r.Generation, r.EntryValid, r.Attr)
}

func (r *LookupResponse) String() string {
	return fmt.Sprintf("Lookup %s", r.string())
}

// An OpenRequest asks to open a file or directory
type OpenRequest struct {
	Header    `json:"-"`
	Dir       bool // is this Opendir?
	Flags     OpenFlags
	OpenFlags OpenRequestFlags
}

var _ Request = (*OpenRequest)(nil)

func (r *OpenRequest) String() string {
	return fmt.Sprintf("Open [%s] dir=%v fl=%v", &r.Header, r.Dir, r.Flags)
}

// Respond replies to the request with the given response.
func (r *OpenRequest) Respond(resp *OpenResponse) {
	buf := newBuffer(unsafe.Sizeof(openOut{}))
	out := (*openOut)(buf.alloc(unsafe.Sizeof(openOut{})))
	out.Fh = uint64(resp.Handle)
	out.OpenFlags = uint32(resp.Flags)
	r.respond(buf)
}

// A OpenResponse is the response to a OpenRequest.
type OpenResponse struct {
	Handle HandleID
	Flags  OpenResponseFlags
}

func (r *OpenResponse) string() string {
	return fmt.Sprintf("%v fl=%v", r.Handle, r.Flags)
}

func (r *OpenResponse) String() string {
	return fmt.Sprintf("Open %s", r.string())
}

// A CreateRequest asks to create and open a file (not a directory).
type CreateRequest struct {
	Header `json:"-"`
	Name   string
	Flags  OpenFlags
	Mode   os.FileMode
	// Umask of the request.
	Umask os.FileMode
}

var _ Request = (*CreateRequest)(nil)

func (r *CreateRequest) String() string {
	return fmt.Sprintf("Create [%s] %q fl=%v mode=%v umask=%v", &r.Header, r.Name, r.Flags, r.Mode, r.Umask)
}

// Respond replies to the request with the given response.
func (r *CreateRequest) Respond(resp *CreateResponse) {
	eSize := entryOutSize(r.Header.Conn.proto)
	buf := newBuffer(eSize + unsafe.Sizeof(openOut{}))

	e := (*entryOut)(buf.alloc(eSize))
	e.Nodeid = uint64(resp.Node)
	e.Generation = resp.Generation
	e.EntryValid = uint64(resp.EntryValid / time.Second)
	e.EntryValidNsec = uint32(resp.EntryValid % time.Second / time.Nanosecond)
	e.AttrValid = uint64(resp.Attr.Valid / time.Second)
	e.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	resp.Attr.attr(&e.Attr, r.Header.Conn.proto)

	o := (*openOut)(buf.alloc(unsafe.Sizeof(openOut{})))
	o.Fh = uint64(resp.Handle)
	o.OpenFlags = uint32(resp.Flags)

	r.respond(buf)
}

// A CreateResponse is the response to a CreateRequest.
// It describes the created node and opened handle.
type CreateResponse struct {
	LookupResponse
	OpenResponse
}

func (r *CreateResponse) String() string {
	return fmt.Sprintf("Create {%s} {%s}", r.LookupResponse.string(), r.OpenResponse.string())
}

// A MkdirRequest asks to create (but not open) a directory.
type MkdirRequest struct {
	Header `json:"-"`
	Name   string
	Mode   os.FileMode
	// Umask of the request.
	Umask os.FileMode
}

var _ Request = (*MkdirRequest)(nil)

func (r *MkdirRequest) String() string {
	return fmt.Sprintf("Mkdir [%s] %q mode=%v umask=%v", &r.Header, r.Name, r.Mode, r.Umask)
}

// Respond replies to the request with the given response.
func (r *MkdirRequest) Respond(resp *MkdirResponse) {
	size := entryOutSize(r.Header.Conn.proto)
	buf := newBuffer(size)
	out := (*entryOut)(buf.alloc(size))
	out.Nodeid = uint64(resp.Node)
	out.Generation = resp.Generation
	out.EntryValid = uint64(resp.EntryValid / time.Second)
	out.EntryValidNsec = uint32(resp.EntryValid % time.Second / time.Nanosecond)
	out.AttrValid = uint64(resp.Attr.Valid / time.Second)
	out.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	resp.Attr.attr(&out.Attr, r.Header.Conn.proto)
	r.respond(buf)
}

// A MkdirResponse is the response to a MkdirRequest.
type MkdirResponse struct {
	LookupResponse
}

func (r *MkdirResponse) String() string {
	return fmt.Sprintf("Mkdir %v", r.LookupResponse.string())
}

// A ReadRequest asks to read from an open file.
type ReadRequest struct {
	Header    `json:"-"`
	Dir       bool // is this Readdir?
	Handle    HandleID
	Offset    int64
	Size      int
	Flags     ReadFlags
	LockOwner LockOwner
	FileFlags OpenFlags
}

var _ Request = (*ReadRequest)(nil)

func (r *ReadRequest) String() string {
	return fmt.Sprintf("Read [%s] %v %d @%#x dir=%v fl=%v owner=%v ffl=%v", &r.Header, r.Handle, r.Size, r.Offset, r.Dir, r.Flags, r.LockOwner, r.FileFlags)
}

// Respond replies to the request with the given response.
func (r *ReadRequest) Respond(resp *ReadResponse) {
	buf := newBuffer(uintptr(len(resp.Data)))
	buf = append(buf, resp.Data...)
	r.respond(buf)
}

// A ReadResponse is the response to a ReadRequest.
type ReadResponse struct {
	Data []byte
}

func (r *ReadResponse) String() string {
	return fmt.Sprintf("Read %d", len(r.Data))
}

type jsonReadResponse struct {
	Len uint64
}

func (r *ReadResponse) MarshalJSON() ([]byte, error) {
	j := jsonReadResponse{
		Len: uint64(len(r.Data)),
	}
	return json.Marshal(j)
}

// A ReleaseRequest asks to release (close) an open file handle.
type ReleaseRequest struct {
	Header       `json:"-"`
	Dir          bool // is this Releasedir?
	Handle       HandleID
	Flags        OpenFlags // flags from OpenRequest
	ReleaseFlags ReleaseFlags
	LockOwner    LockOwner
}

var _ Request = (*ReleaseRequest)(nil)

func (r *ReleaseRequest) String() string {
	return fmt.Sprintf("Release [%s] %v fl=%v rfl=%v owner=%v", &r.Header, r.Handle, r.Flags, r.ReleaseFlags, r.LockOwner)
}

// Respond replies to the request, indicating that the handle has been released.
func (r *ReleaseRequest) Respond() {
	buf := newBuffer(0)
	r.respond(buf)
}

// A DestroyRequest is sent by the kernel when unmounting the file system.
// No more requests will be received after this one, but it should still be
// responded to.
type DestroyRequest struct {
	Header `json:"-"`
}

var _ Request = (*DestroyRequest)(nil)

func (r *DestroyRequest) String() string {
	return fmt.Sprintf("Destroy [%s]", &r.Header)
}

// Respond replies to the request.
func (r *DestroyRequest) Respond() {
	buf := newBuffer(0)
	r.respond(buf)
}

// A ForgetRequest is sent by the kernel when forgetting about r.Node
// as returned by r.N lookup requests.
type ForgetRequest struct {
	Header `json:"-"`
	N      uint64
}

var _ Request = (*ForgetRequest)(nil)

func (r *ForgetRequest) String() string {
	return fmt.Sprintf("Forget [%s] %d", &r.Header, r.N)
}

// Respond replies to the request, indicating that the forgetfulness has been recorded.
func (r *ForgetRequest) Respond() {
	// Don't reply to forget messages.
	r.noResponse()
}

type BatchForgetItem struct {
	NodeID NodeID
	N      uint64
}

type BatchForgetRequest struct {
	Header `json:"-"`
	Forget []BatchForgetItem
}

var _ Request = (*BatchForgetRequest)(nil)

func (r *BatchForgetRequest) String() string {
	b := new(strings.Builder)
	fmt.Fprintf(b, "BatchForget [%s]", &r.Header)
	if len(r.Forget) == 0 {
		b.WriteString(" empty")
	} else {
		for _, item := range r.Forget {
			fmt.Fprintf(b, " %dx%d", item.NodeID, item.N)
		}
	}
	return b.String()
}

// Respond replies to the request, indicating that the forgetfulness has been recorded.
func (r *BatchForgetRequest) Respond() {
	// Don't reply to forget messages.
	r.noResponse()
}

// A Dirent represents a single directory entry.
type Dirent struct {
	// Inode this entry names.
	Inode uint64

	// Type of the entry, for example DT_File.
	//
	// Setting this is optional. The zero value (DT_Unknown) means
	// callers will just need to do a Getattr when the type is
	// needed. Providing a type can speed up operations
	// significantly.
	Type DirentType

	// Name of the entry
	Name string
}

// Type of an entry in a directory listing.
type DirentType uint32

const (
	// These don't quite match os.FileMode; especially there's an
	// explicit unknown, instead of zero value meaning file. They
	// are also not quite syscall.DT_*; nothing says the FUSE
	// protocol follows those, and even if they were, we don't
	// want each fs to fiddle with syscall.

	// The shift by 12 is hardcoded in the FUSE userspace
	// low-level C library, so it's safe here.

	DT_Unknown DirentType = 0
	DT_Socket  DirentType = syscall.S_IFSOCK >> 12
	DT_Link    DirentType = syscall.S_IFLNK >> 12
	DT_File    DirentType = syscall.S_IFREG >> 12
	DT_Block   DirentType = syscall.S_IFBLK >> 12
	DT_Dir     DirentType = syscall.S_IFDIR >> 12
	DT_Char    DirentType = syscall.S_IFCHR >> 12
	DT_FIFO    DirentType = syscall.S_IFIFO >> 12
)

func (t DirentType) String() string {
	switch t {
	case DT_Unknown:
		return "unknown"
	case DT_Socket:
		return "socket"
	case DT_Link:
		return "link"
	case DT_File:
		return "file"
	case DT_Block:
		return "block"
	case DT_Dir:
		return "dir"
	case DT_Char:
		return "char"
	case DT_FIFO:
		return "fifo"
	}
	return "invalid"
}

// AppendDirent appends the encoded form of a directory entry to data
// and returns the resulting slice.
func AppendDirent(data []byte, dir Dirent) []byte {
	de := dirent{
		Ino:     dir.Inode,
		Namelen: uint32(len(dir.Name)),
		Type:    uint32(dir.Type),
	}
	de.Off = uint64(len(data) + direntSize + (len(dir.Name)+7)&^7)
	data = append(data, (*[direntSize]byte)(unsafe.Pointer(&de))[:]...)
	data = append(data, dir.Name...)
	n := direntSize + uintptr(len(dir.Name))
	if n%8 != 0 {
		var pad [8]byte
		data = append(data, pad[:8-n%8]...)
	}
	return data
}

// A WriteRequest asks to write to an open file.
type WriteRequest struct {
	Header
	Handle    HandleID
	Offset    int64
	Data      []byte
	Flags     WriteFlags
	LockOwner LockOwner
	FileFlags OpenFlags
}

var _ Request = (*WriteRequest)(nil)

func (r *WriteRequest) String() string {
	return fmt.Sprintf("Write [%s] %v %d @%d fl=%v owner=%v ffl=%v", &r.Header, r.Handle, len(r.Data), r.Offset, r.Flags, r.LockOwner, r.FileFlags)
}

type jsonWriteRequest struct {
	Handle HandleID
	Offset int64
	Len    uint64
	Flags  WriteFlags
}

func (r *WriteRequest) MarshalJSON() ([]byte, error) {
	j := jsonWriteRequest{
		Handle: r.Handle,
		Offset: r.Offset,
		Len:    uint64(len(r.Data)),
		Flags:  r.Flags,
	}
	return json.Marshal(j)
}

// Respond replies to the request with the given response.
func (r *WriteRequest) Respond(resp *WriteResponse) {
	buf := newBuffer(unsafe.Sizeof(writeOut{}))
	out := (*writeOut)(buf.alloc(unsafe.Sizeof(writeOut{})))
	out.Size = uint32(resp.Size)
	r.respond(buf)
}

// A WriteResponse replies to a write indicating how many bytes were written.
type WriteResponse struct {
	Size int
}

func (r *WriteResponse) String() string {
	return fmt.Sprintf("Write %d", r.Size)
}

// A SetattrRequest asks to change one or more attributes associated with a file,
// as indicated by Valid.
type SetattrRequest struct {
	Header `json:"-"`
	Valid  SetattrValid
	Handle HandleID
	Size   uint64
	Atime  time.Time
	Mtime  time.Time
	Ctime  time.Time
	// Mode is the file mode to set (when valid).
	//
	// The type of the node (as in os.ModeType, os.ModeDir etc) is not
	// guaranteed to be sent by the kernel, in which case
	// os.ModeIrregular will be set.
	Mode os.FileMode
	Uid  uint32
	Gid  uint32
}

var _ Request = (*SetattrRequest)(nil)

func (r *SetattrRequest) String() string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Setattr [%s]", &r.Header)
	if r.Valid.Mode() {
		fmt.Fprintf(&buf, " mode=%v", r.Mode)
	}
	if r.Valid.Uid() {
		fmt.Fprintf(&buf, " uid=%d", r.Uid)
	}
	if r.Valid.Gid() {
		fmt.Fprintf(&buf, " gid=%d", r.Gid)
	}
	if r.Valid.Size() {
		fmt.Fprintf(&buf, " size=%d", r.Size)
	}
	if r.Valid.Atime() {
		fmt.Fprintf(&buf, " atime=%v", r.Atime)
	}
	if r.Valid.AtimeNow() {
		fmt.Fprintf(&buf, " atime=now")
	}
	if r.Valid.Mtime() {
		fmt.Fprintf(&buf, " mtime=%v", r.Mtime)
	}
	if r.Valid.MtimeNow() {
		fmt.Fprintf(&buf, " mtime=now")
	}
	if r.Valid.Handle() {
		fmt.Fprintf(&buf, " handle=%v", r.Handle)
	} else {
		fmt.Fprintf(&buf, " handle=INVALID-%v", r.Handle)
	}
	if r.Valid.LockOwner() {
		fmt.Fprintf(&buf, " lockowner")
	}
	return buf.String()
}

// Respond replies to the request with the given response,
// giving the updated attributes.
func (r *SetattrRequest) Respond(resp *SetattrResponse) {
	size := attrOutSize(r.Header.Conn.proto)
	buf := newBuffer(size)
	out := (*attrOut)(buf.alloc(size))
	out.AttrValid = uint64(resp.Attr.Valid / time.Second)
	out.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	resp.Attr.attr(&out.Attr, r.Header.Conn.proto)
	r.respond(buf)
}

// A SetattrResponse is the response to a SetattrRequest.
type SetattrResponse struct {
	Attr Attr // file attributes
}

func (r *SetattrResponse) String() string {
	return fmt.Sprintf("Setattr %v", r.Attr)
}

// A FlushRequest asks for the current state of an open file to be flushed
// to storage, as when a file descriptor is being closed.  A single opened Handle
// may receive multiple FlushRequests over its lifetime.
type FlushRequest struct {
	Header `json:"-"`
	Handle HandleID
	// Deprecated: Unused since 2006.
	Flags     uint32
	LockOwner LockOwner
}

var _ Request = (*FlushRequest)(nil)

func (r *FlushRequest) String() string {
	return fmt.Sprintf("Flush [%s] %v fl=%#x owner=%v", &r.Header, r.Handle, r.Flags, r.LockOwner)
}

// Respond replies to the request, indicating that the flush succeeded.
func (r *FlushRequest) Respond() {
	buf := newBuffer(0)
	r.respond(buf)
}

// A RemoveRequest asks to remove a file or directory from the
// directory r.Node.
type RemoveRequest struct {
	Header `json:"-"`
	Name   string // name of the entry to remove
	Dir    bool   // is this rmdir?
}

var _ Request = (*RemoveRequest)(nil)

func (r *RemoveRequest) String() string {
	return fmt.Sprintf("Remove [%s] %q dir=%v", &r.Header, r.Name, r.Dir)
}

// Respond replies to the request, indicating that the file was removed.
func (r *RemoveRequest) Respond() {
	buf := newBuffer(0)
	r.respond(buf)
}

// A SymlinkRequest is a request to create a symlink making NewName point to Target.
type SymlinkRequest struct {
	Header          `json:"-"`
	NewName, Target string
}

var _ Request = (*SymlinkRequest)(nil)

func (r *SymlinkRequest) String() string {
	return fmt.Sprintf("Symlink [%s] from %q to target %q", &r.Header, r.NewName, r.Target)
}

// Respond replies to the request, indicating that the symlink was created.
func (r *SymlinkRequest) Respond(resp *SymlinkResponse) {
	size := entryOutSize(r.Header.Conn.proto)
	buf := newBuffer(size)
	out := (*entryOut)(buf.alloc(size))
	out.Nodeid = uint64(resp.Node)
	out.Generation = resp.Generation
	out.EntryValid = uint64(resp.EntryValid / time.Second)
	out.EntryValidNsec = uint32(resp.EntryValid % time.Second / time.Nanosecond)
	out.AttrValid = uint64(resp.Attr.Valid / time.Second)
	out.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	resp.Attr.attr(&out.Attr, r.Header.Conn.proto)
	r.respond(buf)
}

// A SymlinkResponse is the response to a SymlinkRequest.
type SymlinkResponse struct {
	LookupResponse
}

func (r *SymlinkResponse) String() string {
	return fmt.Sprintf("Symlink %v", r.LookupResponse.string())
}

// A ReadlinkRequest is a request to read a symlink's target.
type ReadlinkRequest struct {
	Header `json:"-"`
}

var _ Request = (*ReadlinkRequest)(nil)

func (r *ReadlinkRequest) String() string {
	return fmt.Sprintf("Readlink [%s]", &r.Header)
}

func (r *ReadlinkRequest) Respond(target string) {
	buf := newBuffer(uintptr(len(target)))
	buf = append(buf, target...)
	r.respond(buf)
}

// A LinkRequest is a request to create a hard link.
type LinkRequest struct {
	Header  `json:"-"`
	OldNode NodeID
	NewName string
}

var _ Request = (*LinkRequest)(nil)

func (r *LinkRequest) String() string {
	return fmt.Sprintf("Link [%s] node %d to %q", &r.Header, r.OldNode, r.NewName)
}

func (r *LinkRequest) Respond(resp *LookupResponse) {
	size := entryOutSize(r.Header.Conn.proto)
	buf := newBuffer(size)
	out := (*entryOut)(buf.alloc(size))
	out.Nodeid = uint64(resp.Node)
	out.Generation = resp.Generation
	out.EntryValid = uint64(resp.EntryValid / time.Second)
	out.EntryValidNsec = uint32(resp.EntryValid % time.Second / time.Nanosecond)
	out.AttrValid = uint64(resp.Attr.Valid / time.Second)
	out.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	resp.Attr.attr(&out.Attr, r.Header.Conn.proto)
	r.respond(buf)
}

// A RenameRequest is a request to rename a file.
type RenameRequest struct {
	Header           `json:"-"`
	NewDir           NodeID
	OldName, NewName string
}

var _ Request = (*RenameRequest)(nil)

func (r *RenameRequest) String() string {
	return fmt.Sprintf("Rename [%s] from %q to dirnode %v %q", &r.Header, r.OldName, r.NewDir, r.NewName)
}

func (r *RenameRequest) Respond() {
	buf := newBuffer(0)
	r.respond(buf)
}

type MknodRequest struct {
	Header `json:"-"`
	Name   string
	Mode   os.FileMode
	Rdev   uint32
	// Umask of the request.
	Umask os.FileMode
}

var _ Request = (*MknodRequest)(nil)

func (r *MknodRequest) String() string {
	return fmt.Sprintf("Mknod [%s] Name %q mode=%v umask=%v rdev=%d", &r.Header, r.Name, r.Mode, r.Umask, r.Rdev)
}

func (r *MknodRequest) Respond(resp *LookupResponse) {
	size := entryOutSize(r.Header.Conn.proto)
	buf := newBuffer(size)
	out := (*entryOut)(buf.alloc(size))
	out.Nodeid = uint64(resp.Node)
	out.Generation = resp.Generation
	out.EntryValid = uint64(resp.EntryValid / time.Second)
	out.EntryValidNsec = uint32(resp.EntryValid % time.Second / time.Nanosecond)
	out.AttrValid = uint64(resp.Attr.Valid / time.Second)
	out.AttrValidNsec = uint32(resp.Attr.Valid % time.Second / time.Nanosecond)
	resp.Attr.attr(&out.Attr, r.Header.Conn.proto)
	r.respond(buf)
}

type FsyncRequest struct {
	Header `json:"-"`
	Handle HandleID
	// TODO bit 1 is datasync, not well documented upstream
	Flags uint32
	Dir   bool
}

var _ Request = (*FsyncRequest)(nil)

func (r *FsyncRequest) String() string {
	return fmt.Sprintf("Fsync [%s] Handle %v Flags %v", &r.Header, r.Handle, r.Flags)
}

func (r *FsyncRequest) Respond() {
	buf := newBuffer(0)
	r.respond(buf)
}

// An InterruptRequest is a request to interrupt another pending request. The
// response to that request should return an error status of EINTR.
type InterruptRequest struct {
	Header `json:"-"`
	IntrID RequestID // ID of the request to be interrupt.
}

var _ Request = (*InterruptRequest)(nil)

func (r *InterruptRequest) Respond() {
	// nothing to do here
	r.noResponse()
}

func (r *InterruptRequest) String() string {
	return fmt.Sprintf("Interrupt [%s] ID %v", &r.Header, r.IntrID)
}

// NotifyReply is a response to an earlier notification. It behaves
// like a Request, but is not really a request expecting a response.
type NotifyReply struct {
	Header `json:"-"`
	msg    *message
}

var _ Request = (*NotifyReply)(nil)

func (r *NotifyReply) String() string {
	return fmt.Sprintf("NotifyReply [%s]", &r.Header)
}

type PollRequest struct {
	Header `json:"-"`
	Handle HandleID
	kh     uint64
	Flags  PollFlags
	// Events is a bitmap of events of interest.
	//
	// This field is only set for FUSE protocol 7.21 and later.
	Events PollEvents
}

var _ Request = (*PollRequest)(nil)

func (r *PollRequest) String() string {
	return fmt.Sprintf("Poll [%s] %v kh=%v fl=%v ev=%v", &r.Header, r.Handle, r.kh, r.Flags, r.Events)
}

type PollWakeup struct {
	kh uint64
}

func (p PollWakeup) String() string {
	return fmt.Sprintf("PollWakeup{kh=%d}", p.kh)
}

// Wakeup returns information that can be used later to wake up file
// system clients polling a Handle or a Node.
//
// ok is false if wakeups are not requested for this poll.
//
// Do not retain PollWakeup past the lifetime of the Handle or Node.
func (r *PollRequest) Wakeup() (_ PollWakeup, ok bool) {
	if r.Flags&PollScheduleNotify == 0 {
		return PollWakeup{}, false
	}
	p := PollWakeup{
		kh: r.kh,
	}
	return p, true
}

func (r *PollRequest) Respond(resp *PollResponse) {
	buf := newBuffer(unsafe.Sizeof(pollOut{}))
	out := (*pollOut)(buf.alloc(unsafe.Sizeof(pollOut{})))
	out.REvents = uint32(resp.REvents)
	r.respond(buf)
}

type PollResponse struct {
	REvents PollEvents
}

func (r *PollResponse) String() string {
	return fmt.Sprintf("Poll revents=%v", r.REvents)
}

type FileLock struct {
	Start uint64
	End   uint64
	Type  LockType
	PID   int32
}

// LockRequest asks to try acquire a byte range lock on a node. The
// response should be immediate, do not wait to obtain lock.
//
// Unlocking can be
//
//   - explicit with UnlockRequest
//   - for flock: implicit on final close (ReleaseRequest.ReleaseFlags
//     has ReleaseFlockUnlock set)
//   - for POSIX locks: implicit on any close (FlushRequest)
//   - for Open File Description locks: implicit on final close
//     (no LockOwner observed as of 2020-04)
//
// See LockFlags to know which kind of a lock is being requested. (As
// of 2020-04, Open File Descriptor locks are indistinguishable from
// POSIX. This means programs using those locks will likely misbehave
// when closing FDs on FUSE-based distributed filesystems, as the
// filesystem has no better knowledge than to follow POSIX locking
// rules and release the global lock too early.)
//
// Most of the other differences between flock (BSD) and POSIX (fcntl
// F_SETLK) locks are relevant only to the caller, not the filesystem.
// FUSE always sees ranges, and treats flock whole-file locks as
// requests for the maximum byte range. Filesystems should do the
// same, as this provides a forwards compatibility path to
// Linux-native Open file description locks.
//
// To enable locking events in FUSE, pass LockingFlock() and/or
// LockingPOSIX() to Mount.
//
// See also LockWaitRequest.
type LockRequest struct {
	Header
	Handle HandleID
	// LockOwner is a unique identifier for the originating client, to
	// identify locks.
	LockOwner LockOwner
	Lock      FileLock
	LockFlags LockFlags
}

var _ Request = (*LockRequest)(nil)

func (r *LockRequest) String() string {
	return fmt.Sprintf("Lock [%s] %v owner=%v range=%d..%d type=%v pid=%v fl=%v", &r.Header, r.Handle, r.LockOwner, r.Lock.Start, r.Lock.End, r.Lock.Type, r.Lock.PID, r.LockFlags)
}

func (r *LockRequest) Respond() {
	buf := newBuffer(0)
	r.respond(buf)
}

// LockWaitRequest asks to acquire a byte range lock on a node,
// delaying response until lock can be obtained (or the request is
// interrupted).
//
// See LockRequest. LockWaitRequest can be converted to a LockRequest.
type LockWaitRequest LockRequest

var _ LockRequest = LockRequest(LockWaitRequest{})

var _ Request = (*LockWaitRequest)(nil)

func (r *LockWaitRequest) String() string {
	return fmt.Sprintf("LockWait [%s] %v owner=%v range=%d..%d type=%v pid=%v fl=%v", &r.Header, r.Handle, r.LockOwner, r.Lock.Start, r.Lock.End, r.Lock.Type, r.Lock.PID, r.LockFlags)
}

func (r *LockWaitRequest) Respond() {
	buf := newBuffer(0)
	r.respond(buf)
}

// UnlockRequest asks to release a lock on a byte range on a node.
//
// UnlockRequests always have Lock.Type == LockUnlock.
//
// See LockRequest. UnlockRequest can be converted to a LockRequest.
type UnlockRequest LockRequest

var _ LockRequest = LockRequest(UnlockRequest{})

var _ Request = (*UnlockRequest)(nil)

func (r *UnlockRequest) String() string {
	return fmt.Sprintf("Unlock [%s] %v owner=%v range=%d..%d type=%v pid=%v fl=%v", &r.Header, r.Handle, r.LockOwner, r.Lock.Start, r.Lock.End, r.Lock.Type, r.Lock.PID, r.LockFlags)
}

func (r *UnlockRequest) Respond() {
	buf := newBuffer(0)
	r.respond(buf)
}

// QueryLockRequest queries the lock status.
//
// If the lock could be placed, set response Lock.Type to
// unix.F_UNLCK.
//
// If there are conflicting locks, the response should describe one of
// them. For Open File Description locks, set PID to -1. (This is
// probably also the sane behavior for locks held by remote parties.)
type QueryLockRequest struct {
	Header
	Handle    HandleID
	LockOwner LockOwner
	Lock      FileLock
	LockFlags LockFlags
}

var _ Request = (*QueryLockRequest)(nil)

func (r *QueryLockRequest) String() string {
	return fmt.Sprintf("QueryLock [%s] %v owner=%v range=%d..%d type=%v pid=%v fl=%v", &r.Header, r.Handle, r.LockOwner, r.Lock.Start, r.Lock.End, r.Lock.Type, r.Lock.PID, r.LockFlags)
}

// Respond replies to the request with the given response.
func (r *QueryLockRequest) Respond(resp *QueryLockResponse) {
	buf := newBuffer(unsafe.Sizeof(lkOut{}))
	out := (*lkOut)(buf.alloc(unsafe.Sizeof(lkOut{})))
	out.Lk = fileLock{
		Start: resp.Lock.Start,
		End:   resp.Lock.End,
		Type:  uint32(resp.Lock.Type),
		PID:   uint32(resp.Lock.PID),
	}
	r.respond(buf)
}

type QueryLockResponse struct {
	Lock FileLock
}

func (r *QueryLockResponse) String() string {
	return fmt.Sprintf("QueryLock range=%d..%d type=%v pid=%v", r.Lock.Start, r.Lock.End, r.Lock.Type, r.Lock.PID)
}

// FAllocateRequest  manipulates space reserved for the file.
//
// Note that the kernel limits what modes are acceptable in any FUSE filesystem.
type FAllocateRequest struct {
	Header
	Handle HandleID
	Offset uint64
	Length uint64
	Mode   FAllocateFlags
}

var _ Request = (*FAllocateRequest)(nil)

func (r *FAllocateRequest) String() string {
	return fmt.Sprintf("FAllocate [%s] %v %d @%d mode=%s", &r.Header, r.Handle,
		r.Length, r.Offset, r.Mode)
}

// Respond replies to the request, indicating that the FAllocate succeeded.
func (r *FAllocateRequest) Respond() {
	buf := newBuffer(0)
	r.respond(buf)
}
