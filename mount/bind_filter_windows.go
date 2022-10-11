package mount

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

//go:generate go run github.com/Microsoft/go-winio/tools/mkwinsyscall -output zsyscall_windows.go ./*.go
//sys BfSetupFilter(jobHandle windows.Handle, flags uint32, virtRootPath *uint16, virtTargetPath *uint16, virtExceptions **uint16, virtExceptionPathCount uint32) (hr error) = bindfltapi.BfSetupFilter?
//sys BfRemoveMapping(jobHandle windows.Handle, virtRootPath *uint16)  (hr error) = bindfltapi.BfRemoveMapping?
//sys BfGetMappings(flags uint32, jobHandle windows.Handle, virtRootPath *uint16, sid *windows.SID, bufferSize *uint32, outBuffer uintptr)  (hr error) = bindfltapi.BfGetMappings?

// BfSetupFilter flags. See:
// https://github.com/microsoft/BuildXL/blob/a6dce509f0d4f774255e5fbfb75fa6d5290ed163/Public/Src/Utilities/Native/Processes/Windows/NativeContainerUtilities.cs#L193-L240
const (
	BINDFLT_FLAG_READ_ONLY_MAPPING uint32 = 0x00000001
	// Generates a merged binding, mapping target entries to the virtualization root.
	BINDFLT_FLAG_MERGED_BIND_MAPPING uint32 = 0x00000002
	// Use the binding mapping attached to the mapped-in job object (silo) instead of the default global mapping.
	BINDFLT_FLAG_USE_CURRENT_SILO_MAPPING uint32 = 0x00000004
	BINDFLT_FLAG_REPARSE_ON_FILES         uint32 = 0x00000008
	// Skips checks on file/dir creation inside a non-merged, read-only mapping.
	// Only usable when READ_ONLY_MAPPING is set.
	BINDFLT_FLAG_SKIP_SHARING_CHECK uint32 = 0x00000010
	BINDFLT_FLAG_CLOUD_FILES_ECPS   uint32 = 0x00000020
	// Tells bindflt to fail mapping with STATUS_INVALID_PARAMETER if a mapping produces
	// multiple targets.
	BINDFLT_FLAG_NO_MULTIPLE_TARGETS uint32 = 0x00000040
	// Turns on caching by asserting that the backing store for name mappings is immutable.
	BINDFLT_FLAG_IMMUTABLE_BACKING              uint32 = 0x00000080
	BINDFLT_FLAG_PREVENT_CASE_SENSITIVE_BINDING uint32 = 0x00000100
	// Tells bindflt to fail with STATUS_OBJECT_PATH_NOT_FOUND when a mapping is being added
	// but its parent paths (ancestors) have not already been added.
	BINDFLT_FLAG_EMPTY_VIRT_ROOT         uint32 = 0x00000200
	BINDFLT_FLAG_NO_REPARSE_ON_ROOT      uint32 = 0x10000000
	BINDFLT_FLAG_BATCHED_REMOVE_MAPPINGS uint32 = 0x20000000
)

const (
	BINDFLT_GET_MAPPINGS_FLAG_VOLUME uint32 = 0x00000001
	BINDFLT_GET_MAPPINGS_FLAG_SILO   uint32 = 0x00000002
	BINDFLT_GET_MAPPINGS_FLAG_USER   uint32 = 0x00000004
)

// ApplyFileBinding creates a global mount of the source in root, with an optional
// read only flag.
// The bind filter allows us to create mounts of directories and volumes. By default it allows
// us to mount multiple sources inside a single root, acting as an overlay. Files from the
// second source will superscede the first source that was mounted.
// This function disables this behavior and sets the BINDFLT_FLAG_NO_MULTIPLE_TARGETS flag
// on the mount.
func ApplyFileBinding(root, source string, readOnly bool) error {
	// The parent directory needs to exist for the bind to work. MkdirAll stats and
	// returns nil if the directory exists internally so we should be fine to mkdirall
	// every time.
	if err := os.MkdirAll(filepath.Dir(root), 0); err != nil {
		return err
	}

	if strings.Contains(source, "Volume{") && !strings.HasSuffix(source, "\\") {
		// Add trailing slash to volumes, otherwise we get an error when binding it to
		// a folder.
		source = source + "\\"
	}

	rootPtr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return err
	}

	targetPtr, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	flags := BINDFLT_FLAG_NO_MULTIPLE_TARGETS
	if readOnly {
		flags |= BINDFLT_FLAG_READ_ONLY_MAPPING
	}

	// Set the job handle to 0 to create a global mount.
	if err := BfSetupFilter(
		0,
		flags,
		rootPtr,
		targetPtr,
		nil,
		0,
	); err != nil {
		return fmt.Errorf("failed to bind target %q to root %q: %w", source, root, err)
	}
	return nil
}

func RemoveFileBinding(root string) error {
	rootPtr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return fmt.Errorf("converting path to utf-16: %w", err)
	}

	if err := BfRemoveMapping(0, rootPtr); err != nil {
		return fmt.Errorf("removing file binding: %w", err)
	}
	return nil
}

// mappingEntry holds information about where in the response buffer we can
// find information about the virtual root (the mount point) and the targets (sources)
// that get mounted, as well as the flags used to bind the targets to the virtual root.
type mappingEntry struct {
	VirtRootLength      uint32
	VirtRootOffset      uint32
	Flags               uint32
	NumberOfTargets     uint32
	TargetEntriesOffset uint32
}

type mappingTargetEntry struct {
	TargetRootLength uint32
	TargetRootOffset uint32
}

// getMappingsResponseHeader represents the first 12 bytes of the BfGetMappings() response.
// It gives us the size of the buffer, the status of the call and the number of mappings.
// A response
type getMappingsResponseHeader struct {
	Size         uint32
	Status       uint32
	MappingCount uint32
}

type BindMapping struct {
	MountPoint string
	Flags      uint32
	Targets    []string
}

func decodeEntry(buffer []byte) (string, error) {
	name := make([]uint16, len(buffer)/2)
	err := binary.Read(bytes.NewReader(buffer), binary.LittleEndian, &name)
	if err != nil {
		return "", fmt.Errorf("decoding name: %w", err)
	}
	return string(utf16.Decode(name)), nil
}

func getTargetsFromBuffer(buffer []byte, offset, count int) ([]string, error) {
	if len(buffer) < offset+count*6 {
		return nil, fmt.Errorf("invalid buffer")
	}

	targets := make([]string, count)
	for i := 0; i < count; i++ {
		entryBuf := buffer[offset+i*8 : offset+i*8+8]
		tgt := *(*mappingTargetEntry)(unsafe.Pointer(&entryBuf[0]))
		if len(buffer) < int(tgt.TargetRootOffset)+int(tgt.TargetRootLength) {
			return nil, fmt.Errorf("invalid buffer")
		}
		decoded, err := decodeEntry(buffer[tgt.TargetRootOffset : uint32(tgt.TargetRootOffset)+uint32(tgt.TargetRootLength)])
		if err != nil {
			return nil, fmt.Errorf("decoding name: %w", err)
		}
		decoded, err = getFinalPath(decoded)
		if err != nil {
			return nil, fmt.Errorf("fetching final path: %w", err)
		}

		targets[i] = decoded
	}
	return targets, nil
}

func getFinalPath(pth string) (string, error) {
	// BfGetMappings returns VOLUME_NAME_NT paths like \Device\HarddiskVolume2\ProgramData.
	// These can be accessed by prepending \\.\GLOBALROOT to the path. We use this to get the
	// DOS paths for these files.
	if strings.HasPrefix(pth, `\Device`) {
		pth = `\\.\GLOBALROOT` + pth
	}

	han, err := getFileHandle(pth)
	if err != nil {
		return "", fmt.Errorf("fetching file handle: %w", err)
	}

	buf := make([]uint16, 100)
	var flags uint32 = 0x0
	for {
		n, err := windows.GetFinalPathNameByHandle(windows.Handle(han), &buf[0], uint32(len(buf)), flags)
		if err != nil {
			// if we mounted a volume that does not also have a drive letter assigned, attempting to
			// fetch the VOLUME_NAME_DOS will fail with os.ErrNotExist. Attempt to get the VOLUME_NAME_GUID.
			if errors.Is(err, os.ErrNotExist) && flags != 0x1 {
				flags = 0x1
				continue
			}
			return "", fmt.Errorf("getting final path name: %w", err)
		}
		if n < uint32(len(buf)) {
			break
		}
		buf = make([]uint16, n)
	}
	finalPath := syscall.UTF16ToString(buf)
	// We got VOLUME_NAME_DOS, we need to strip away some leading slashes.
	// Leave unchanged if we ended up requesting VOLUME_NAME_GUID
	if len(finalPath) > 4 && finalPath[:4] == `\\?\` && flags == 0x0 {
		finalPath = finalPath[4:]
		if len(finalPath) > 3 && finalPath[:3] == `UNC` {
			// return path like \\server\share\...
			finalPath = `\` + finalPath[3:]
		}
	}

	return finalPath, nil
}

func getBindMappingFromBuffer(buffer []byte, entry mappingEntry) (BindMapping, error) {
	if len(buffer) < int(entry.VirtRootOffset)+int(entry.VirtRootLength) {
		return BindMapping{}, fmt.Errorf("invalid buffer")
	}

	src, err := decodeEntry(buffer[entry.VirtRootOffset : entry.VirtRootOffset+uint32(entry.VirtRootLength)])
	if err != nil {
		return BindMapping{}, fmt.Errorf("decoding entry: %w", err)
	}
	targets, err := getTargetsFromBuffer(buffer, int(entry.TargetEntriesOffset), int(entry.NumberOfTargets))
	if err != nil {
		return BindMapping{}, fmt.Errorf("fetching targets: %w", err)
	}

	src, err = getFinalPath(src)
	if err != nil {
		return BindMapping{}, fmt.Errorf("fetching final path: %w", err)
	}

	return BindMapping{
		Flags:      entry.Flags,
		Targets:    targets,
		MountPoint: src,
	}, nil
}

func getFileHandle(pth string) (syscall.Handle, error) {
	info, err := os.Lstat(pth)
	if err != nil {
		return 0, fmt.Errorf("accessing file: %w", err)
	}
	p, err := syscall.UTF16PtrFromString(pth)
	if err != nil {
		return 0, err
	}
	attrs := uint32(syscall.FILE_FLAG_BACKUP_SEMANTICS)
	if info.Mode()&os.ModeSymlink != 0 {
		attrs |= syscall.FILE_FLAG_OPEN_REPARSE_POINT
	}
	h, err := syscall.CreateFile(p, 0, 0, nil, syscall.OPEN_EXISTING, attrs, 0)
	if err != nil {
		return 0, err
	}
	return h, nil
}

// GetBindMappings returns a list of bind mappings that have their root on a
// particular volume. The volumePath parameter can be any path that exists on
// a volume. For example, if a number of mappings are created in C:\ProgramData\test,
// to get a list of those mappings, the volumePath parameter would have to be set to
// C:\ or the VOLUME_NAME_GUID notation of C:\ (\\?\Volume{GUID}\), or any child
// path that exists.
func GetBindMappings(volumePath string) ([]BindMapping, error) {
	rootPtr, err := windows.UTF16PtrFromString(volumePath)
	if err != nil {
		return nil, err
	}

	var flags uint32 = BINDFLT_GET_MAPPINGS_FLAG_VOLUME
	// allocate a large buffer for results
	var outBuffSize uint32 = 256 * 1024
	buf := make([]byte, outBuffSize)

	if err := BfGetMappings(flags, 0, rootPtr, nil, &outBuffSize, uintptr(unsafe.Pointer(&buf[0]))); err != nil {
		return nil, err
	}

	if outBuffSize < 12 {
		return nil, fmt.Errorf("invalid buffer returned")
	}

	result := buf[:outBuffSize]

	// The first 12 bytes are the three uint32 fields in getMappingsResponseHeader{}
	headerBuffer := result[:12]
	// The alternative to using unsafe and casting it to the above defined structures, is to manually
	// parse the fields. Not too terrible, but not sure it'd worth the trouble.
	header := *(*getMappingsResponseHeader)(unsafe.Pointer(&headerBuffer[0]))

	if header.MappingCount == 0 {
		// no mappings
		return []BindMapping{}, nil
	}

	mappingsBuffer := result[12 : int(unsafe.Sizeof(mappingEntry{}))*int(header.MappingCount)]
	// Get a pointer to the first mapping in the slice
	mappingsPointer := (*mappingEntry)(unsafe.Pointer(&mappingsBuffer[0]))
	// Get slice of mappings
	mappings := unsafe.Slice(mappingsPointer, header.MappingCount)

	mappingEntries := make([]BindMapping, header.MappingCount)
	for i := 0; i < int(header.MappingCount); i++ {
		bindMapping, err := getBindMappingFromBuffer(result, mappings[i])
		if err != nil {
			return nil, fmt.Errorf("fetching bind mappings: %w", err)
		}
		mappingEntries[i] = bindMapping
	}

	return mappingEntries, nil
}
