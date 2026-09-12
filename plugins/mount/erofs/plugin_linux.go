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
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
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

func (h *erofsMountHandler) Mount(ctx context.Context, m mount.Mount, mp string, _ []mount.ActiveMount) (_ mount.ActiveMount, retErr error) {
	if m.Type != "erofs" {
		return mount.ActiveMount{}, errdefs.ErrNotImplemented
	}

	var dmverityDevice string
	defer func() {
		if retErr == nil || dmverityDevice == "" {
			return
		}
		dmverity.Close(dmverityDevice)
	}()

	// Parse dm-verity metadata path from mount options
	metadataPath := ""
	for _, opt := range m.Options {
		if path, ok := strings.CutPrefix(opt, "X-containerd.dmverity="); ok {
			metadataPath = path
			break
		}
	}

	// Set up dm-verity device if metadata path is provided
	if metadataPath != "" {
		log.G(ctx).WithFields(log.Fields{
			"source":        m.Source,
			"metadata-path": metadataPath,
		}).Debug("setting up dm-verity device from mount option")

		metadata, err := dmverity.ReadMetadata(metadataPath)
		if err != nil {
			return mount.ActiveMount{}, fmt.Errorf("failed to read dm-verity metadata from %s: %w", metadataPath, err)
		}

		devicePath, cleanupName, err := setupDmVerityDevice(ctx, m.Source, mp, metadata)
		dmverityDevice = cleanupName
		if err != nil {
			return mount.ActiveMount{}, err
		}
		m.Source = devicePath
	}

	filteredOptions := make([]string, 0, len(m.Options))
	for _, v := range m.Options {
		// Skip loop option (handled by loop device setup) and dmverity options (already processed)
		if v == "loop" || strings.HasPrefix(v, "X-containerd.dmverity=") {
			continue
		}
		filteredOptions = append(filteredOptions, v)
	}
	m.Options = filteredOptions

	if err := os.MkdirAll(mp, 0700); err != nil {
		return mount.ActiveMount{}, err
	}

	err := error(unix.ENOTBLK)
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
			if after, ok := strings.CutPrefix(v, "device="); ok {
				loop, err := mount.SetupLoop(after, params)
				if err != nil {
					return mount.ActiveMount{}, err
				}
				m.Options[i] = "device=" + loop.Name()
				loops = append(loops, loop)
			}
		}
		err = doMount(m, mp)
		if err != nil {
			return mount.ActiveMount{}, err
		}
	} else if err != nil {
		return mount.ActiveMount{}, err
	}

	t := time.Now()
	return mount.ActiveMount{
		Mount:      m,
		MountedAt:  &t,
		MountPoint: mp,
	}, nil
}

// dmverityDeviceName names the dm-verity device for a layer mounted from source
// at mountpoint.
//
// The name has to be unique to this mount. Mount closes the device it creates
// when the mount fails, so a name two mounts share would let one of them close
// a device the other is using. Neither half is unique alone: a layer content
// cache serves one blob to every snapshot of that layer, and a mount point is
// reused once the mount before it is gone. Together they identify the mount,
// and a device left behind by a crash is reused only for the same blob in the
// same place, where its root hash still matches.
func dmverityDeviceName(source, mountpoint string) string {
	sum := sha256.Sum256([]byte(source + "\x00" + mountpoint))
	return fmt.Sprintf("containerd-erofs-%x", sum[:16])
}

// setupDmVerityDevice creates or reuses a dm-verity device for the given EROFS source.
// It returns the device path to mount from, and a cleanup name (non-empty only when
// a new device was created, so the caller can close it on error).
func setupDmVerityDevice(ctx context.Context, source, mountpoint string, metadata *dmverity.DmverityMetadata) (devicePath string, cleanupName string, err error) {
	supported, err := dmverity.IsSupported()
	if err != nil || !supported {
		return "", "", fmt.Errorf("layer requires dm-verity but system doesn't support it (dm_verity module not loaded): %w", err)
	}

	deviceName := dmverityDeviceName(source, mountpoint)
	devicePath = dmverity.DevicePath(deviceName)

	log.G(ctx).WithFields(log.Fields{
		"source":      source,
		"device-name": deviceName,
		"hash-offset": metadata.HashOffset,
	}).Debug("opening dm-verity device")

	// Try to create dm-verity device first (avoids TOCTOU race)
	_, err = dmverity.Open(source, deviceName, source, metadata.RootHash, metadata.HashOffset, nil)
	if err == nil {
		// New device created — wait for it to appear and return cleanupName
		// so the caller can close it on error.
		if waitErr := waitForDevice(devicePath); waitErr != nil {
			return "", deviceName, waitErr
		}
		log.G(ctx).WithField("device", devicePath).Debug("dm-verity device created successfully")
		return devicePath, deviceName, nil
	}

	// Open failed — check if the device already exists and can be reused.
	if _, statErr := os.Stat(devicePath); statErr != nil {
		return "", "", fmt.Errorf("failed to open dm-verity device: %w", err)
	}

	if verifyErr := dmverity.VerifyDevice(deviceName, metadata.RootHash); verifyErr != nil {
		return "", "", fmt.Errorf("existing dm-verity device %q verification failed: %w", deviceName, verifyErr)
	}

	log.G(ctx).WithField("device", devicePath).Debug("dm-verity device already exists and verified, reusing")
	return devicePath, "", nil
}

// waitForDevice polls for the device node to appear, returning an error if it
// does not appear within a reasonable timeout.
func waitForDevice(devicePath string) error {
	for range 100 {
		if _, err := os.Stat(devicePath); err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(devicePath); err != nil {
		return fmt.Errorf("dm-verity device %q not found after creation: %w", devicePath, err)
	}
	return nil
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
		InitFn: func(ic *plugin.InitContext) (any, error) {
			p := platforms.DefaultSpec()
			p.OS = runtime.GOOS
			ic.Meta.Platforms = append(ic.Meta.Platforms, p)

			return NewErofsMountHandler(), nil
		},
	})
}
