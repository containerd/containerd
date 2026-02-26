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

package erofs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/containerd/log"
	"github.com/containerd/platforms"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/internal/dmverity"
	"github.com/containerd/containerd/v2/internal/fsmount"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/errdefs"

	"golang.org/x/sys/unix"
)

var forceloop bool

type erofsMountHandler struct{}

// NewErofsMountHandler creates a new EROFS mount handler that supports dm-verity
func NewErofsMountHandler() mount.Handler {
	return &erofsMountHandler{}
}

func (h *erofsMountHandler) Mount(ctx context.Context, m mount.Mount, mp string, _ []mount.ActiveMount) (mount.ActiveMount, error) {
	if m.Type != "erofs" {
		return mount.ActiveMount{}, errdefs.ErrNotImplemented
	}

	var dmverityDevice string

	// Check for dmverity mode in mount options
	dmverityMode := "auto" // default
	for _, opt := range m.Options {
		if strings.HasPrefix(opt, "X-containerd.dmverity=") {
			dmverityMode = strings.TrimPrefix(opt, "X-containerd.dmverity=")
			break
		}
	}

	// Check if this layer has dm-verity metadata
	metadata, err := dmverity.ReadMetadata(m.Source)
	if err == nil && dmverityMode != "off" {
		log.G(ctx).WithField("source", m.Source).Debug("detected dm-verity metadata, setting up dm-verity device")

		supported, err := dmverity.IsSupported()
		if err != nil || !supported {
			return mount.ActiveMount{}, fmt.Errorf("layer requires dm-verity but system doesn't support it (dm_verity module not loaded): %w", err)
		}

		// Extract snapshot ID from source path
		// Path format: {root}/snapshots/{id}/layer.erofs
		snapshotID := filepath.Base(filepath.Dir(m.Source))
		deviceName := fmt.Sprintf("containerd-erofs-%s", snapshotID)
		devicePath := dmverity.DevicePath(deviceName)

		// Try to create dm-verity device first (avoids TOCTOU race)
		log.G(ctx).WithFields(log.Fields{
			"source":      m.Source,
			"device-name": deviceName,
			"hash-offset": metadata.HashOffset,
		}).Debug("opening dm-verity device")

		_, err = dmverity.Open(m.Source, deviceName, m.Source, metadata.RootHash, metadata.HashOffset, nil)
		if err != nil {
			if _, statErr := os.Stat(devicePath); statErr == nil {
				if verifyErr := dmverity.VerifyDevice(deviceName, metadata.RootHash); verifyErr != nil {
					return mount.ActiveMount{}, fmt.Errorf("existing dm-verity device %q verification failed: %w", deviceName, verifyErr)
				}
				log.G(ctx).WithField("device", devicePath).Debug("dm-verity device already exists and verified, reusing")
			} else {
				return mount.ActiveMount{}, fmt.Errorf("failed to open dm-verity device: %w", err)
			}
		} else {
			// Wait for device to appear
			for i := 0; i < 100; i++ {
				if _, err := os.Stat(devicePath); err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}

			// Verify device exists
			if _, err := os.Stat(devicePath); err != nil {
				dmverity.Close(deviceName)
				return mount.ActiveMount{}, fmt.Errorf("dm-verity device %q not found after creation: %w", devicePath, err)
			}

			log.G(ctx).WithField("device", devicePath).Info("dm-verity device created successfully")
		}

		dmverityDevice = deviceName
		m.Source = devicePath
	}
	// else: no metadata file, proceed with regular EROFS mount

	filteredOptions := make([]string, 0, len(m.Options))
	for _, v := range m.Options {
		// Skip loop option (handled by loop device setup) and dmverity mode option (already processed)
		if v == "loop" || strings.HasPrefix(v, "X-containerd.dmverity=") {
			continue
		}
		filteredOptions = append(filteredOptions, v)
	}
	m.Options = filteredOptions

	if err := os.MkdirAll(mp, 0700); err != nil {
		if dmverityDevice != "" {
			dmverity.Close(dmverityDevice)
		}
		return mount.ActiveMount{}, err
	}

	err = unix.ENOTBLK
	if !forceloop {
		// Try to use file-backed mount feature if available (Linux 6.12+) first
		err = doMount(m, mp)
	}
	if errors.Is(err, unix.ENOTBLK) {
		var loops []*os.File

		// Never try to mount with raw files anymore if tried
		forceloop = true
		params := mount.LoopParams{
			Readonly:  true,
			Autoclear: true,
		}
		// set up all loop devices
		loop, err := mount.SetupLoop(m.Source, params)
		if err != nil {
			if dmverityDevice != "" {
				dmverity.Close(dmverityDevice)
			}
			return mount.ActiveMount{}, err
		}
		m.Source = loop.Name()
		loops = append(loops, loop)
		defer func() {
			for _, loop := range loops {
				loop.Close()
			}
		}()

		for i, v := range m.Options {
			// Convert raw files in `device=` into loop devices too
			if strings.HasPrefix(v, "device=") {
				loop, err := mount.SetupLoop(strings.TrimPrefix(v, "device="), params)
				if err != nil {
					if dmverityDevice != "" {
						dmverity.Close(dmverityDevice)
					}
					return mount.ActiveMount{}, err
				}
				m.Options[i] = "device=" + loop.Name()
				loops = append(loops, loop)
			}
		}
		err = doMount(m, mp)
		if err != nil {
			if dmverityDevice != "" {
				dmverity.Close(dmverityDevice)
			}
			return mount.ActiveMount{}, err
		}
	} else if err != nil {
		if dmverityDevice != "" {
			dmverity.Close(dmverityDevice)
		}
		return mount.ActiveMount{}, err
	}

	t := time.Now()
	return mount.ActiveMount{
		Mount:      m,
		MountedAt:  &t,
		MountPoint: mp,
	}, nil
}

func doMount(m mount.Mount, target string) error {
	if err := fsmount.Fsmount(m, target); err != nil {
		// Fall back to traditional mount() if fsmount syscall not available (Linux < 5.2)
		if errors.Is(err, unix.ENOSYS) {
			log.L.WithError(err).Debug("fsmount not available, falling back to traditional mount")
			return m.Mount(target)
		}
		return err
	}
	return nil
}

func (h *erofsMountHandler) Unmount(ctx context.Context, path string) error {
	// Check what's currently mounted to determine if dm-verity device cleanup is needed
	var deviceName string
	mountInfo, err := mount.Lookup(path)
	if err == nil {
		source := mountInfo.Source
		if strings.HasPrefix(source, "/dev/mapper/containerd-erofs-") {
			deviceName = strings.TrimPrefix(source, "/dev/mapper/")
		}
	}

	err = mount.Unmount(path, 0)

	if deviceName != "" {
		log.G(ctx).WithFields(log.Fields{
			"mount-point": path,
			"device":      deviceName,
		}).Debug("attempting to close dm-verity device")

		if closeErr := dmverity.Close(deviceName); closeErr != nil {
			log.G(ctx).WithError(closeErr).WithField("device", deviceName).Debug("unable to close dm-verity device")
		} else {
			log.G(ctx).WithField("device", deviceName).Debug("dm-verity device closed successfully")
		}
	}

	return err
}

type Config struct{}

func init() {
	registry.Register(&plugin.Registration{
		Type:   plugins.MountHandlerPlugin,
		ID:     "erofs",
		Config: &Config{},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			p := platforms.DefaultSpec()
			p.OS = runtime.GOOS
			ic.Meta.Platforms = append(ic.Meta.Platforms, p)

			return NewErofsMountHandler(), nil
		},
	})
}
