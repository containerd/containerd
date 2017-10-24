package mount

import (
	"path/filepath"

	"github.com/Microsoft/hcsshim"
	"github.com/pkg/errors"
)

var (
	ErrNotImplementOnWindows = errors.New("not implemented under windows")
)

func (m *Mount) Mount(target string) error {
	// Windows cannot mount an indvidual layer, as the filesystem
	// filter requires all underlying layers to mount.
	return ErrNotImplementOnWindows
}

// All mounts all the provided mounts to the provided target
func All(mounts []Mount, target string) error {
	layerFolderPath := mounts[0].Source
	layerID := filepath.Base(layerFolderPath)

	var layerFolders []string
	for _, m := range mounts[1:] {
		layerFolders = append(layerFolders, m.Source)
	}

	var di = hcsshim.DriverInfo{
		Flavour: hcsshim.FilterDriver,
		HomeDir: filepath.Dir(layerFolderPath),
	}

	var err error
	if err = hcsshim.ActivateLayer(di, layerID); err != nil {
		return errors.Wrapf(err, "failed to activate layer %s", layerFolderPath)
	}
	defer func() {
		if err != nil {
			hcsshim.DeactivateLayer(di, layerID)
		}
	}()

	if err = hcsshim.PrepareLayer(di, layerID, layerFolders); err != nil {
		return errors.Wrapf(err, "failed to prepare layer %s", layerFolderPath)
	}
	defer func() {
		if err != nil {
			hcsshim.UnprepareLayer(di, layerID)
		}
	}()
	return nil
}

func Unmount(mount string, flags int) error {
	return ErrNotImplementOnWindows
}

func UnmountAll(mount string, flags int) error {
	layerID := filepath.Base(mount)

	var di = hcsshim.DriverInfo{
		Flavour: 1, // filter driver
		HomeDir: filepath.Dir(mount),
	}

	var err error
	if err = hcsshim.UnprepareLayer(di, layerID); err != nil {
		return errors.Wrapf(err, "failed to unprepare layer %s", mount)
	}
	if err = hcsshim.DeactivateLayer(di, layerID); err != nil {
		return errors.Wrapf(err, "failed to deactivate layer %s", mount)
	}

	return nil
}
