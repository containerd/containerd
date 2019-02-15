// +build linux

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

package devmapper

import (
	"context"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots/devmapper/dmsetup"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
)

// PoolDevice ties together data and metadata volumes, represents thin-pool and manages volumes, snapshots and device ids.
type PoolDevice struct {
	poolName string
	metadata *PoolMetadata
}

// NewPoolDevice creates new thin-pool from existing data and metadata volumes.
// If pool 'poolName' already exists, it'll be reloaded with new parameters.
func NewPoolDevice(ctx context.Context, config *Config) (*PoolDevice, error) {
	log.G(ctx).Infof("initializing pool device %q", config.PoolName)

	version, err := dmsetup.Version()
	if err != nil {
		log.G(ctx).Errorf("dmsetup not available")
		return nil, err
	}

	log.G(ctx).Infof("using dmsetup:\n%s", version)

	dbpath := filepath.Join(config.RootPath, config.PoolName+".db")
	poolMetaStore, err := NewPoolMetadata(dbpath)
	if err != nil {
		return nil, err
	}

	if err := openPool(ctx, config); err != nil {
		return nil, err
	}

	return &PoolDevice{
		poolName: config.PoolName,
		metadata: poolMetaStore,
	}, nil
}

func openPool(ctx context.Context, config *Config) error {
	if err := config.Validate(); err != nil {
		return err
	}

	var (
		poolPath   = dmsetup.GetFullDevicePath(config.PoolName)
		poolExists = false
	)

	if _, err := os.Stat(poolPath); err != nil && !os.IsNotExist(err) {
		return errors.Wrapf(err, "failed to stat for %q", poolPath)
	} else if err == nil {
		poolExists = true
	}

	// Create new pool if not exists
	if !poolExists {
		log.G(ctx).Debug("creating new pool device")
		if err := dmsetup.CreatePool(config.PoolName, config.DataDevice, config.MetadataDevice, config.DataBlockSizeSectors); err != nil {
			return errors.Wrapf(err, "failed to create thin-pool with name %q", config.PoolName)
		}

		return nil
	}

	// Pool exists, check if it needs to be reloaded
	if config.DataDevice != "" && config.MetadataDevice != "" {
		log.G(ctx).Debugf("reloading existing pool %q", poolPath)
		if err := dmsetup.ReloadPool(config.PoolName, config.DataDevice, config.MetadataDevice, config.DataBlockSizeSectors); err != nil {
			return errors.Wrapf(err, "failed to reload pool %q", config.PoolName)
		}

		return nil
	}

	// If data and meta devices are not provided, use existing pool. Query info to make sure it's OK.
	if _, err := dmsetup.Info(poolPath); err != nil {
		return errors.Wrapf(err, "failed to query info for existing pool %q", poolPath)
	}

	return nil
}

// transition invokes 'updateStateFn' callback to perform devmapper operation and reflects device state changes/errors in meta store.
// 'tryingState' will be set before invoking callback. If callback succeeded 'successState' will be set, otherwise
// error details will be recorded in meta store.
func (p *PoolDevice) transition(ctx context.Context, deviceName string, tryingState DeviceState, successState DeviceState, updateStateFn func() error) error {
	// Set device to trying state
	uerr := p.metadata.UpdateDevice(ctx, deviceName, func(deviceInfo *DeviceInfo) error {
		deviceInfo.State = tryingState
		return nil
	})

	if uerr != nil {
		return errors.Wrapf(uerr, "failed to set device %q state to %q", deviceName, tryingState)
	}

	var result *multierror.Error

	// Invoke devmapper operation
	err := updateStateFn()

	if err != nil {
		result = multierror.Append(result, err)
	}

	// If operation succeeded transition to success state, otherwise save error details
	uerr = p.metadata.UpdateDevice(ctx, deviceName, func(deviceInfo *DeviceInfo) error {
		if err == nil {
			deviceInfo.State = successState
			deviceInfo.Error = ""
		} else {
			deviceInfo.Error = err.Error()
		}
		return nil
	})

	if uerr != nil {
		result = multierror.Append(result, uerr)
	}

	return result.ErrorOrNil()
}

// CreateThinDevice creates new devmapper thin-device with given name and size.
// Device ID for thin-device will be allocated from metadata store.
// If allocation successful, device will be activated with /dev/mapper/<deviceName>
func (p *PoolDevice) CreateThinDevice(ctx context.Context, deviceName string, virtualSizeBytes uint64) error {
	info := &DeviceInfo{
		Name:  deviceName,
		Size:  virtualSizeBytes,
		State: Unknown,
	}

	// Save initial device metadata and allocate new device ID from store
	if err := p.metadata.AddDevice(ctx, info); err != nil {
		return errors.Wrapf(err, "failed to save initial metadata for new thin device %q", deviceName)
	}

	// Create thin device
	if err := p.transition(ctx, deviceName, Creating, Created, func() error {
		return dmsetup.CreateDevice(p.poolName, info.DeviceID)
	}); err != nil {
		return errors.Wrapf(err, "failed to create new thin device %q (dev: %d)", info.Name, info.DeviceID)
	}

	// Activate thin device
	if err := p.transition(ctx, deviceName, Activating, Activated, func() error {
		return dmsetup.ActivateDevice(p.poolName, info.Name, info.DeviceID, info.Size, "")
	}); err != nil {
		return errors.Wrapf(err, "failed to activate new thin device %q (dev: %d)", info.Name, info.DeviceID)
	}

	return nil
}

// CreateSnapshotDevice creates and activates new thin-device from parent thin-device (makes snapshot)
func (p *PoolDevice) CreateSnapshotDevice(ctx context.Context, deviceName string, snapshotName string, virtualSizeBytes uint64) error {
	baseInfo, err := p.metadata.GetDevice(ctx, deviceName)
	if err != nil {
		return errors.Wrapf(err, "failed to query device metadata for %q", deviceName)
	}

	isActivated := baseInfo.State == Activated

	// Suspend thin device if it was activated previously
	if isActivated {
		if err := p.transition(ctx, baseInfo.Name, Suspending, Suspended, func() error {
			return dmsetup.SuspendDevice(baseInfo.Name)
		}); err != nil {
			return errors.Wrapf(err, "failed to suspend device %q", baseInfo.Name)
		}
	}

	snapInfo := &DeviceInfo{
		Name:       snapshotName,
		Size:       virtualSizeBytes,
		ParentName: deviceName,
		State:      Unknown,
	}

	// Save snapshot metadata and allocate new device ID
	if err := p.metadata.AddDevice(ctx, snapInfo); err != nil {
		return errors.Wrapf(err, "failed to save initial metadata for snapshot %q", snapshotName)
	}

	// Create thin device snapshot
	if err := p.transition(ctx, snapInfo.Name, Creating, Created, func() error {
		return dmsetup.CreateSnapshot(p.poolName, snapInfo.DeviceID, baseInfo.DeviceID)
	}); err != nil {
		return errors.Wrapf(err,
			"failed to create snapshot %q (dev: %d) from %q (dev: %d, activated: %t)",
			snapInfo.Name,
			snapInfo.DeviceID,
			baseInfo.Name,
			baseInfo.DeviceID,
			isActivated)
	}

	if isActivated {
		// Resume base thin-device
		if err := p.transition(ctx, baseInfo.Name, Resuming, Resumed, func() error {
			return dmsetup.ResumeDevice(baseInfo.Name)
		}); err != nil {
			return errors.Wrapf(err, "failed to resume device %q", deviceName)
		}
	}

	// Activate snapshot
	if err := p.transition(ctx, snapInfo.Name, Activating, Activated, func() error {
		return dmsetup.ActivateDevice(p.poolName, snapInfo.Name, snapInfo.DeviceID, snapInfo.Size, "")
	}); err != nil {
		return errors.Wrapf(err, "failed to activate snapshot device %q (dev: %d)", snapInfo.Name, snapInfo.DeviceID)
	}

	return nil
}

// DeactivateDevice deactivates thin device
func (p *PoolDevice) DeactivateDevice(ctx context.Context, deviceName string, deferred bool) error {
	devicePath := dmsetup.GetFullDevicePath(deviceName)
	if _, err := os.Stat(devicePath); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}

		return err
	}

	opts := []dmsetup.RemoveDeviceOpt{dmsetup.RemoveWithForce, dmsetup.RemoveWithRetries}
	if deferred {
		opts = append(opts, dmsetup.RemoveDeferred)
	}

	if err := p.transition(ctx, deviceName, Deactivating, Deactivated, func() error {
		return dmsetup.RemoveDevice(deviceName, opts...)
	}); err != nil {
		return errors.Wrapf(err, "failed to deactivate device %q", deviceName)
	}

	return nil
}

// RemoveDevice completely wipes out thin device from thin-pool and frees it's device ID
func (p *PoolDevice) RemoveDevice(ctx context.Context, deviceName string) error {
	info, err := p.metadata.GetDevice(ctx, deviceName)
	if err != nil {
		return errors.Wrapf(err, "can't query metadata for device %q", deviceName)
	}

	if err := p.DeactivateDevice(ctx, deviceName, true); err != nil && err != ErrNotFound {
		return err
	}

	if err := p.transition(ctx, deviceName, Removing, Removed, func() error {
		// Send 'delete' message to thin-pool
		return dmsetup.DeleteDevice(p.poolName, info.DeviceID)
	}); err != nil {
		return errors.Wrapf(err, "failed to delete device %q (dev id: %d)", info.Name, info.DeviceID)
	}

	// Remove record from meta store and free device ID
	if err := p.metadata.RemoveDevice(ctx, deviceName); err != nil {
		return errors.Wrapf(err, "can't remove device %q metadata from store after removal", deviceName)
	}

	return nil
}

// RemovePool deactivates all child thin-devices and removes thin-pool device
func (p *PoolDevice) RemovePool(ctx context.Context) error {
	deviceNames, err := p.metadata.GetDeviceNames(ctx)
	if err != nil {
		return errors.Wrap(err, "can't query device names")
	}

	var result *multierror.Error

	// Deactivate devices if any
	for _, name := range deviceNames {
		if err := p.DeactivateDevice(ctx, name, true); err != nil && err != ErrNotFound {
			result = multierror.Append(result, errors.Wrapf(err, "failed to remove %q", name))
		}
	}

	if err := dmsetup.RemoveDevice(p.poolName, dmsetup.RemoveWithForce, dmsetup.RemoveWithRetries, dmsetup.RemoveDeferred); err != nil {
		result = multierror.Append(result, errors.Wrapf(err, "failed to remove pool %q", p.poolName))
	}

	return result.ErrorOrNil()
}

// Close closes pool device (thin-pool will not be removed)
func (p *PoolDevice) Close() error {
	return p.metadata.Close()
}
