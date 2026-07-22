package erofs

import (
	"fmt"
	"io"
	"io/fs"
	"math/bits"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/erofs/go-erofs/internal/builder"
	"github.com/erofs/go-erofs/internal/disk"
)

// --- Exported types ---

// Writer is a writable filesystem that produces an EROFS image on Close.
// Files are added via Create, Mkdir, Symlink, and Mknod, then finalized
// by calling Close which serializes the complete EROFS image.
type Writer struct {
	out          io.WriteSeeker
	closed       bool
	blockSize    int    // 0 = unset, resolved to defaultBlockSize in Close
	buildTime    uint64 // from WithBuildTime or buildTimer
	buildTimeNs  uint32
	hasBuildTime bool
	wErr         error               // sticky error: once set, all subsequent ops return it
	root         *fsEntry            // root directory
	byPath       map[string]*fsEntry // path → entry (all types)

	devices []uint64 // per-device block counts (one per MetadataOnly source)

	// Per-CopyFrom state, reset at the start of each CopyFrom call.
	copyMetadataOnly bool   // metadata-only for current CopyFrom
	copyMerge        bool   // merge mode: apply whiteouts
	copyDeviceID     uint16 // device ID assigned to current MetadataOnly CopyFrom

	dataFile *os.File // external data file (nil = spool mode)
	dataOff  int64    // current byte offset in data file
	spool    *os.File // temp spool (created lazily)
	spoolOff int64    // current byte offset in spool
	tempDir  string   // from WithTempDir
	cpBuf    []byte   // shared buffer for io.Copy into File
	padBuf   []byte   // shared zero buffer for padding (block-sized, lazy)
}

// File is a writable regular file returned by Writer.Create.
// Data is written via Write or ReadFrom, then committed with Close.
type File struct {
	fs           *Writer
	entry        *fsEntry
	dataStartOff int64 // byte offset where this file's data begins
	written      int64
	closed       bool
}

// CreateOpt configures EROFS image creation.
type CreateOpt func(*createOptions)

// CopyOpt configures a CopyFrom operation.
type CopyOpt func(*Writer)

// --- Constructor ---

// Create returns a Writer that produces an EROFS image on Close.
// Options configure build time, data file, and temp directory.
func Create(out io.WriteSeeker, opts ...CreateOpt) *Writer {
	var o createOptions
	for _, opt := range opts {
		opt(&o)
	}

	root := &fsEntry{
		path: "/",
		ino:  &fsInode{mode: disk.StatTypeDir | 0o755},
	}
	fsys := &Writer{
		out:          out,
		buildTime:    o.buildTime,
		buildTimeNs:  o.buildTimeNs,
		hasBuildTime: o.hasBuildTime,
		root:         root,
		byPath:       map[string]*fsEntry{"/": root},
		dataFile:     o.dataFile,
		tempDir:      o.tempDir,
	}

	if o.blockSize != 0 {
		if err := fsys.setBlockSize(o.blockSize); err != nil {
			fsys.wErr = err
		}
	}

	if o.dataFile != nil {
		// Reserve device slot 0 (DeviceID=1) for the data file.
		// MetadataOnly CopyFrom device IDs will start at slot 1+.
		// The reserved slot is filled in with the actual block count at Close.
		fsys.devices = append(fsys.devices, 0)
		off, err := o.dataFile.Seek(0, io.SeekEnd)
		if err == nil {
			fsys.dataOff = off
		}
	}

	return fsys
}

// --- CopyOpt functions ---

// MetadataOnly configures the current CopyFrom to emit only metadata.
// Regular files with pre-existing chunk mappings use chunk-based layout
// referencing an external device; file data is not copied.
func MetadataOnly() CopyOpt {
	return func(w *Writer) {
		w.copyMetadataOnly = true
	}
}

// Merge enables overlay merge semantics for the current CopyFrom.
// AUFS-style whiteout files (.wh.<name>) delete the named entry from
// prior layers, and opaque markers (.wh..wh..opq) delete all children
// of their parent directory. The whiteout entries themselves are not
// added to the image.
//
// When using Merge with a source containing AUFS whiteout files, do not
// pre-convert them; the Writer processes raw whiteout entries directly.
func Merge() CopyOpt {
	return func(w *Writer) {
		w.copyMerge = true
	}
}

// --- CreateOpt functions ---

// WithBlockSize sets the filesystem block size. The value must be a power
// of two between 512 and 64 KiB. When unset the default is 4096.
// An invalid size causes subsequent Writer operations to return an error.
// If CopyFrom is called with a source that declares a different block size,
// CopyFrom returns an error.
func WithBlockSize(n int) CreateOpt {
	return func(o *createOptions) {
		o.blockSize = n
	}
}

// WithBuildTime sets the filesystem build timestamp.
func WithBuildTime(sec uint64, nsec uint32) CreateOpt {
	return func(o *createOptions) {
		o.buildTime = sec
		o.buildTimeNs = nsec
		o.hasBuildTime = true
	}
}

// WithDataFile sets an external data file for metadata-only mode.
// File.Write appends to this file at block-aligned offsets; chunk
// indexes reference those blocks with DeviceID=1.
func WithDataFile(f *os.File) CreateOpt {
	return func(o *createOptions) {
		o.dataFile = f
	}
}

// WithTempDir overrides the temp directory for the spool file.
// Only used when no data file is provided.
func WithTempDir(dir string) CreateOpt {
	return func(o *createOptions) {
		o.tempDir = dir
	}
}

// --- Writer entry methods ---

// Create creates a regular file with default mode 0644. The caller must
// Close the returned File.
func (fsys *Writer) Create(name string) (*File, error) {
	if fsys.wErr != nil {
		return nil, fsys.wErr
	}
	name = cleanPath(name)
	if name == "/" {
		return nil, fmt.Errorf("mkfs: cannot create file at root")
	}
	if err := fsys.checkPath(name); err != nil {
		return nil, err
	}

	fsys.ensureParent(name)

	ino := &fsInode{mode: disk.StatTypeReg | 0o644}
	e := &fsEntry{path: name, ino: ino}
	fsys.addChild(e)

	f := &File{
		fs:    fsys,
		entry: e,
	}

	if fsys.dataFile != nil {
		f.dataStartOff = fsys.dataOff
		ino.dataStartOff = fsys.dataOff
	} else {
		if err := fsys.ensureSpool(); err != nil {
			return nil, err
		}
		f.dataStartOff = fsys.spoolOff
		ino.spoolOff = fsys.spoolOff
		ino.dataStartOff = fsys.spoolOff
	}

	return f, nil
}

// Mkdir creates a directory. Only permission bits from perm are used;
// type bits are forced to directory. Mkdir("/", perm) sets root permissions.
func (fsys *Writer) Mkdir(name string, perm fs.FileMode) error {
	if fsys.wErr != nil {
		return fsys.wErr
	}
	name = cleanPath(name)
	if name == "/" {
		fsys.root.ino.mode = disk.StatTypeDir | uint16(perm.Perm())
		return nil
	}
	if err := fsys.checkPath(name); err != nil {
		return err
	}

	fsys.ensureParent(name)

	e := &fsEntry{
		path: name,
		ino:  &fsInode{mode: disk.StatTypeDir | uint16(perm.Perm())},
	}
	fsys.addChild(e)

	return nil
}

// Symlink creates newname as a symbolic link to oldname (mode 0777).
func (fsys *Writer) Symlink(oldname, newname string) error {
	if fsys.wErr != nil {
		return fsys.wErr
	}
	newname = cleanPath(newname)
	if newname == "/" {
		return fmt.Errorf("mkfs: cannot create symlink at root")
	}
	if err := fsys.checkPath(newname); err != nil {
		return err
	}

	fsys.ensureParent(newname)

	e := &fsEntry{
		path: newname,
		ino:  &fsInode{mode: disk.StatTypeSymlink | 0o777, linkTarget: oldname},
	}
	fsys.addChild(e)

	return nil
}

// Mknod creates a device, FIFO, or socket. mode must include type bits
// (e.g. disk.StatTypeChrdev | 0o666).
func (fsys *Writer) Mknod(name string, mode uint16, rdev uint32) error {
	if fsys.wErr != nil {
		return fsys.wErr
	}
	name = cleanPath(name)
	if name == "/" {
		return fmt.Errorf("mkfs: cannot mknod at root")
	}
	if err := fsys.checkPath(name); err != nil {
		return err
	}

	fsys.ensureParent(name)

	e := &fsEntry{
		path: name,
		ino:  &fsInode{mode: mode, rdev: rdev},
	}
	fsys.addChild(e)

	return nil
}

// --- Writer metadata methods ---

// Chmod sets permission bits on the named path, preserving type bits.
func (fsys *Writer) Chmod(name string, mode fs.FileMode) error {
	if fsys.wErr != nil {
		return fsys.wErr
	}
	e, err := fsys.lookup(name)
	if err != nil {
		return err
	}
	perm := goModeToUnixMode(mode) & 0o7777
	e.ino.mode = (e.ino.mode & disk.StatTypeMask) | perm
	return nil
}

// Chown sets the owner UID and GID on the named path.
func (fsys *Writer) Chown(name string, uid, gid int) error {
	if fsys.wErr != nil {
		return fsys.wErr
	}
	e, err := fsys.lookup(name)
	if err != nil {
		return err
	}
	e.ino.uid = uint32(uid)
	e.ino.gid = uint32(gid)
	return nil
}

// Chtimes sets the access and modification times on the named path.
// EROFS only stores mtime; atime is retained for read-back before Close.
func (fsys *Writer) Chtimes(name string, atime time.Time, mtime time.Time) error {
	if fsys.wErr != nil {
		return fsys.wErr
	}
	e, err := fsys.lookup(name)
	if err != nil {
		return err
	}
	e.ino.atime = uint64(atime.Unix())
	e.ino.atimeNs = uint32(atime.Nanosecond())
	e.ino.mtime = uint64(mtime.Unix())
	e.ino.mtimeNs = uint32(mtime.Nanosecond())
	return nil
}

// Setxattr sets an extended attribute on the named path.
func (fsys *Writer) Setxattr(name, attr, value string) error {
	if fsys.wErr != nil {
		return fsys.wErr
	}
	e, err := fsys.lookup(name)
	if err != nil {
		return err
	}
	if e.ino.xattrs == nil {
		e.ino.xattrs = make(map[string]string)
	}
	e.ino.xattrs[attr] = value
	return nil
}

// SetNlink overrides the computed link count on the named path.
func (fsys *Writer) SetNlink(name string, nlink uint32) error {
	if fsys.wErr != nil {
		return fsys.wErr
	}
	e, err := fsys.lookup(name)
	if err != nil {
		return err
	}
	e.ino.nlink = nlink
	e.ino.nlinkSet = true
	return nil
}

// --- Writer bulk copy ---

// CopyFrom walks an fs.FS and adds all entries.
// Opens files for data when Entry.Data is nil.
// Reads symlink targets via readLinker interface when Entry.LinkTarget is empty.
// If src implements blockSizer, the image block size is set accordingly.
func (fsys *Writer) CopyFrom(src fs.FS, opts ...CopyOpt) error {
	if fsys.wErr != nil {
		return fsys.wErr
	}
	// Reset per-CopyFrom state.
	fsys.copyMetadataOnly = false
	fsys.copyMerge = false
	fsys.copyDeviceID = 0
	for _, opt := range opts {
		opt(fsys)
	}
	// Detect EROFS image source for direct metadata/chunk extraction.
	// The fast path (copyFromImage) only applies to MetadataOnly mode
	// where no file data needs to be read — just inodes, dirents, and
	// chunk indexes. For non-MetadataOnly, fall through to the fs.WalkDir
	// path which opens files for data.
	if srcImg, ok := src.(*image); ok {
		if err := fsys.setBlockSize(int(srcImg.blockSize())); err != nil {
			return err
		}
		if !fsys.hasBuildTime {
			fsys.buildTime = srcImg.buildTime()
			fsys.hasBuildTime = true
		}
		if fsys.copyMetadataOnly {
			devBlocks := srcImg.deviceBlocks()
			fsys.devices = append(fsys.devices, devBlocks...)
			fsys.copyDeviceID = uint16(len(fsys.devices) - len(devBlocks) + 1)
			return fsys.copyFromImage(srcImg)
		}
	}
	if bs, ok := src.(blockSizer); ok {
		if err := fsys.setBlockSize(int(bs.BlockSize())); err != nil {
			return err
		}
	}
	if fsys.copyMetadataOnly {
		if db, ok := src.(deviceBlocker); ok {
			fsys.devices = append(fsys.devices, db.DeviceBlocks())
			fsys.copyDeviceID = uint16(len(fsys.devices))
		}
	}
	if bt, ok := src.(buildTimer); ok && !fsys.hasBuildTime {
		fsys.buildTime = bt.BuildTime()
		fsys.hasBuildTime = true
	}
	return fs.WalkDir(src, ".", func(fpath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", fpath, err)
		}

		// Normalize path to absolute
		p := "/" + fpath
		if fpath == "." {
			p = "/"
		}

		// Merge mode: process whiteout markers.
		if fsys.copyMerge && p != "/" {
			base := path.Base(p)
			if strings.HasPrefix(base, whiteoutPrefix) {
				if base == opaqueWhiteout {
					// Opaque directory: remove all prior children of parent.
					fsys.removeChildren(path.Dir(p))
				} else {
					// File whiteout: remove the named entry.
					target := path.Join(path.Dir(p), base[len(whiteoutPrefix):])
					fsys.remove(target)
				}
				return nil
			}
		}

		// Extract extended metadata from Sys().
		var be *builder.Entry
		switch sys := info.Sys().(type) {
		case *builder.Entry:
			be = sys
		case *Stat:
			// EROFS image source: convert *Stat to *builder.Entry.
			be = &builder.Entry{
				UID:     sys.UID,
				GID:     sys.GID,
				Mtime:   sys.Mtime,
				MtimeNs: sys.MtimeNs,
				Nlink:   uint32(sys.Nlink),
				Rdev:    sys.Rdev,
				Xattrs:  sys.Xattrs,
			}
		}

		// For regular files, get a data reader.
		if info.Mode().IsRegular() && info.Size() > 0 && (be == nil || be.Data == nil) {
			// In metadata-only mode, data is referenced via chunk indexes
			// from the source — no need to open the file.
			if fsys.copyMetadataOnly {
				if be == nil {
					be = entryFromSys(info)
					if be == nil {
						be = &builder.Entry{}
					}
				}
				// Generate chunks from DataRange if available.
				if len(be.Chunks) == 0 {
					if dr, ok := info.(dataRanger); ok {
						if ranges := dr.DataRange(); len(ranges) > 0 {
							chunks, err := fsys.chunksFromRanges(ranges, info.Size())
							if err != nil {
								return fmt.Errorf("chunksFromRanges %s: %w", p, err)
							}
							be.Chunks = chunks
							// Contiguous: a single non-hole range whose total-size
							// invariant is satisfied (guaranteed by chunksFromRanges)
							// means the file is fully covered by one contiguous extent.
							be.Contiguous = len(ranges) == 1 && ranges[0].Offset != holeOffset
						}
					}
				}
				return fsys.add(p, &entryFileInfo{info: info, sys: be})
			}
			// For EROFS sources, use direct SectionReader (bypasses
			// block-at-a-time reader for contiguous flat-plain data).
			if srcImg, ok := src.(*image); ok {
				if st, ok := info.Sys().(*Stat); ok {
					f := file{img: srcImg, nid: uint64(st.Ino)}
					if ino, err := f.readInfo(); err == nil {
						if dr := srcImg.openDirect(ino); dr != nil {
							if be == nil {
								be = &builder.Entry{}
							}
							be.Data = dr
							return fsys.add(p, &entryFileInfo{info: info, sys: be})
						}
					}
				}
			}
			f, err := src.Open(fpath)
			if err != nil {
				return fmt.Errorf("open %s: %w", fpath, err)
			}
			if be == nil {
				be = entryFromSys(info)
				if be == nil {
					be = &builder.Entry{}
				}
			}
			be.Data = f.(io.Reader)
			return fsys.add(p, &entryFileInfo{info: info, sys: be})
		}

		// For symlinks without LinkTarget, read via ReadLink interface.
		if info.Mode()&fs.ModeSymlink != 0 && (be == nil || be.LinkTarget == "") {
			if rl, ok := src.(readLinker); ok {
				target, err := rl.ReadLink(fpath)
				if err != nil {
					return fmt.Errorf("readlink %s: %w", fpath, err)
				}
				if be == nil {
					be = entryFromSys(info)
					if be == nil {
						be = &builder.Entry{}
					}
				}
				be.LinkTarget = target
				return fsys.add(p, &entryFileInfo{info: info, sys: be})
			}
		}

		// For directories, ensure nlink >= 2.
		if info.Mode().IsDir() {
			if be == nil {
				be = entryFromSys(info)
				if be == nil {
					be = &builder.Entry{Nlink: 2}
				}
			}
			if be.Nlink < 2 {
				be.Nlink = 2
			}
			return fsys.add(p, &entryFileInfo{info: info, sys: be})
		}

		// General case: devices, fifos, sockets, etc.
		// Wrap in entryFileInfo when be was extracted from Sys()
		// so that add() sees the metadata.
		if be != nil {
			return fsys.add(p, &entryFileInfo{info: info, sys: be})
		}
		return fsys.add(p, info)
	})
}

// --- Writer finalization ---

// Close writes the EROFS image. The FS must not be used after Close.
func (fsys *Writer) Close() error {
	if fsys.wErr != nil {
		return fsys.wErr
	}
	if fsys.closed {
		return fmt.Errorf("mkfs: FS already closed")
	}
	fsys.closed = true

	if fsys.spool != nil {
		defer func() { _ = fsys.spool.Close() }()
	}

	fsys.resolveBlockSize()

	if fsys.dataFile != nil {
		// Fill in the reserved device slot 0 with the actual block count.
		blocks := (fsys.dataOff + int64(fsys.blockSize) - 1) / int64(fsys.blockSize)
		fsys.devices[0] = uint64(blocks)
	}

	buildTime := fsys.buildTime
	if !fsys.hasBuildTime {
		buildTime = uint64(time.Now().Unix())
	}

	// Build erofsEntry tree from the fsEntry tree via BFS.
	root := fsys.buildErofsTree()

	var chunkBits uint8
	for cs := fsys.blockSize; cs < 4096; cs <<= 1 {
		chunkBits++
	}

	ew := &erofsWriter{
		buildTime:   buildTime,
		buildTimeNs: fsys.buildTimeNs,
		devices:     fsys.devices,
		blockSize:   fsys.blockSize,
		chunkBits:   chunkBits,
		zeroBuf:     make([]byte, fsys.blockSize),
	}

	ew.planLayout(root)
	fixParentNids(root, root)

	return ew.write(fsys.out)
}

// Stat returns file info for the named path. The name is cleaned the same
// way as other Writer methods (leading slash, no trailing slash).
//
// The Writer does not follow symlinks: a path that traverses through a
// symlink or other non-directory component returns ErrNotDirectory.
func (fsys *Writer) Stat(name string) (fs.FileInfo, error) {
	e, err := fsys.resolveEntry("stat", name)
	if err != nil {
		return nil, err
	}
	return &writerFileInfo{entry: e}, nil
}

// Open opens the named file for reading. For regular files, the file must
// have been closed (data finalized) before it can be opened for reading.
// For directories, the returned file implements fs.ReadDirFile.
//
// The Writer does not follow symlinks: a path that traverses through a
// symlink or other non-directory component returns ErrNotDirectory.
func (fsys *Writer) Open(name string) (fs.File, error) {
	entry, err := fsys.resolveEntry("open", name)
	if err != nil {
		return nil, err
	}
	name = cleanPath(name)
	ino := entry.ino

	switch ino.mode & disk.StatTypeMask {
	case disk.StatTypeDir:
		return &readDir{fsys: fsys, entry: entry}, nil

	case disk.StatTypeReg:
		if !ino.fileClosed {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fmt.Errorf("file not yet closed for writing")}
		}
		var sr *io.SectionReader
		if fsys.dataFile != nil {
			sr = io.NewSectionReader(fsys.dataFile, ino.dataStartOff, int64(ino.size))
		} else if fsys.spool != nil && ino.size > 0 {
			sr = io.NewSectionReader(fsys.spool, ino.dataStartOff, int64(ino.size))
		}
		return &readFile{entry: entry, reader: sr}, nil

	default:
		// Symlinks, devices, etc.: stat-only, no readable data.
		return &readFile{entry: entry}, nil
	}
}

// Lstat returns the FileInfo for the named path. Symlinks are stored as their
// own entries, so the result describes the link itself, never its target.
// Together with ReadLink this lets the Writer satisfy fs.ReadLinkFS.
func (fsys *Writer) Lstat(name string) (fs.FileInfo, error) {
	return fsys.Stat(name)
}

// ReadLink returns the target of the symlink at name. It returns ErrInvalid
// if name is not a symlink, ErrNotDirectory if the path traverses a
// non-directory, or ErrNotExist if it is absent. Targets are readable before
// the image is finalised with Close.
func (fsys *Writer) ReadLink(name string) (string, error) {
	entry, err := fsys.resolveEntry("readlink", name)
	if err != nil {
		return "", err
	}
	if entry.ino.mode&disk.StatTypeMask != disk.StatTypeSymlink {
		return "", &fs.PathError{Op: "readlink", Path: cleanPath(name), Err: fs.ErrInvalid}
	}
	return entry.ino.linkTarget, nil
}

// Link creates newname as a hard link to the existing file at oldname.
// Both names refer to the same fsInode, so the data and metadata (mode, owner,
// times, xattrs) are shared: a later change through either name is visible
// through the other. Directories cannot be linked.
//
// The shared nlink count is maintained automatically; do not call SetNlink on
// a hard-linked entry.
func (fsys *Writer) Link(oldname, newname string) error {
	if fsys.wErr != nil {
		return fsys.wErr
	}
	oldname = cleanPath(oldname)
	newname = cleanPath(newname)
	if newname == "/" {
		return fmt.Errorf("mkfs: cannot link at root")
	}

	src, err := fsys.resolveEntry("link", oldname)
	if err != nil {
		return err
	}
	if src.ino.mode&disk.StatTypeMask == disk.StatTypeDir {
		return &fs.PathError{Op: "link", Path: oldname, Err: ErrIsDirectory}
	}
	if err := fsys.checkPath(newname); err != nil {
		return err
	}
	fsys.ensureParent(newname)

	// Increment nlink before the first link is created so that the count
	// reflects all names, including the original.
	if src.ino.nlink == 0 {
		src.ino.nlink = 1 // first time we add a second name
	}
	src.ino.nlink++

	// The new entry shares the source fsInode directly.
	dst := &fsEntry{
		path: newname,
		ino:  src.ino,
	}
	fsys.addChild(dst)
	return nil
}

// --- File methods ---

// Write appends data to the file.
func (f *File) Write(p []byte) (int, error) {
	if f.closed {
		return 0, fmt.Errorf("mkfs: write to closed file")
	}

	if f.fs.dataFile != nil {
		n, err := f.fs.dataFile.Write(p)
		f.written += int64(n)
		f.fs.dataOff += int64(n)
		return n, err
	}

	n, err := f.fs.spool.Write(p)
	f.written += int64(n)
	f.fs.spoolOff += int64(n)
	return n, err
}

// ReadFrom implements io.ReaderFrom, allowing io.Copy(f, src) to use
// a shared buffer instead of allocating a new 32KB buffer per call.
func (f *File) ReadFrom(r io.Reader) (int64, error) {
	buf := f.fs.copyBuf()
	var written int64
	for {
		nr, er := r.Read(buf)
		if nr > 0 {
			nw, ew := f.Write(buf[:nr])
			written += int64(nw)
			if ew != nil {
				return written, ew
			}
		}
		if er != nil {
			if er == io.EOF {
				return written, nil
			}
			return written, er
		}
	}
}

// Close commits the file entry. For data file mode, pads to block
// boundary and records chunk indexes.
func (f *File) Close() error {
	if f.closed {
		return fmt.Errorf("mkfs: file already closed")
	}
	f.closed = true
	f.entry.ino.fileClosed = true
	f.entry.ino.size = uint64(f.written)

	if f.fs.dataFile != nil {
		return f.closeDataFile()
	}
	return nil
}

// Chmod sets permission bits on the file, matching os.File.Chmod.
func (f *File) Chmod(mode fs.FileMode) error {
	perm := goModeToUnixMode(mode) & 0o7777
	f.entry.ino.mode = (f.entry.ino.mode & disk.StatTypeMask) | perm
	return nil
}

// Chown sets the owner UID and GID on the file, matching os.File.Chown.
func (f *File) Chown(uid, gid int) error {
	f.entry.ino.uid = uint32(uid)
	f.entry.ino.gid = uint32(gid)
	return nil
}

// --- Internal types ---

// fsInode holds the shared inode payload for a filesystem entry. Every fsEntry
// owns exactly one *fsInode; hard links share the same *fsInode across multiple
// fsEntry values. No fsEntry is "primary" — any surviving name can own the
// on-disk inode during serialization.
type fsInode struct {
	mode     uint16
	uid      uint32
	gid      uint32
	atime    uint64
	atimeNs  uint32
	mtime    uint64
	mtimeNs  uint32
	nlink    uint32
	nlinkSet bool // true if SetNlink was called; suppresses auto-computation
	size     uint64
	rdev     uint32
	xattrs   map[string]string

	// Symlink target (empty for non-symlinks).
	linkTarget string

	// Chunk-based layout (metadata-only mode).
	chunks     []builder.Chunk
	contiguous bool

	// Data location in spool or external data file.
	spoolOff     int64
	dataStartOff int64
	fileClosed   bool      // true after File.Close()
	directData   io.Reader // non-nil when data is referenced directly (no spool copy)

	metadataOnly bool // chunk-based layout even when chunks is nil
}

// fsEntry is the directory-entry view of a file. It carries only the path
// and tree linkage; all fsInode data (mode, owner, size, data) lives in ino,
// which is shared among all hard-linked names.
type fsEntry struct {
	path string
	ino  *fsInode

	// Tree structure — maintained during add/remove.
	parent   *fsEntry
	children []*fsEntry

	removed bool // true if removed by a whiteout in a merge layer
}

// createOptions holds the parsed option values for Create.
type createOptions struct {
	buildTime    uint64
	buildTimeNs  uint32
	hasBuildTime bool
	blockSize    int      // 0 = use default
	dataFile     *os.File // external data file for metadata-only mode
	tempDir      string   // temp directory for spool file
}

// blockSizer may be implemented by an fs.FS to declare its block size.
// Writer.CopyFrom uses this to set the image block size automatically.
type blockSizer interface {
	BlockSize() uint32
}

// buildTimer may be implemented by an fs.FS to suggest a build timestamp.
// If the caller hasn't set WithBuildTime, CopyFrom uses this value.
// Entries whose mtime matches the build time can use compact (32-byte) inodes.
type buildTimer interface {
	BuildTime() uint64
}

// deviceBlocker may be implemented by an fs.FS to declare the total
// block count of its backing device. Writer.CopyFrom uses this to
// configure the device slot for metadata-only mode.
type deviceBlocker interface {
	DeviceBlocks() uint64
}

// readLinker is an interface for filesystems that support reading symlink targets.
type readLinker interface {
	ReadLink(name string) (string, error)
}

// dataRanger may be implemented by fs.FileInfo to provide the physical
// location of uncompressed file data in backing devices. CopyFrom checks
// this via type assertion in metadata-only mode to build chunk indexes
// without requiring the caller to construct internal chunk types.
//
// This interface should only be implemented for files whose device data
// is stored verbatim (uncompressed). For compressed files, return nil or
// do not implement the interface. In full-image mode CopyFrom then falls
// back to reading through Open(), which decompresses transparently. In
// MetadataOnly mode there is no such fallback: the file is stored as a
// chunk-based inode with no physical mappings (all holes).
type dataRanger interface {
	DataRange() []DataRange
}

// --- Internal types ---

// erofsEntry is the internal representation of a file/dir/symlink used by the builder.
type erofsEntry struct {
	mode    uint16
	uid     uint32
	gid     uint32
	mtime   uint64
	mtimeNs uint32
	nlink   uint32
	size    uint64
	rdev    uint32

	name      string
	path      string
	children  []*erofsEntry
	symTarget string

	// For regular files — metadata-only mode
	chunks       []builder.Chunk
	contiguous   bool  // data blocks are contiguous; use large chunk size
	chunkBits    uint8 // per-entry chunk bits (0 = use global)
	metadataOnly bool  // chunk-based layout even without chunks

	// For regular files — full-image mode
	data io.Reader

	// Extended attributes
	xattrs map[string]string

	// Hard links: non-nil for secondary entries that share an fsInode with primary.
	// The primary entry gets an on-disk inode; secondary entries borrow its NID.
	hardLinkPrimary *erofsEntry

	// EROFS layout (assigned during planning)
	nid           uint64
	parentNid     uint64
	erofsFileType uint8
	layout        uint8
	compact       bool // true = 32-byte compact inode; false = 64-byte extended
	xattrSize     int  // bytes of xattr area (0 if no xattrs)
	trailingSize  int

	// Data block address for flat-plain files (full-image mode)
	dataBlkAddr uint32
}

// --- Internal helpers ---

// cleanPath normalizes a filesystem path to an absolute rooted form.
func cleanPath(p string) string {
	if p == "" || p == "." || p == "/" {
		return "/"
	}
	p = path.Clean(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// fixParentNids sets the parent NID in the ".." dirent for all directories.
// This must be called after planLayout has assigned NIDs.
func fixParentNids(e *erofsEntry, parent *erofsEntry) {
	e.parentNid = parent.nid
	for _, c := range e.children {
		if c.mode&disk.StatTypeMask == disk.StatTypeDir {
			fixParentNids(c, e)
		}
	}
}

// entryFileInfo wraps an fs.FileInfo but overrides Sys() to return a *builder.Entry.
type entryFileInfo struct {
	info fs.FileInfo
	sys  *builder.Entry
}

func (fi *entryFileInfo) Name() string       { return fi.info.Name() }
func (fi *entryFileInfo) Size() int64        { return fi.info.Size() }
func (fi *entryFileInfo) Mode() fs.FileMode  { return fi.info.Mode() }
func (fi *entryFileInfo) ModTime() time.Time { return fi.info.ModTime() }
func (fi *entryFileInfo) IsDir() bool        { return fi.info.IsDir() }
func (fi *entryFileInfo) Sys() any           { return fi.sys }

// WriterStat is returned by Sys() on [fs.FileInfo] values from a [Writer],
// exposing fsInode metadata before the image is finalised. After [Writer.Close]
// the image can be reopened with [Open], whose Sys() returns a [Stat].
type WriterStat struct {
	Mode    fs.FileMode
	Size    int64
	UID     uint32
	GID     uint32
	Rdev    uint32
	Mtime   uint64
	MtimeNs uint32
	Nlink   uint32
	Xattrs  map[string]string
}

// writerFileInfo implements fs.FileInfo for an fsEntry.
type writerFileInfo struct {
	entry *fsEntry
}

func (fi *writerFileInfo) Name() string { return path.Base(fi.entry.path) }
func (fi *writerFileInfo) Size() int64  { return int64(fi.entry.ino.size) }
func (fi *writerFileInfo) Mode() fs.FileMode {
	return disk.EroFSModeToGoFileMode(fi.entry.ino.mode)
}
func (fi *writerFileInfo) ModTime() time.Time {
	return time.Unix(int64(fi.entry.ino.mtime), int64(fi.entry.ino.mtimeNs))
}
func (fi *writerFileInfo) IsDir() bool {
	return fi.entry.ino.mode&disk.StatTypeMask == disk.StatTypeDir
}
func (fi *writerFileInfo) Sys() any {
	ino := fi.entry.ino
	var xattrs map[string]string
	if len(ino.xattrs) > 0 {
		xattrs = make(map[string]string, len(ino.xattrs))
		for k, v := range ino.xattrs {
			xattrs[k] = v
		}
	}
	return &WriterStat{
		Mode:    disk.EroFSModeToGoFileMode(ino.mode),
		Size:    int64(ino.size),
		UID:     ino.uid,
		GID:     ino.gid,
		Rdev:    ino.rdev,
		Mtime:   ino.mtime,
		MtimeNs: ino.mtimeNs,
		Nlink:   inodeNlink(ino),
		Xattrs:  xattrs,
	}
}

// inodeNlink returns the effective nlink for a shared inode.
func inodeNlink(ino *fsInode) uint32 {
	if ino.nlinkSet {
		return ino.nlink
	}
	if ino.nlink > 0 {
		return ino.nlink
	}
	if ino.mode&disk.StatTypeMask == disk.StatTypeDir {
		return 2
	}
	return 1
}

// readFile implements fs.File for reading back a finalized file's data.
type readFile struct {
	entry  *fsEntry
	reader *io.SectionReader // nil for empty files or non-regular types
	closed bool
}

func (f *readFile) Stat() (fs.FileInfo, error) {
	return &writerFileInfo{entry: f.entry}, nil
}

func (f *readFile) Read(p []byte) (int, error) {
	if f.closed {
		return 0, fmt.Errorf("mkfs: read from closed file")
	}
	if f.reader == nil {
		return 0, io.EOF
	}
	return f.reader.Read(p)
}

func (f *readFile) Close() error {
	if f.closed {
		return fmt.Errorf("mkfs: file already closed")
	}
	f.closed = true
	return nil
}

// readDir implements fs.ReadDirFile for a directory in Writer.
type readDir struct {
	fsys     *Writer
	entry    *fsEntry
	children []fs.DirEntry // lazily populated
	offset   int
	closed   bool
}

func (d *readDir) Stat() (fs.FileInfo, error) {
	return &writerFileInfo{entry: d.entry}, nil
}

func (d *readDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.entry.path, Err: fmt.Errorf("is a directory")}
}

func (d *readDir) Close() error {
	if d.closed {
		return fmt.Errorf("mkfs: dir already closed")
	}
	d.closed = true
	return nil
}

func (d *readDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if d.closed {
		return nil, fmt.Errorf("mkfs: read from closed dir")
	}
	if d.children == nil {
		d.children = d.collectChildren()
	}

	if n <= 0 {
		entries := d.children[d.offset:]
		d.offset = len(d.children)
		return entries, nil
	}

	remaining := d.children[d.offset:]
	if len(remaining) == 0 {
		return nil, io.EOF
	}
	if n > len(remaining) {
		n = len(remaining)
	}
	entries := remaining[:n]
	d.offset += n
	if d.offset >= len(d.children) {
		return entries, io.EOF
	}
	return entries, nil
}

func (d *readDir) collectChildren() []fs.DirEntry {
	children := make([]fs.DirEntry, 0, len(d.entry.children))
	for _, e := range d.entry.children {
		if e.removed {
			continue
		}
		children = append(children, &dirEntry{entry: e})
	}
	sort.Slice(children, func(i, j int) bool {
		return children[i].Name() < children[j].Name()
	})
	return children
}

// dirEntry implements fs.DirEntry for an fsEntry.
type dirEntry struct {
	entry *fsEntry
}

func (de *dirEntry) Name() string { return path.Base(de.entry.path) }
func (de *dirEntry) IsDir() bool  { return de.entry.ino.mode&disk.StatTypeMask == disk.StatTypeDir }
func (de *dirEntry) Type() fs.FileMode {
	return disk.EroFSModeToGoFileMode(de.entry.ino.mode).Type()
}
func (de *dirEntry) Info() (fs.FileInfo, error) { return &writerFileInfo{entry: de.entry}, nil }

// add adds a single entry. Mode and Size come from info; extended metadata
// comes from info.Sys(). Checks Sys() for *builder.Entry first, then
// platform-specific stat types as a fallback for plain fs.FS sources.
func (fsys *Writer) add(p string, info fs.FileInfo) error {
	p = cleanPath(p)
	mode := goModeToUnixMode(info.Mode())
	size := uint64(info.Size())
	typ := mode & disk.StatTypeMask

	be := entryFromSys(info)
	if be == nil {
		be = &builder.Entry{}
	}

	if p == "/" {
		ino := fsys.root.ino
		ino.mode = mode
		ino.uid = be.UID
		ino.gid = be.GID
		ino.mtime = be.Mtime
		ino.mtimeNs = be.MtimeNs
		if be.Nlink > 0 {
			ino.nlink = be.Nlink
			ino.nlinkSet = true
		}
		ino.xattrs = be.Xattrs
		return nil
	}

	fsys.ensureParent(p)

	ino := &fsInode{
		mode:       mode,
		uid:        be.UID,
		gid:        be.GID,
		mtime:      be.Mtime,
		mtimeNs:    be.MtimeNs,
		size:       size,
		rdev:       be.Rdev,
		xattrs:     be.Xattrs,
		linkTarget: be.LinkTarget,
		chunks:     be.Chunks,
		contiguous: be.Contiguous,
	}
	if be.Nlink > 0 {
		ino.nlink = be.Nlink
		ino.nlinkSet = true
	}
	fe := &fsEntry{path: p, ino: ino}

	// Handle duplicate paths (overwrite semantics).
	if existing, ok := fsys.byPath[p]; ok {
		// Detach the old fsInode before replacing it so that any hard-linked
		// names that still reference it get a correct nlink.
		if existing.ino != ino {
			unlinkInode(existing.ino)
		}
		existing.ino = ino
		fe = existing
	} else {
		fsys.addChild(fe)
	}

	if fsys.copyMetadataOnly {
		ino.metadataOnly = true
		// Remap chunk DeviceIDs from source-relative to absolute.
		// For single-device sources, all chunks use DeviceID=1
		// and get mapped to copyDeviceID.
		// For multi-device sources (e.g. EROFS images), chunks have
		// DeviceIDs 1..N that get offset by copyDeviceID-1.
		if fsys.copyDeviceID > 0 {
			offset := fsys.copyDeviceID - 1
			for i := range ino.chunks {
				ino.chunks[i].DeviceID += offset
			}
		}
	}

	// Write regular file data.
	// Skip entirely in metadata-only mode.
	needData := typ == disk.StatTypeReg && size > 0 && be.Data != nil &&
		!fsys.copyMetadataOnly
	if needData {
		// Data is stored locally; clear any source chunk mappings.
		ino.chunks = nil
		ino.contiguous = false
		if fsys.dataFile != nil {
			// Data file mode: copy through File for block-aligned padding and chunk recording.
			f := &File{fs: fsys, entry: fe}
			f.dataStartOff = fsys.dataOff
			ino.dataStartOff = fsys.dataOff
			if _, err := f.ReadFrom(be.Data); err != nil {
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		} else {
			// Spool mode: keep a direct reference to avoid copying.
			ino.directData = be.Data
			ino.fileClosed = true
		}
	} else {
		ino.fileClosed = true
	}

	return nil
}

// checkPath validates that a path hasn't already been registered.
func (fsys *Writer) checkPath(name string) error {
	if fsys.closed {
		return fmt.Errorf("mkfs: FS is closed")
	}
	if _, ok := fsys.byPath[name]; ok {
		return fmt.Errorf("mkfs: duplicate path %q", name)
	}
	return nil
}

// ensureParent creates implicit parent directories for name.
func (fsys *Writer) ensureParent(name string) {
	dir := path.Dir(name)
	if dir == "/" {
		return
	}
	// Walk up to find existing ancestors.
	var missing []string
	for d := dir; d != "/"; d = path.Dir(d) {
		if _, ok := fsys.byPath[d]; ok {
			break
		}
		missing = append(missing, d)
	}
	// Create in top-down order.
	for i := len(missing) - 1; i >= 0; i-- {
		d := missing[i]
		e := &fsEntry{
			path: d,
			ino:  &fsInode{mode: disk.StatTypeDir | 0o755},
		}
		fsys.addChild(e)
	}
}

// addChild registers an entry in the tree and byPath map.
// The entry's parent is resolved from its path.
func (fsys *Writer) addChild(e *fsEntry) {
	parent := fsys.byPath[path.Dir(e.path)]
	if parent == nil {
		parent = fsys.root
	}
	e.parent = parent
	parent.children = append(parent.children, e)
	fsys.byPath[e.path] = e
}

// remove marks an entry and all its descendants as removed.
// Used by Merge to process whiteout deletions.
func (fsys *Writer) remove(p string) {
	p = cleanPath(p)
	e, ok := fsys.byPath[p]
	if !ok {
		return
	}
	e.removed = true
	delete(fsys.byPath, p)
	unlinkInode(e.ino)
	if e.ino.mode&disk.StatTypeMask == disk.StatTypeDir {
		fsys.removeSubtree(e)
	}
}

// removeChildren marks all descendants of a directory as removed.
// The directory itself is not removed.
func (fsys *Writer) removeChildren(dir string) {
	dir = cleanPath(dir)
	e, ok := fsys.byPath[dir]
	if !ok {
		return
	}
	fsys.removeSubtree(e)
}

// removeSubtree recursively marks all descendants of e as removed.
func (fsys *Writer) removeSubtree(e *fsEntry) {
	for _, c := range e.children {
		if !c.removed {
			c.removed = true
			delete(fsys.byPath, c.path)
			unlinkInode(c.ino)
			if c.ino.mode&disk.StatTypeMask == disk.StatTypeDir {
				fsys.removeSubtree(c)
			}
		}
	}
}

// unlinkInode decrements the nlink counter of a shared inode when one of its
// names is removed, keeping the count consistent with surviving names.
func unlinkInode(ino *fsInode) {
	if ino.nlink > 0 {
		ino.nlink--
	}
}

// buildErofsTree converts the fsEntry tree into an erofsEntry tree via BFS.
// Children are sorted for deterministic output. The Writer is consumed.
func (fsys *Writer) buildErofsTree() *erofsEntry {
	type pair struct {
		fs *fsEntry
		er *erofsEntry
	}

	// seen maps a shared *fsInode to the first erofsEntry built for it.
	// Entries whose fsInode nlink > 1 are hard-linked; the first erofsEntry
	// seen for that fsInode owns the on-disk inode slot; later ones point
	// back via hardLinkPrimary and borrow its NID.
	seen := make(map[*fsInode]*erofsEntry)

	rootEr := fsys.fsToErofs(fsys.root)
	queue := []pair{{fsys.root, rootEr}}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		// Count child directories for nlink.
		var childDirs uint32
		for _, c := range cur.fs.children {
			if !c.removed && c.ino.mode&disk.StatTypeMask == disk.StatTypeDir {
				childDirs++
			}
		}
		if !cur.fs.ino.nlinkSet && cur.fs.ino.mode&disk.StatTypeMask == disk.StatTypeDir {
			cur.er.nlink = 2 + childDirs
		}

		// Convert and enqueue children.
		if len(cur.fs.children) > 0 {
			cur.er.children = make([]*erofsEntry, 0, len(cur.fs.children))
		}
		for _, c := range cur.fs.children {
			if c.removed {
				continue
			}
			ent := fsys.fsToErofs(c)

			// Hard link: fsInodes shared by more than one surviving name are
			// tracked in seen. The first erofsEntry for a given fsInode owns
			// the on-disk inode slot; subsequent ones point back to it.
			if inodeNlink(c.ino) > 1 {
				if owner, ok := seen[c.ino]; ok {
					ent.hardLinkPrimary = owner
				} else {
					seen[c.ino] = ent
				}
			}

			cur.er.children = append(cur.er.children, ent)
			if c.ino.mode&disk.StatTypeMask == disk.StatTypeDir {
				queue = append(queue, pair{c, ent})
			}
		}

		// Sort children for deterministic output.
		sort.Slice(cur.er.children, func(i, j int) bool {
			return cur.er.children[i].name < cur.er.children[j].name
		})
	}
	return rootEr
}

// fsToErofs converts an fsEntry to an erofsEntry.
// The name/path come from the directory entry; fsInode data comes from ino.
func (fsys *Writer) fsToErofs(entry *fsEntry) *erofsEntry {
	ino := entry.ino
	nlink := inodeNlink(ino)

	var data io.Reader
	if fsys.dataFile == nil && len(ino.chunks) == 0 && !ino.metadataOnly &&
		ino.mode&disk.StatTypeMask == disk.StatTypeReg && ino.size > 0 {
		if ino.directData != nil {
			data = ino.directData
		} else if fsys.spool != nil {
			data = io.NewSectionReader(fsys.spool, ino.spoolOff, int64(ino.size))
		}
	}

	return &erofsEntry{
		mode:          ino.mode,
		uid:           ino.uid,
		gid:           ino.gid,
		mtime:         ino.mtime,
		mtimeNs:       ino.mtimeNs,
		nlink:         nlink,
		size:          ino.size,
		rdev:          ino.rdev,
		name:          path.Base(entry.path),
		path:          entry.path,
		symTarget:     ino.linkTarget,
		chunks:        ino.chunks,
		contiguous:    ino.contiguous,
		metadataOnly:  ino.metadataOnly,
		data:          data,
		xattrs:        ino.xattrs,
		erofsFileType: modeToFileType(ino.mode),
	}
}

// setBlockSize sets the image block size. If already set to a different
// value, it returns an error. Safe to call multiple times with the same value.
func (fsys *Writer) setBlockSize(n int) error {
	if n < minBlockSize || n > maxBlockSize {
		return fmt.Errorf("mkfs: invalid block size %d: must be between %d and %d", n, minBlockSize, maxBlockSize)
	}
	if bits.OnesCount(uint(n)) != 1 {
		return fmt.Errorf("mkfs: invalid block size %d: must be a power of two", n)
	}
	if fsys.blockSize == 0 {
		fsys.blockSize = n
		return nil
	}
	if fsys.blockSize != n {
		return fmt.Errorf("mkfs: block size conflict: already %d, requested %d", fsys.blockSize, n)
	}
	return nil
}

// resolveBlockSize returns the block size, defaulting to 4096 if unset.
func (fsys *Writer) resolveBlockSize() int {
	if fsys.blockSize == 0 {
		fsys.blockSize = defaultBlockSize
	}
	return fsys.blockSize
}

// copyBuf returns a shared 32KB buffer for io.Copy operations.
func (fsys *Writer) copyBuf() []byte {
	if fsys.cpBuf == nil {
		fsys.cpBuf = make([]byte, 32*1024)
	}
	return fsys.cpBuf
}

// zeroPad returns a shared zero buffer sized to the resolved block size.
func (fsys *Writer) zeroPad() []byte {
	if fsys.padBuf == nil {
		fsys.padBuf = make([]byte, fsys.resolveBlockSize())
	}
	return fsys.padBuf
}

// chunksFromRanges converts DataRange entries into internal chunk entries.
// fileSize is the logical size of the file; the sum of all range Sizes must
// equal fileSize exactly, or an error is returned.
//
// The block size used is the Writer's resolved block size. DataRange.Device
// values are offset by 1 to produce chunk DeviceIDs: DataRange Device 0
// becomes chunk DeviceID 1 (the first extra device), matching the EROFS
// convention where DeviceID 0 is the primary image.
//
// Validation rules:
//   - sum(Size) == fileSize; a mismatch is rejected.
//   - r.Size > 0 for every entry.
//   - Hole entries (Offset == -1) emit [builder.NullPhysicalBlock] chunks.
//     Hole Size must be block-aligned for non-final entries; the final entry
//     may end mid-block to match the file tail.
//   - For data entries: r.Offset >= 0 and block-aligned; r.Device == 0.
//   - For non-final data entries: r.Size must be a multiple of blockSize.
//     The final entry may have a partial last block to match the file tail.
func (fsys *Writer) chunksFromRanges(ranges []DataRange, fileSize int64) ([]builder.Chunk, error) {
	blockSize := uint64(fsys.resolveBlockSize())

	// Validate total coverage first.
	var total int64
	for _, r := range ranges {
		total += r.Size
	}
	if total != fileSize {
		return nil, fmt.Errorf("DataRange total size %d does not match file size %d", total, fileSize)
	}

	last := len(ranges) - 1
	var chunks []builder.Chunk
	for i, r := range ranges {
		if r.Size <= 0 {
			return nil, fmt.Errorf("DataRange[%d]: non-positive Size %d", i, r.Size)
		}
		// Non-final entries must be block-aligned in size; the final entry may
		// end mid-block to match the file tail.
		if i < last && uint64(r.Size)%blockSize != 0 {
			return nil, fmt.Errorf("DataRange[%d]: non-final Size %d is not block-aligned (block size %d)", i, r.Size, blockSize)
		}
		if r.Offset == holeOffset {
			// Hole: emit NullPhysicalBlock chunks covering the hole span.
			totalBlocks := (uint64(r.Size) + blockSize - 1) / blockSize
			for totalBlocks > 0 {
				count := totalBlocks
				if count > 65535 {
					count = 65535
				}
				chunks = append(chunks, builder.Chunk{
					PhysicalBlock: builder.NullPhysicalBlock,
					Count:         uint16(count),
				})
				totalBlocks -= count
			}
			continue
		}
		if r.Offset < 0 {
			return nil, fmt.Errorf("DataRange[%d]: negative Offset %d", i, r.Offset)
		}
		if uint64(r.Offset)%blockSize != 0 {
			return nil, fmt.Errorf("DataRange[%d]: Offset %d is not block-aligned (block size %d)", i, r.Offset, blockSize)
		}
		// Non-EROFS sources register exactly one device via DeviceBlocks();
		// only Device=0 is valid. Device=0xFFFF would also wrap deviceID to 0
		// (the primary image), producing an invalid mapping.
		if r.Device != 0 {
			return nil, fmt.Errorf("DataRange[%d]: Device %d out of range (source declared one device, only Device=0 is valid)", i, r.Device)
		}
		deviceID := r.Device + 1
		startBlock := uint64(r.Offset) / blockSize
		totalBlocks := (uint64(r.Size) + blockSize - 1) / blockSize
		for totalBlocks > 0 {
			count := totalBlocks
			if count > 65535 {
				count = 65535
			}
			chunks = append(chunks, builder.Chunk{
				PhysicalBlock: startBlock,
				Count:         uint16(count),
				DeviceID:      deviceID,
			})
			startBlock += count
			totalBlocks -= count
		}
	}
	return chunks, nil
}

// ensureSpool lazily creates the spool temp file.
func (fsys *Writer) ensureSpool() error {
	if fsys.spool != nil {
		return nil
	}
	tmp, err := os.CreateTemp(fsys.tempDir, "erofs-mkfs-*")
	if err != nil {
		return fmt.Errorf("mkfs: create spool: %w", err)
	}
	_ = os.Remove(tmp.Name()) // unlink immediately; fd keeps data accessible
	fsys.spool = tmp
	return nil
}

// lookup resolves name to its fsEntry. Callers mutate e.ino to affect the
// shared fsInode, so changes are visible through all hard-linked names.
func (fsys *Writer) lookup(name string) (*fsEntry, error) {
	return fsys.resolveEntry("lookup", name)
}

// resolveEntry looks up name in byPath. On a miss it calls classifyMiss to
// distinguish a genuinely absent path (ErrNotExist) from one that traverses
// through a non-directory component (ErrNotDirectory).
func (fsys *Writer) resolveEntry(op, name string) (*fsEntry, error) {
	name = cleanPath(name)
	if e, ok := fsys.byPath[name]; ok {
		return e, nil
	}
	if err := fsys.classifyMiss(name); err != nil {
		return nil, &fs.PathError{Op: op, Path: name, Err: err}
	}
	return nil, &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
}

// classifyMiss returns ErrNotDirectory if an existing ancestor of name is not
// a directory, or nil if the path is simply absent.
func (fsys *Writer) classifyMiss(name string) error {
	for dir := path.Dir(name); dir != "/" && dir != "."; dir = path.Dir(dir) {
		e, ok := fsys.byPath[dir]
		if !ok {
			continue
		}
		if e.ino.mode&disk.StatTypeMask != disk.StatTypeDir {
			return ErrNotDirectory
		}
		// Nearest existing ancestor is a directory: the path is just absent.
		return nil
	}
	return nil
}

// closeDataFile pads the data file to a block boundary and records chunks.
func (f *File) closeDataFile() error {
	if f.written == 0 {
		return nil
	}

	// Pad to block boundary.
	bs := int64(f.fs.resolveBlockSize())
	rem := f.fs.dataOff % bs
	if rem != 0 {
		padSize := bs - rem
		n, err := f.fs.dataFile.Write(f.fs.zeroPad()[:padSize])
		f.fs.dataOff += int64(n)
		if err != nil {
			return fmt.Errorf("mkfs: pad data file: %w", err)
		}
	}

	// Compute chunks from the start offset and written bytes.
	startBlock := uint64(f.dataStartOff) / uint64(f.fs.resolveBlockSize())
	totalBlocks := (uint64(f.written) + uint64(f.fs.resolveBlockSize()) - 1) / uint64(f.fs.resolveBlockSize())

	for totalBlocks > 0 {
		count := totalBlocks
		if count > 65535 {
			count = 65535
		}
		f.entry.ino.chunks = append(f.entry.ino.chunks, builder.Chunk{
			PhysicalBlock: startBlock,
			Count:         uint16(count),
			DeviceID:      1,
		})
		startBlock += count
		totalBlocks -= count
	}

	return nil
}

// --- Constants ---

const (
	minBlockSize     = 512
	defaultBlockSize = 4096
	nullAddr         = 0xFFFFFFFF // marks a hole/sparse chunk

	// Overlay whiteout markers (AUFS convention used by OCI layers).
	whiteoutPrefix = ".wh."
	opaqueWhiteout = ".wh..wh..opq"
)

// blkBits returns log2(blockSize).
func blkBits(blockSize int) uint8 {
	return uint8(bits.TrailingZeros(uint(blockSize)))
}
