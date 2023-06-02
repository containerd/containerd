//go:build windows
// +build windows

package cimfs

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio/pkg/guid"
	hcsschema "github.com/Microsoft/hcsshim/internal/hcs/schema2"
	"github.com/Microsoft/hcsshim/internal/winapi"
	"github.com/pkg/errors"
)

// This package provides simple go wrappers on top of the win32 CIMFS mount APIs. The mounting/unmount of cim
// layers is done by the cim mount functions in internal/wclayer/cim package.

type MountError struct {
	Cim        string
	Op         string
	VolumeGUID guid.GUID
	Err        error
}

func (e *MountError) Error() string {
	s := "cim " + e.Op
	if e.Cim != "" {
		s += " " + e.Cim
	}
	s += " " + e.VolumeGUID.String() + ": " + e.Err.Error()
	return s
}

func MountWithFlags(cimPath string, mountFlags uint32) (string, error) {
	layerGUID, err := guid.NewV4()
	if err != nil {
		return "", &MountError{Cim: cimPath, Op: "Mount", Err: err}
	}
	if err := winapi.CimMountImage(filepath.Dir(cimPath), filepath.Base(cimPath), mountFlags, &layerGUID); err != nil {
		return "", &MountError{Cim: cimPath, Op: "Mount", VolumeGUID: layerGUID, Err: err}
	}
	return fmt.Sprintf("\\\\?\\Volume{%s}\\", layerGUID.String()), nil
}

// Mount mounts the cim at path `cimPath` and returns the mount location of that cim.  This method uses the
// `CimMountFlagCacheRegions` mount flag when mounting the cim, if some other mount flag is desired use the
// `MountWithFlags` method.
func Mount(cimPath string) (string, error) {
	return MountWithFlags(cimPath, hcsschema.CimMountFlagCacheFiles)
}

// Unmount unmounts the cim at mounted at path `volumePath`.
func Unmount(volumePath string) error {
	// The path is expected to be in the \\?\Volume{GUID}\ format
	if volumePath[len(volumePath)-1] != '\\' {
		volumePath += "\\"
	}

	if !(strings.HasPrefix(volumePath, "\\\\?\\Volume{") && strings.HasSuffix(volumePath, "}\\")) {
		return errors.Errorf("volume path %s is not in the expected format", volumePath)
	}

	trimmedStr := strings.TrimPrefix(volumePath, "\\\\?\\Volume{")
	trimmedStr = strings.TrimSuffix(trimmedStr, "}\\")

	volGUID, err := guid.FromString(trimmedStr)
	if err != nil {
		return errors.Wrapf(err, "guid parsing failed for %s", trimmedStr)
	}

	if err := winapi.CimDismountImage(&volGUID); err != nil {
		return &MountError{VolumeGUID: volGUID, Op: "Unmount", Err: err}
	}

	return nil
}
