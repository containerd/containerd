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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Microsoft/hcsshim"
)

const sourceStreamName = "containerd.io-source"

var (
	// ErrNotImplementOnWindows is returned when an action is not implemented for windows
	ErrNotImplementOnWindows = errors.New("not implemented under windows")
)

// Mount to the provided target.
func (m *Mount) mount(target string) error {
	if m.Type == "bind" {
		if err := m.bindMount(target); err != nil {
			return fmt.Errorf("failed to bind-mount to %s: %w", target, err)
		}
		return nil
	}

	if m.Type != "windows-layer" {
		return fmt.Errorf("invalid windows mount type: '%s'", m.Type)
	}

	home, layerID := filepath.Split(m.Source)

	parentLayerPaths, err := m.GetParentPaths()
	if err != nil {
		return err
	}

	var di = hcsshim.DriverInfo{
		HomeDir: home,
	}

	if err = hcsshim.ActivateLayer(di, layerID); err != nil {
		return fmt.Errorf("failed to activate layer %s: %w", m.Source, err)
	}
	defer func() {
		if err != nil {
			hcsshim.DeactivateLayer(di, layerID)
		}
	}()

	if err = hcsshim.PrepareLayer(di, layerID, parentLayerPaths); err != nil {
		return fmt.Errorf("failed to prepare layer %s: %w", m.Source, err)
	}
	defer func() {
		if err != nil {
			hcsshim.UnprepareLayer(di, layerID)
		}
	}()

	volume, err := hcsshim.GetLayerMountPath(di, layerID)
	if err != nil {
		return fmt.Errorf("failed to get volume path for layer %s: %w", m.Source, err)
	}

	if err = setVolumeMountPoint(target, volume); err != nil {
		return fmt.Errorf("failed to set volume mount path for layer %s: %w", m.Source, err)
	}
	defer func() {
		if err != nil {
			deleteVolumeMountPoint(target)
		}
	}()

	// Add an Alternate Data Stream to record the layer source.
	// See https://docs.microsoft.com/en-au/archive/blogs/askcore/alternate-data-streams-in-ntfs
	// for details on Alternate Data Streams.
	if err = os.WriteFile(filepath.Clean(target)+":"+sourceStreamName, []byte(m.Source), 0666); err != nil {
		return fmt.Errorf("failed to record source for layer %s: %w", m.Source, err)
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
				return nil, fmt.Errorf("failed to unmarshal parent layer paths from mount: %w", err)
			}
		}
	}
	return parentLayerPaths, nil
}

// Unmount the mount at the provided path
func Unmount(mount string, flags int) error {
	mount = filepath.Clean(mount)

	// Helpfully, both reparse points and symlinks look like links to Go
	// Less-helpfully, ReadLink cannot return \\?\Volume{GUID} for a volume mount,
	// and ends up returning the directory we gave it for some reason.
	if mountTarget, err := os.Readlink(mount); err != nil {
		// Not a mount point.
		// This isn't an error, per the EINVAL handling in the Linux version
		return nil
	} else if mount != filepath.Clean(mountTarget) {
		// Directory symlink
		if err := bindUnmount(mount); err != nil {
			return fmt.Errorf("failed to bind-unmount from %s: %w", mount, err)
		}
		return nil
	}

	layerPathb, err := os.ReadFile(mount + ":" + sourceStreamName)

	if err != nil {
		return fmt.Errorf("failed to retrieve source for layer %s: %w", mount, err)
	}

	layerPath := string(layerPathb)

	if err := deleteVolumeMountPoint(mount); err != nil {
		return fmt.Errorf("failed failed to release volume mount path for layer %s: %w", mount, err)
	}

	var (
		home, layerID = filepath.Split(layerPath)
		di            = hcsshim.DriverInfo{
			HomeDir: home,
		}
	)

	if err := hcsshim.UnprepareLayer(di, layerID); err != nil {
		return fmt.Errorf("failed to unprepare layer %s: %w", mount, err)
	}

	if err := hcsshim.DeactivateLayer(di, layerID); err != nil {
		return fmt.Errorf("failed to deactivate layer %s: %w", mount, err)
	}

	return nil
}

// UnmountAll unmounts from the provided path
func UnmountAll(mount string, flags int) error {
	if mount == "" {
		// This isn't an error, per the EINVAL handling in the Linux version
		return nil
	}

	return Unmount(mount, flags)
}

// UnmountRecursive unmounts from the provided path
func UnmountRecursive(mount string, flags int) error {
	return UnmountAll(mount, flags)
}

func (m *Mount) bindMount(target string) error {
	for _, option := range m.Options {
		if option == "ro" {
			return fmt.Errorf("read-only bind mount: %w", ErrNotImplementOnWindows)
		}
	}

	if err := os.Remove(target); err != nil {
		return err
	}

	// TODO: We don't honour the Read-Only flag.
	// It's possible that Windows simply lacks this.
	return os.Symlink(m.Source, target)
}

func bindUnmount(target string) error {
	return os.Remove(target)
}
