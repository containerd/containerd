//go:build windows
// +build windows

package cimfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/hcsshim/internal/winapi"
	"github.com/pkg/errors"
	"golang.org/x/sys/windows"
)

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

// Mount mounts the given cim at a volume with given GUID. Returns the full volume
// path if mount is successful.
func Mount(cimPath string, volumeGUID guid.GUID, mountFlags uint32) (string, error) {
	if err := winapi.CimMountImage(filepath.Dir(cimPath), filepath.Base(cimPath), mountFlags, &volumeGUID); err != nil {
		return "", &MountError{Cim: cimPath, Op: "Mount", VolumeGUID: volumeGUID, Err: err}
	}
	return fmt.Sprintf("\\\\?\\Volume{%s}\\", volumeGUID.String()), nil
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

// MountMergedBlockCIMs mounts the given merged BlockCIM (usually created with
// `MergeBlockCIMs`) at a volume with given GUID. The `sourceCIMs` MUST be identical
// to the `sourceCIMs` passed to `MergeBlockCIMs` when creating this merged CIM.
func MountMergedBlockCIMs(mergedCIM *BlockCIM, sourceCIMs []*BlockCIM, mountFlags uint32, volumeGUID guid.GUID) (string, error) {
	if !IsMergedCimSupported() {
		return "", fmt.Errorf("merged CIMs aren't supported on this OS version")
	} else if len(sourceCIMs) < 2 {
		return "", fmt.Errorf("need at least 2 source CIMs, got %d: %w", len(sourceCIMs), os.ErrInvalid)
	}

	switch mergedCIM.Type {
	case BlockCIMTypeDevice:
		mountFlags |= CimMountBlockDeviceCim
	case BlockCIMTypeSingleFile:
		mountFlags |= CimMountSingleFileCim
	default:
		return "", fmt.Errorf("invalid block CIM type `%d`", mergedCIM.Type)
	}

	for _, sCIM := range sourceCIMs {
		if sCIM.Type != mergedCIM.Type {
			return "", fmt.Errorf("source CIM (%s) type doesn't match with merged CIM type: %w", sCIM.String(), os.ErrInvalid)
		}
	}

	// win32 mount merged CIM API expects an array of all CIMs. 0th entry in the array
	// should be the merged CIM. All remaining entries should be the source CIM paths
	// in the same order that was used while creating the merged CIM.
	allcims := append([]*BlockCIM{mergedCIM}, sourceCIMs...)
	cimsToMerge := []winapi.CimFsImagePath{}
	for _, bcim := range allcims {
		// Trailing backslashes cause problems-remove those
		imageDir, err := windows.UTF16PtrFromString(strings.TrimRight(bcim.BlockPath, `\`))
		if err != nil {
			return "", fmt.Errorf("convert string to utf16: %w", err)
		}
		cimName, err := windows.UTF16PtrFromString(bcim.CimName)
		if err != nil {
			return "", fmt.Errorf("convert string to utf16: %w", err)
		}

		cimsToMerge = append(cimsToMerge, winapi.CimFsImagePath{
			ImageDir:  imageDir,
			ImageName: cimName,
		})
	}

	if err := winapi.CimMergeMountImage(uint32(len(cimsToMerge)), &cimsToMerge[0], mountFlags, &volumeGUID); err != nil {
		return "", &MountError{Cim: filepath.Join(mergedCIM.BlockPath, mergedCIM.CimName), Op: "MountMerged", Err: err}
	}
	return fmt.Sprintf("\\\\?\\Volume{%s}\\", volumeGUID.String()), nil
}
