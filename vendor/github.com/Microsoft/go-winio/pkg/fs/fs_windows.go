package fs

import (
	"errors"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	// ErrInvalidPath is returned when the location of a file path doesn't begin with a driver letter.
	ErrInvalidPath = errors.New("the path provided to GetFileSystemType must start with a drive letter")
)

// GetFileSystemType obtains the type of a file system through GetVolumeInformation.
// https://msdn.microsoft.com/en-us/library/windows/desktop/aa364993(v=vs.85).aspx
func GetFileSystemType(path string) (fsType string, hr error) {
	drive := filepath.VolumeName(path)
	if len(drive) != 2 {
		return "", ErrInvalidPath
	}

	var (
		modkernel32              = windows.NewLazySystemDLL("kernel32.dll")
		procGetVolumeInformation = modkernel32.NewProc("GetVolumeInformationW")
		buf                      = make([]uint16, 255)
		size                     = windows.MAX_PATH + 1
	)
	drive += `\`
	n := uintptr(unsafe.Pointer(nil))
	r0, _, _ := syscall.Syscall9(procGetVolumeInformation.Addr(), 8, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(drive))), n, n, n, n, n, uintptr(unsafe.Pointer(&buf[0])), uintptr(size), 0)
	if int32(r0) < 0 {
		hr = syscall.Errno(win32FromHresult(r0))
	}
	fsType = windows.UTF16ToString(buf)
	return
}

// win32FromHresult is a helper function to get the win32 error code from an HRESULT.
func win32FromHresult(hr uintptr) uintptr {
	if hr&0x1fff0000 == 0x00070000 {
		return hr & 0xffff
	}
	return hr
}
