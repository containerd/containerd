package osversion

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// OSVersion is a wrapper for Windows version information
// https://msdn.microsoft.com/en-us/library/windows/desktop/ms724439(v=vs.85).aspx
type OSVersion struct {
	Version      uint32
	MajorVersion uint8
	MinorVersion uint8
	Build        uint16
}

// https://msdn.microsoft.com/en-us/library/windows/desktop/ms724833(v=vs.85).aspx
type osVersionInfoEx struct {
	OSVersionInfoSize uint32
	MajorVersion      uint32
	MinorVersion      uint32
	BuildNumber       uint32
	PlatformID        uint32
	CSDVersion        [128]uint16
	ServicePackMajor  uint16
	ServicePackMinor  uint16
	SuiteMask         uint16
	ProductType       byte
	Reserve           byte
}

// Get gets the operating system version on Windows.
// The calling application must be manifested to get the correct version information.
func Get() OSVersion {
	var err error
	osv := OSVersion{}
	osv.Version, err = windows.GetVersion()
	if err != nil {
		// GetVersion never fails.
		panic(err)
	}
	osv.MajorVersion = uint8(osv.Version & 0xFF)
	osv.MinorVersion = uint8(osv.Version >> 8 & 0xFF)
	osv.Build = uint16(osv.Version >> 16)
	return osv
}

func (osv OSVersion) ToString() string {
	return fmt.Sprintf("%d.%d.%d", osv.MajorVersion, osv.MinorVersion, osv.Build)
}
