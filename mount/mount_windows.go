/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package mount

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Microsoft/hcsshim"
	"github.com/pkg/errors"
	"golang.org/x/sys/windows"
)

var (
	// ErrNotImplementOnWindows is returned when an action is not implemented for windows
	ErrNotImplementOnWindows = errors.New("not implemented under windows")
)

// Mount to the provided target
func (m *Mount) Mount(target string) (retErr error) {
	home, layerID := filepath.Split(m.Source)

	parentLayerPaths, err := m.GetParentPaths()
	if err != nil {
		return err
	}

	var di = hcsshim.DriverInfo{
		HomeDir: home,
	}

	if err = hcsshim.ActivateLayer(di, layerID); err != nil {
		return errors.Wrapf(err, "failed to activate layer %s", m.Source)
	}
	defer func() {
		if retErr != nil {
			hcsshim.DeactivateLayer(di, layerID)
		}
	}()

	if err = hcsshim.PrepareLayer(di, layerID, parentLayerPaths); err != nil {
		return errors.Wrapf(err, "failed to prepare layer %s", m.Source)
	}
	layerPath, err := hcsshim.GetLayerMountPath(di, layerID)
	if err != nil {
		return errors.Wrapf(err, "failed to get mount path for layer %s", m.Source)
	}

	if !strings.HasPrefix(layerPath, "\\\\?\\") {
		layerPath = filepath.Join(layerPath, "Files")
		if _, err := os.Lstat(layerPath); err != nil {
			if !os.IsNotExist(err) {
				return errors.Wrapf(err, "failed to find Files dir")
			}
			if err := os.Mkdir(layerPath, 0755); err != nil {
				return errors.Wrap(err, "failed to create Files dir")
			}
		}
		if err := os.Remove(target); err != nil {
			return errors.Wrapf(err, "remove target prior to mounting %q", target)
		}
		if err := os.Symlink(layerPath, target); err != nil {
			return errors.Wrapf(err, "failed to mount layer %q at %q", m.Source, target)
		}
	} else {
		target = filepath.Clean(target) + string(filepath.Separator)
		targetp, err := syscall.UTF16PtrFromString(target)
		if err != nil {
			return err
		}

		volName := filepath.Clean(layerPath) + string(filepath.Separator)
		volNamep, err := syscall.UTF16PtrFromString(volName)
		if err != nil {
			return err
		}

		if err := windows.SetVolumeMountPoint(targetp, volNamep); err != nil {
			return errors.Wrapf(err, "failed to mount layer %q at %q, volume: %q", m.Source, target, volName)
		}
	}

	for strings.HasSuffix(target, string(os.PathSeparator)) {
		target = target[:len(target)-1]
	}

	idf, err := os.Create(target + ":layerid")
	if err != nil {
		return err
	}
	defer idf.Close()

	_, err = idf.Write([]byte(m.Source))
	if err != nil {
		return err
	}
	return nil
}

// ParentLayerPathsFlag is the options flag used to represent the JSON encoded
// list of parent layers required to use the layer
const ParentLayerPathsFlag = "parentLayerPaths="

// GetParentPaths of the mount
func (m *Mount) GetParentPaths() ([]string, error) {
	var parentLayerPaths []string
	for _, option := range m.Options {
		if strings.HasPrefix(option, ParentLayerPathsFlag) {
			err := json.Unmarshal([]byte(option[len(ParentLayerPathsFlag):]), &parentLayerPaths)
			if err != nil {
				return nil, errors.Wrap(err, "failed to unmarshal parent layer paths from mount")
			}
		}
	}
	return parentLayerPaths, nil
}

// Unmount the mount at the provided path
func Unmount(mount string, flags int) error {
	fi, err := os.Lstat(mount)
	if err != nil {
		return errors.Wrapf(err, "unable to find mounted volume %s", mount)
	}

	layerPathb, err := ioutil.ReadFile(mount + ":layerid")
	if err != nil {
		return err
	}
	layerPath := string(layerPathb)

	var (
		home, layerID = filepath.Split(layerPath)
		di            = hcsshim.DriverInfo{
			HomeDir: home,
		}
	)

	if fi.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(mount); err != nil {
			return errors.Wrap(err, "failed to delete mount")
		}
	} else {
		mount = filepath.Clean(mount) + string(filepath.Separator)
		mountp, err := syscall.UTF16PtrFromString(mount)
		if err != nil {
			return err
		}

		const volumeNameLen = 50
		volumeNamep := make([]uint16, volumeNameLen)
		volumeNamep[0] = 0

		if err := windows.GetVolumeNameForVolumeMountPoint(mountp, &volumeNamep[0], volumeNameLen); err != nil {
			return errors.Wrapf(err, "unable to find mounted volume %s", mount)
		}

		if err := windows.DeleteVolumeMountPoint(&volumeNamep[0]); err != nil {
			return errors.Wrapf(err, "unable to delete mounted volume %s", mount)
		}
	}

	if err := hcsshim.UnprepareLayer(di, layerID); err != nil {
		return errors.Wrapf(err, "failed to unprepare layer %s", mount)
	}
	if err := hcsshim.DeactivateLayer(di, layerID); err != nil {
		return errors.Wrapf(err, "failed to deactivate layer %s", mount)
	}

	return nil
}

// UnmountAll unmounts from the provided path
func UnmountAll(mount string, flags int) error {
	return Unmount(mount, flags)
}
