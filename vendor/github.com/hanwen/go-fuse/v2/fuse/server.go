// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

import (
	"fmt"
	"log"
	"math"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	// Linux v4.20+ caps requests at 1 MiB. Older kernels at 128 kiB.
	MAX_KERNEL_WRITE = 1024 * 1024

	// Linux kernel constant from include/uapi/linux/fuse.h
	// Reads from /dev/fuse that are smaller fail with EINVAL.
	_FUSE_MIN_READ_BUFFER = 8192

	// defaultMaxWrite is the default value for MountOptions.MaxWrite
	defaultMaxWrite = 128 * 1024 // 128 kiB

	minMaxReaders = 2
	maxMaxReaders = 16
)

// Server contains the logic for reading from the FUSE device and
// translating it to RawFileSystem interface calls.
type Server struct {
	protocolServer

	// Empty if unmounted.
	mountPoint string

	// writeMu serializes close and notify writes
	writeMu sync.Mutex

	// I/O with kernel and daemon.
	mountFd int

	opts *MountOptions

	// maxReaders is the maximum number of goroutines reading requests
	maxReaders int

	// Pools for []byte
	buffers bufferPool

	// Pool for request structs.
	reqPool sync.Pool

	// Pool for raw requests data
	readPool   sync.Pool
	reqMu      sync.Mutex
	reqReaders int

	singleReader bool
	canSplice    bool
	loops        sync.WaitGroup
	serving      bool // for preventing duplicate Serve() calls

	// Used to implement WaitMount on macos.
	ready chan error

	// for implementing single threaded processing.
	requestProcessingMu sync.Mutex
}

// SetDebug is deprecated. Use MountOptions.Debug instead.
func (ms *Server) SetDebug(dbg bool) {
	// This will typically trigger the race detector.
	ms.opts.Debug = dbg
}

// KernelSettings returns the Init message from the kernel, so
// filesystems can adapt to availability of features of the kernel
// driver. The message should not be altered.
func (ms *Server) KernelSettings() *InitIn {
	s := ms.kernelSettings

	return &s
}

const _MAX_NAME_LEN = 20

// This type may be provided for recording latencies of each FUSE
// operation.
type LatencyMap interface {
	Add(name string, dt time.Duration)
}

// RecordLatencies switches on collection of timing for each request
// coming from the kernel.P assing a nil argument switches off the
func (ms *Server) RecordLatencies(l LatencyMap) {
	ms.latencies = l
}

// Unmount calls fusermount -u on the mount. This has the effect of
// shutting down the filesystem. After the Server is unmounted, it
// should be discarded.  This function is idempotent.
//
// Does not work when we were mounted with the magic /dev/fd/N mountpoint syntax,
// as we do not know the real mountpoint. Unmount using
//
//	fusermount -u /path/to/real/mountpoint
//
// in this case.
func (ms *Server) Unmount() (err error) {
	if ms.mountPoint == "" {
		return nil
	}
	if parseFuseFd(ms.mountPoint) >= 0 {
		return fmt.Errorf("Cannot unmount magic mountpoint %q. Please use `fusermount -u REALMOUNTPOINT` instead.", ms.mountPoint)
	}
	delay := time.Duration(0)
	for try := 0; try < 5; try++ {
		err = unmount(ms.mountPoint, ms.opts)
		if err == nil {
			break
		}

		// Sleep for a bit. This is not pretty, but there is
		// no way we can be certain that the kernel thinks all
		// open files have already been closed.
		delay = 2*delay + 5*time.Millisecond
		time.Sleep(delay)
	}
	if err != nil {
		return
	}
	// Wait for event loops to exit.
	ms.loops.Wait()
	ms.mountPoint = ""
	return err
}

// alignSlice ensures that the byte at alignedByte is aligned with the
// given logical block size.  The input slice should be at least (size
// + blockSize)
func alignSlice(buf []byte, alignedByte, blockSize, size uintptr) []byte {
	misaligned := uintptr(unsafe.Pointer(&buf[alignedByte])) & (blockSize - 1)
	buf = buf[blockSize-misaligned:]
	return buf[:size]
}

// NewServer creates a FUSE server and attaches ("mounts") it to the
// `mountPoint` directory.
//
// See the "Mount styles" section in the package documentation if you want to
// know about the inner workings of the mount process. Usually you do not.
func NewServer(fs RawFileSystem, mountPoint string, opts *MountOptions) (*Server, error) {
	if opts == nil {
		opts = &MountOptions{
			MaxBackground: _DEFAULT_BACKGROUND_TASKS,
		}
	}
	o := *opts
	if o.Logger == nil {
		o.Logger = log.Default()
	}
	if o.MaxWrite < 0 {
		o.MaxWrite = 0
	}
	if o.MaxWrite == 0 {
		o.MaxWrite = defaultMaxWrite
	}
	if o.MaxWrite > MAX_KERNEL_WRITE {
		o.MaxWrite = MAX_KERNEL_WRITE
	}
	if o.MaxStackDepth == 0 {
		o.MaxStackDepth = 1
	}
	if o.Name == "" {
		name := fs.String()
		l := len(name)
		if l > _MAX_NAME_LEN {
			l = _MAX_NAME_LEN
		}
		o.Name = strings.Replace(name[:l], ",", ";", -1)
	}

	maxReaders := runtime.GOMAXPROCS(0)
	if maxReaders < minMaxReaders {
		maxReaders = minMaxReaders
	} else if maxReaders > maxMaxReaders {
		maxReaders = maxMaxReaders
	}

	ms := &Server{
		protocolServer: protocolServer{
			fileSystem:  fs,
			retrieveTab: make(map[uint64]*retrieveCacheRequest),
			opts:        &o,
		},
		opts:         &o,
		maxReaders:   maxReaders,
		singleReader: useSingleReader,
		ready:        make(chan error, 1),
	}
	ms.reqPool.New = func() interface{} {
		return &requestAlloc{
			request: request{
				cancel: make(chan struct{}),
			},
		}
	}
	ms.readPool.New = func() interface{} {
		targetSize := o.MaxWrite + int(maxInputSize)
		if targetSize < _FUSE_MIN_READ_BUFFER {
			targetSize = _FUSE_MIN_READ_BUFFER
		}
		// O_DIRECT typically requires buffers aligned to
		// blocksize (see man 2 open), but requirements vary
		// across file systems. Presumably, we could also fix
		// this by reading the requests using readv.
		buf := make([]byte, targetSize+logicalBlockSize)
		buf = alignSlice(buf, unsafe.Sizeof(WriteIn{}), logicalBlockSize, uintptr(targetSize))
		return buf
	}
	mountPoint = filepath.Clean(mountPoint)
	if !filepath.IsAbs(mountPoint) {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		mountPoint = filepath.Clean(filepath.Join(cwd, mountPoint))
	}
	fd, err := mount(mountPoint, &o, ms.ready)
	if err != nil {
		return nil, err
	}

	ms.mountPoint = mountPoint
	ms.mountFd = fd

	if code := ms.handleInit(); !code.Ok() {
		syscall.Close(fd)
		// TODO - unmount as well?
		return nil, fmt.Errorf("init: %s", code)
	}

	// This prepares for Serve being called somewhere, either
	// synchronously or asynchronously.
	ms.loops.Add(1)
	return ms, nil
}

func escape(optionValue string) string {
	return strings.Replace(strings.Replace(optionValue, `\`, `\\`, -1), `,`, `\,`, -1)
}

func (o *MountOptions) optionsStrings() []string {
	var r []string
	r = append(r, o.Options...)

	if o.AllowOther {
		r = append(r, "allow_other")
	}
	if o.FsName != "" {
		r = append(r, "fsname="+o.FsName)
	}
	if o.Name != "" {
		r = append(r, "subtype="+o.Name)
	}
	r = append(r, fmt.Sprintf("max_read=%d", o.MaxWrite))

	// OSXFUSE applies a 60-second timeout for file operations. This
	// is inconsistent with how FUSE works on Linux, where operations
	// last as long as the daemon is willing to let them run.
	if runtime.GOOS == "darwin" {
		r = append(r, "daemon_timeout=0")
	}
	if o.IDMappedMount && !o.containsOption("default_permissions") {
		r = append(r, "default_permissions")
	}

	// Commas and backslashs in an option need to be escaped, because
	// options are separated by a comma and backslashs are used to
	// escape other characters.
	var rEscaped []string
	for _, s := range r {
		rEscaped = append(rEscaped, escape(s))
	}

	return rEscaped
}

func (o *MountOptions) containsOption(opt string) bool {
	for _, o := range o.Options {
		if o == opt {
			return true
		}
	}
	return false
}

// DebugData returns internal status information for debugging
// purposes.
func (ms *Server) DebugData() string {
	var r int
	ms.reqMu.Lock()
	r = ms.reqReaders
	ms.reqMu.Unlock()

	return fmt.Sprintf("readers: %d", r)
}

// handleEINTR retries the given function until it doesn't return syscall.EINTR.
// This is similar to the HANDLE_EINTR() macro from Chromium ( see
// https://code.google.com/p/chromium/codesearch#chromium/src/base/posix/eintr_wrapper.h
// ) and the TEMP_FAILURE_RETRY() from glibc (see
// https://www.gnu.org/software/libc/manual/html_node/Interrupted-Primitives.html
// ).
//
// Don't use handleEINTR() with syscall.Close(); see
// https://code.google.com/p/chromium/issues/detail?id=269623 .
func handleEINTR(fn func() error) (err error) {
	for {
		err = fn()
		if err != syscall.EINTR {
			break
		}
	}
	return
}

// Returns a new request, or error. Returns
// nil, OK if we have too many readers already.
func (ms *Server) readRequest() (req *requestAlloc, code Status) {
	ms.reqMu.Lock()
	if ms.reqReaders > ms.maxReaders {
		ms.reqMu.Unlock()
		return nil, OK
	}
	ms.reqReaders++
	ms.reqMu.Unlock()

	reqIface := ms.reqPool.Get()
	req = reqIface.(*requestAlloc)
	destIface := ms.readPool.Get()
	dest := destIface.([]byte)

	var n int
	err := handleEINTR(func() error {
		var err error
		n, err = syscall.Read(ms.mountFd, dest)
		return err
	})
	if err != nil {
		code = ToStatus(err)
		ms.reqPool.Put(reqIface)
		ms.reqMu.Lock()
		ms.reqReaders--
		ms.reqMu.Unlock()
		return nil, code
	}

	if ms.latencies != nil {
		req.startTime = time.Now()
	}
	ms.reqMu.Lock()
	defer ms.reqMu.Unlock()
	gobbled := req.setInput(dest[:n])
	if len(req.inputBuf) < int(unsafe.Sizeof(InHeader{})) {
		log.Printf("Short read for input header: %v", req.inputBuf)
		return nil, EINVAL
	}
	opCode := ((*InHeader)(unsafe.Pointer(&req.inputBuf[0]))).Opcode
	/* These messages don't expect reply, so they cost nothing for
	   the kernel to send. Make sure we're not overwhelmed by not
	   spawning a new reader.
	*/
	needsBackPressure := (opCode == _OP_FORGET || opCode == _OP_BATCH_FORGET)

	if !gobbled {
		ms.readPool.Put(destIface)
	}
	ms.reqReaders--
	if !ms.singleReader && ms.reqReaders <= 0 && !needsBackPressure {
		ms.loops.Add(1)
		go ms.loop()
	}

	return req, OK
}

// returnRequest returns a request to the pool of unused requests.
func (ms *Server) returnRequest(req *requestAlloc) {
	ms.recordStats(&req.request)

	if req.bufferPoolOutputBuf != nil {
		ms.buffers.FreeBuffer(req.bufferPoolOutputBuf)
		req.bufferPoolOutputBuf = nil
	}
	if req.interrupted {
		req.interrupted = false
		req.cancel = make(chan struct{}, 0)
	}
	req.clear()

	if p := req.bufferPoolInputBuf; p != nil {
		req.bufferPoolInputBuf = nil
		ms.readPool.Put(p)
	}
	ms.reqPool.Put(req)
}

func (ms *Server) recordStats(req *request) {
	if ms.latencies != nil {
		dt := time.Now().Sub(req.startTime)
		opname := operationName(req.inHeader().Opcode)
		ms.latencies.Add(opname, dt)
	}
}

// Serve initiates the FUSE loop. Normally, callers should run Serve()
// and wait for it to exit, but tests will want to run this in a
// goroutine.
//
// Each filesystem operation executes in a separate goroutine.
func (ms *Server) Serve() {
	if ms.serving {
		// Calling Serve() multiple times leads to a panic on unmount and fun
		// debugging sessions ( https://github.com/hanwen/go-fuse/issues/512 ).
		// Catch it early.
		log.Panic("Serve() must only be called once, you have called it a second time")
	}
	ms.serving = true

	ms.loop()
	ms.loops.Wait()

	ms.writeMu.Lock()
	syscall.Close(ms.mountFd)
	ms.writeMu.Unlock()

	// shutdown in-flight cache retrieves.
	//
	// It is possible that umount comes in the middle - after retrieve
	// request was sent to kernel, but corresponding kernel reply has not
	// yet been read. We unblock all such readers and wake them up with ENODEV.
	ms.retrieveMu.Lock()
	rtab := ms.retrieveTab
	// retrieve attempts might be erroneously tried even after close
	// we have to keep retrieveTab !nil not to panic.
	ms.retrieveTab = make(map[uint64]*retrieveCacheRequest)
	ms.retrieveMu.Unlock()
	for _, reading := range rtab {
		reading.n = 0
		reading.st = ENODEV
		close(reading.ready)
	}

	ms.fileSystem.OnUnmount()
}

// Wait waits for the serve loop to exit. This should only be called
// after Serve has been called, or it will hang indefinitely.
func (ms *Server) Wait() {
	ms.loops.Wait()
}

func (ms *Server) handleInit() Status {
	// The first request should be INIT; read it synchronously,
	// and don't spawn new readers.
	orig := ms.singleReader
	ms.singleReader = true
	req, errNo := ms.readRequest()
	ms.singleReader = orig

	if errNo != OK || req == nil {
		return errNo
	}
	if code := ms.handleRequest(req); !code.Ok() {
		return code
	}

	if ms.kernelSettings.Minor >= 13 {
		ms.setSplice()
	}

	// INIT is handled. Init the file system, but don't accept
	// incoming requests, so the file system can setup itself.
	ms.fileSystem.Init(ms)
	return OK
}

// loop is the FUSE event loop. The simplistic way of calling this is
// with singleReader=true, which has a single goroutine reading the
// device, and spawning a new goroutine for each request. It is
// however 2x slower than processing the request inline with the
// reader. The latter requires more logic, because whenever we start
// processing the request, we have to make sure a new routine is
// spawned to read the device.
//
// Benchmark results i5-8350 pinned at 2Ghz:
//
// singleReader = true
//
// BenchmarkGoFuseRead            	     954	   1137408 ns/op	1843.80 MB/s	    5459 B/op	     173 allocs/op
// BenchmarkGoFuseRead-2          	    1327	    798635 ns/op	2625.92 MB/s	    5072 B/op	     169 allocs/op
// BenchmarkGoFuseStat            	    1530	    750944 ns/op
// BenchmarkGoFuseStat-2          	    8455	    120091 ns/op
// BenchmarkGoFuseReaddir         	     741	   1561004 ns/op
// BenchmarkGoFuseReaddir-2       	    2508	    599821 ns/op
//
// singleReader = false
//
// BenchmarkGoFuseRead            	    1890	    671576 ns/op	3122.73 MB/s	    5393 B/op	     136 allocs/op
// BenchmarkGoFuseRead-2          	    2948	    429578 ns/op	4881.88 MB/s	   32235 B/op	     157 allocs/op
// BenchmarkGoFuseStat            	    7886	    153546 ns/op
// BenchmarkGoFuseStat-2          	    9310	    121332 ns/op
// BenchmarkGoFuseReaddir         	    4074	    361568 ns/op
// BenchmarkGoFuseReaddir-2       	    3511	    319765 ns/op
func (ms *Server) loop() {
	defer ms.loops.Done()
exit:
	for {
		req, errNo := ms.readRequest()
		switch errNo {
		case OK:
			if req == nil {
				break exit
			}
		case ENOENT:
			continue
		case ENODEV:
			// Mount was killed. The obvious place to
			// cancel outstanding requests is at the end
			// of Serve, but that reader might be blocked.
			ms.cancelAll()
			if ms.opts.Debug {
				ms.opts.Logger.Printf("received ENODEV (unmount request), thread exiting")
			}
			break exit
		default: // some other error?
			ms.opts.Logger.Printf("Failed to read from fuse conn: %v", errNo)
			break exit
		}

		if ms.singleReader {
			go ms.handleRequest(req)
		} else {
			ms.handleRequest(req)
		}
	}
}

func (ms *Server) handleRequest(req *requestAlloc) Status {
	defer ms.returnRequest(req)
	if ms.opts.SingleThreaded {
		ms.requestProcessingMu.Lock()
		defer ms.requestProcessingMu.Unlock()
	}

	h, inSize, outSize, outPayloadSize, code := parseRequest(req.inputBuf, &ms.kernelSettings)
	if !code.Ok() {
		ms.opts.Logger.Printf("parseRequest: %v", code)
		return code
	}

	req.inPayload = req.inputBuf[inSize:]
	req.inputBuf = req.inputBuf[:inSize]
	req.outputBuf = req.outBuf[:outSize+int(sizeOfOutHeader)]
	copy(req.outputBuf, zeroOutBuf[:])
	if outPayloadSize > 0 {
		req.outPayload = ms.buffers.AllocBuffer(uint32(outPayloadSize))
		req.bufferPoolOutputBuf = req.outPayload
	}
	ms.protocolServer.handleRequest(h, &req.request)
	if req.suppressReply {
		return OK
	}
	errno := ms.write(&req.request)
	if errno != 0 {
		// Ignore ENOENT for INTERRUPT responses which
		// indicates that the referred request is no longer
		// known by the kernel. This is a normal if the
		// referred request already has completed.
		//
		// Ignore ENOENT for RELEASE responses.  When the FS
		// is unmounted directly after a file close, the
		// device can go away while we are still processing
		// RELEASE. This is because RELEASE is analogous to
		// FORGET, and is not synchronized with the calling
		// process, but does require a response.
		if ms.opts.Debug || !(errno == ENOENT && (req.inHeader().Opcode == _OP_INTERRUPT ||
			req.inHeader().Opcode == _OP_RELEASEDIR ||
			req.inHeader().Opcode == _OP_RELEASE)) {
			ms.opts.Logger.Printf("writer: Write/Writev failed, err: %v. opcode: %v",
				errno, operationName(req.inHeader().Opcode))
		}
	}
	return errno
}

func (ms *Server) notifyWrite(req *request) Status {
	req.serializeHeader(req.outPayloadSize())

	if ms.opts.Debug {
		ms.opts.Logger.Println(req.OutputDebug())
	}

	// Protect against concurrent close.
	ms.writeMu.Lock()
	result := ms.write(req)
	ms.writeMu.Unlock()

	if ms.opts.Debug {
		h := getHandler(req.inHeader().Opcode)
		ms.opts.Logger.Printf("Response %s: %v", h.Name, result)
	}
	return result
}

func newNotifyRequest(opcode uint32) *request {
	r := &request{
		inputBuf:  make([]byte, unsafe.Sizeof(InHeader{})),
		outputBuf: make([]byte, sizeOfOutHeader+getHandler(opcode).OutputSize),
		status: map[uint32]Status{
			_OP_NOTIFY_INVAL_INODE:    NOTIFY_INVAL_INODE,
			_OP_NOTIFY_INVAL_ENTRY:    NOTIFY_INVAL_ENTRY,
			_OP_NOTIFY_STORE_CACHE:    NOTIFY_STORE_CACHE,
			_OP_NOTIFY_RETRIEVE_CACHE: NOTIFY_RETRIEVE_CACHE,
			_OP_NOTIFY_DELETE:         NOTIFY_DELETE,
		}[opcode],
	}
	r.inHeader().Opcode = opcode
	return r
}

// InodeNotify invalidates the information associated with the inode
// (ie. data cache, attributes, etc.)
func (ms *Server) InodeNotify(node uint64, off int64, length int64) Status {
	if !ms.kernelSettings.SupportsNotify(NOTIFY_INVAL_INODE) {
		return ENOSYS
	}

	req := newNotifyRequest(_OP_NOTIFY_INVAL_INODE)

	entry := (*NotifyInvalInodeOut)(req.outData())
	entry.Ino = node
	entry.Off = off
	entry.Length = length

	return ms.notifyWrite(req)
}

// InodeNotifyStoreCache tells kernel to store data into inode's cache.
//
// This call is similar to InodeNotify, but instead of only invalidating a data
// region, it gives updated data directly to the kernel.
func (ms *Server) InodeNotifyStoreCache(node uint64, offset int64, data []byte) Status {
	if !ms.kernelSettings.SupportsNotify(NOTIFY_STORE_CACHE) {
		return ENOSYS
	}

	for len(data) > 0 {
		size := len(data)
		if size > math.MaxInt32 {
			// NotifyStoreOut has only uint32 for size.
			// we check for max(int32), not max(uint32), because on 32-bit
			// platforms int has only 31-bit for positive range.
			size = math.MaxInt32
		}

		st := ms.inodeNotifyStoreCache32(node, offset, data[:size])
		if st != OK {
			return st
		}

		data = data[size:]
		offset += int64(size)
	}

	return OK
}

// inodeNotifyStoreCache32 is internal worker for InodeNotifyStoreCache which
// handles data chunks not larger than 2GB.
func (ms *Server) inodeNotifyStoreCache32(node uint64, offset int64, data []byte) Status {
	req := newNotifyRequest(_OP_NOTIFY_STORE_CACHE)

	store := (*NotifyStoreOut)(req.outData())
	store.Nodeid = node
	store.Offset = uint64(offset) // NOTE not int64, as it is e.g. in NotifyInvalInodeOut
	store.Size = uint32(len(data))

	req.outPayload = data

	return ms.notifyWrite(req)
}

// InodeRetrieveCache retrieves data from kernel's inode cache.
//
// InodeRetrieveCache asks kernel to return data from its cache for inode at
// [offset:offset+len(dest)) and waits for corresponding reply. If kernel cache
// has fewer consecutive data starting at offset, that fewer amount is returned.
// In particular if inode data at offset is not cached (0, OK) is returned.
//
// The kernel returns ENOENT if it does not currently have entry for this inode
// in its dentry cache.
func (ms *Server) InodeRetrieveCache(node uint64, offset int64, dest []byte) (n int, st Status) {
	// the kernel won't send us in one go more then what we negotiated as MaxWrite.
	// retrieve the data in chunks.
	// TODO spawn some number of readahead retrievers in parallel.
	ntotal := 0
	for {
		chunkSize := len(dest)
		if chunkSize > ms.opts.MaxWrite {
			chunkSize = ms.opts.MaxWrite
		}
		n, st = ms.inodeRetrieveCache1(node, offset, dest[:chunkSize])
		if st != OK || n == 0 {
			break
		}

		ntotal += n
		offset += int64(n)
		dest = dest[n:]
	}

	// if we could retrieve at least something - it is ok.
	// if ntotal=0 - st will be st returned from first inodeRetrieveCache1.
	if ntotal > 0 {
		st = OK
	}
	return ntotal, st
}

// inodeRetrieveCache1 is internal worker for InodeRetrieveCache which
// actually talks to kernel and retrieves chunks not larger than ms.opts.MaxWrite.
func (ms *Server) inodeRetrieveCache1(node uint64, offset int64, dest []byte) (n int, st Status) {
	if !ms.kernelSettings.SupportsNotify(NOTIFY_RETRIEVE_CACHE) {
		return 0, ENOSYS
	}

	req := newNotifyRequest(_OP_NOTIFY_RETRIEVE_CACHE)

	// retrieve up to 2GB not to overflow uint32 size in NotifyRetrieveOut.
	// see InodeNotifyStoreCache in similar place for why it is only 2GB, not 4GB.
	//
	// ( InodeRetrieveCache calls us with chunks not larger than
	//   ms.opts.MaxWrite, but MaxWrite is int, so let's be extra cautious )
	size := len(dest)
	if size > math.MaxInt32 {
		size = math.MaxInt32
	}
	dest = dest[:size]

	q := (*NotifyRetrieveOut)(req.outData())
	q.Nodeid = node
	q.Offset = uint64(offset) // not int64, as it is e.g. in NotifyInvalInodeOut
	q.Size = uint32(len(dest))

	reading := &retrieveCacheRequest{
		nodeid: q.Nodeid,
		offset: q.Offset,
		dest:   dest,
		ready:  make(chan struct{}),
	}

	ms.retrieveMu.Lock()
	q.NotifyUnique = ms.retrieveNext
	ms.retrieveNext++
	ms.retrieveTab[q.NotifyUnique] = reading
	ms.retrieveMu.Unlock()

	// Protect against concurrent close.
	result := ms.notifyWrite(req)
	if result != OK {
		ms.retrieveMu.Lock()
		r := ms.retrieveTab[q.NotifyUnique]
		if r == reading {
			delete(ms.retrieveTab, q.NotifyUnique)
		} else if r == nil {
			// ok - might be dequeued by umount
		} else {
			// although very unlikely, it is possible that kernel sends
			// unexpected NotifyReply with our notifyUnique, then
			// retrieveNext wraps, makes full cycle, and another
			// retrieve request is made with the same notifyUnique.
			ms.opts.Logger.Printf("W: INODE_RETRIEVE_CACHE: request with notifyUnique=%d mutated", q.NotifyUnique)
		}
		ms.retrieveMu.Unlock()
		return 0, result
	}

	// NotifyRetrieveOut sent to the kernel successfully. Now the kernel
	// have to return data in a separate write-style NotifyReply request.
	// Wait for the result.
	<-reading.ready
	return reading.n, reading.st
}

// retrieveCacheRequest represents in-flight cache retrieve request.
type retrieveCacheRequest struct {
	nodeid uint64
	offset uint64
	dest   []byte

	// reply status
	n     int
	st    Status
	ready chan struct{}
}

// DeleteNotify notifies the kernel that an entry is removed from a
// directory.  In many cases, this is equivalent to EntryNotify,
// except when the directory is in use, eg. as working directory of
// some process. You should not hold any FUSE filesystem locks, as that
// can lead to deadlock.
func (ms *Server) DeleteNotify(parent uint64, child uint64, name string) Status {
	if ms.kernelSettings.Minor < 18 {
		return ms.EntryNotify(parent, name)
	}

	req := newNotifyRequest(_OP_NOTIFY_DELETE)

	entry := (*NotifyInvalDeleteOut)(req.outData())
	entry.Parent = parent
	entry.Child = child
	entry.NameLen = uint32(len(name))

	// Many versions of FUSE generate stacktraces if the
	// terminating null byte is missing.
	nameBytes := make([]byte, len(name)+1)
	copy(nameBytes, name)
	nameBytes[len(nameBytes)-1] = '\000'
	req.outPayload = nameBytes

	return ms.notifyWrite(req)
}

// EntryNotify should be used if the existence status of an entry
// within a directory changes. You should not hold any FUSE filesystem
// locks, as that can lead to deadlock.
func (ms *Server) EntryNotify(parent uint64, name string) Status {
	if !ms.kernelSettings.SupportsNotify(NOTIFY_INVAL_ENTRY) {
		return ENOSYS
	}
	req := newNotifyRequest(_OP_NOTIFY_INVAL_ENTRY)
	entry := (*NotifyInvalEntryOut)(req.outData())
	entry.Parent = parent
	entry.NameLen = uint32(len(name))

	// Many versions of FUSE generate stacktraces if the
	// terminating null byte is missing.
	nameBytes := make([]byte, len(name)+1)
	copy(nameBytes, name)
	nameBytes[len(nameBytes)-1] = '\000'
	req.outPayload = nameBytes

	return ms.notifyWrite(req)
}

// SupportsVersion returns true if the kernel supports the given
// protocol version or newer.
func (in *InitIn) SupportsVersion(maj, min uint32) bool {
	return in.Major > maj || (in.Major == maj && in.Minor >= min)
}

// SupportsNotify returns whether a certain notification type is
// supported. Pass any of the NOTIFY_* types as argument.
func (in *InitIn) SupportsNotify(notifyType int) bool {
	switch notifyType {
	case NOTIFY_INVAL_ENTRY:
		return in.SupportsVersion(7, 12)
	case NOTIFY_INVAL_INODE:
		return in.SupportsVersion(7, 12)
	case NOTIFY_STORE_CACHE, NOTIFY_RETRIEVE_CACHE:
		return in.SupportsVersion(7, 15)
	case NOTIFY_DELETE:
		return in.SupportsVersion(7, 18)
	}
	return false
}

// supportsRenameSwap returns whether the kernel supports the
// renamex_np(2) syscall. This is only supported on OS X.
func (in *InitIn) supportsRenameSwap() bool {
	return in.Flags&CAP_RENAME_SWAP != 0
}

// WaitMount waits for the first request to be served. Use this to
// avoid racing between accessing the (empty or not yet mounted)
// mountpoint, and the OS trying to setup the user-space mount.
func (ms *Server) WaitMount() error {
	err := <-ms.ready
	if err != nil {
		return err
	}
	if parseFuseFd(ms.mountPoint) >= 0 {
		// Magic `/dev/fd/N` mountpoint. We don't know the real mountpoint, so
		// we cannot run the poll hack.
		return nil
	}
	return pollHack(ms.mountPoint)
}

// parseFuseFd checks if `mountPoint` is the special form /dev/fd/N (with N >= 0),
// and returns N in this case. Returns -1 otherwise.
func parseFuseFd(mountPoint string) (fd int) {
	dir, file := path.Split(mountPoint)
	if dir != "/dev/fd/" {
		return -1
	}
	fd, err := strconv.Atoi(file)
	if err != nil || fd <= 0 {
		return -1
	}
	return fd
}
