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
	"path/filepath"
	"strconv"

	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots/devmapper/dmsetup"
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

	// Make sure pool exists and available
	poolPath := dmsetup.GetFullDevicePath(config.PoolName)
	if _, err := dmsetup.Info(poolPath); err != nil {
		return nil, errors.Wrapf(err, "failed to query pool %q", poolPath)
	}

	return &PoolDevice{
		poolName: config.PoolName,
		metadata: poolMetaStore,
	}, nil
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
func (p *PoolDevice) CreateThinDevice(ctx context.Context, deviceName string, virtualSizeBytes uint64) (retErr error) {
	info := &DeviceInfo{
		Name:  deviceName,
		Size:  virtualSizeBytes,
		State: Unknown,
	}

	// Save initial device metadata and allocate new device ID from store
	if err := p.metadata.AddDevice(ctx, info); err != nil {
		return errors.Wrapf(err, "failed to save initial metadata for new thin device %q", deviceName)
	}

	defer func() {
		if retErr == nil {
			return
		}

		// Rollback metadata
		retErr = multierror.Append(retErr, p.metadata.RemoveDevice(ctx, info.Name))
	}()

	// Create thin device
	if err := p.createDevice(ctx, info); err != nil {
		return err
	}

	defer func() {
		if retErr == nil {
			return
		}

		// Rollback creation
		retErr = multierror.Append(retErr, p.deleteDevice(ctx, info))
	}()

	return p.activateDevice(ctx, info)
}

// createDevice creates thin device
func (p *PoolDevice) createDevice(ctx context.Context, info *DeviceInfo) error {
	if err := p.transition(ctx, info.Name, Creating, Created, func() error {
		return dmsetup.CreateDevice(p.poolName, info.DeviceID)
	}); err != nil {
		return errors.Wrapf(err, "failed to create new thin device %q (dev: %d)", info.Name, info.DeviceID)
	}

	return nil
}

// activateDevice activates thin device
func (p *PoolDevice) activateDevice(ctx context.Context, info *DeviceInfo) error {
	if err := p.transition(ctx, info.Name, Activating, Activated, func() error {
		return dmsetup.ActivateDevice(p.poolName, info.Name, info.DeviceID, info.Size, "")
	}); err != nil {
		return errors.Wrapf(err, "failed to activate new thin device %q (dev: %d)", info.Name, info.DeviceID)
	}

	return nil
}

// CreateSnapshotDevice creates and activates new thin-device from parent thin-device (makes snapshot)
func (p *PoolDevice) CreateSnapshotDevice(ctx context.Context, deviceName string, snapshotName string, virtualSizeBytes uint64) (retErr error) {
	baseInfo, err := p.metadata.GetDevice(ctx, deviceName)
	if err != nil {
		return errors.Wrapf(err, "failed to query device metadata for %q", deviceName)
	}

	// Suspend thin device if it was activated previously to avoid corruptions
	isActivated := p.IsActivated(baseInfo.Name)
	if isActivated {
		if err := p.suspendDevice(ctx, baseInfo); err != nil {
			return err
		}

		// Resume back base thin device on exit
		defer func() {
			retErr = multierror.Append(retErr, p.resumeDevice(ctx, baseInfo)).ErrorOrNil()
		}()
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

	defer func() {
		if retErr == nil {
			return
		}

		// Rollback metadata
		retErr = multierror.Append(retErr, p.metadata.RemoveDevice(ctx, snapInfo.Name))
	}()

	// Create thin device snapshot
	if err := p.createSnapshot(ctx, baseInfo, snapInfo); err != nil {
		return err
	}

	defer func() {
		if retErr == nil {
			return
		}

		// Rollback snapshot creation
		retErr = multierror.Append(retErr, p.deleteDevice(ctx, snapInfo))
	}()

	// Activate snapshot device
	return p.activateDevice(ctx, snapInfo)
}

func (p *PoolDevice) suspendDevice(ctx context.Context, info *DeviceInfo) error {
	if err := p.transition(ctx, info.Name, Suspending, Suspended, func() error {
		return dmsetup.SuspendDevice(info.Name)
	}); err != nil {
		return errors.Wrapf(err, "failed to suspend device %q", info.Name)
	}

	return nil
}

func (p *PoolDevice) resumeDevice(ctx context.Context, info *DeviceInfo) error {
	if err := p.transition(ctx, info.Name, Resuming, Resumed, func() error {
		return dmsetup.ResumeDevice(info.Name)
	}); err != nil {
		return errors.Wrapf(err, "failed to resume device %q", info.Name)
	}

	return nil
}

func (p *PoolDevice) createSnapshot(ctx context.Context, baseInfo, snapInfo *DeviceInfo) error {
	if err := p.transition(ctx, snapInfo.Name, Creating, Created, func() error {
		return dmsetup.CreateSnapshot(p.poolName, snapInfo.DeviceID, baseInfo.DeviceID)
	}); err != nil {
		return errors.Wrapf(err,
			"failed to create snapshot %q (dev: %d) from %q (dev: %d)",
			snapInfo.Name,
			snapInfo.DeviceID,
			baseInfo.Name,
			baseInfo.DeviceID)
	}

	return nil
}

// DeactivateDevice deactivates thin device
func (p *PoolDevice) DeactivateDevice(ctx context.Context, deviceName string, deferred bool) error {
	if !p.IsActivated(deviceName) {
		return nil
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

// IsActivated returns true if thin-device is activated and not suspended
func (p *PoolDevice) IsActivated(deviceName string) bool {
	infos, err := dmsetup.Info(deviceName)
	if err != nil || len(infos) != 1 {
		// Couldn't query device info, device not active
		return false
	}

	if devInfo := infos[0]; devInfo.Suspended {
		return false
	}

	return true
}

// GetUsage reports total size in bytes consumed by a thin-device.
// It relies on the number of used blocks reported by 'dmsetup status'.
// The output looks like:
//  device2: 0 204800 thin 17280 204799
// Where 17280 is the number of used sectors
func (p *PoolDevice) GetUsage(deviceName string) (int64, error) {
	status, err := dmsetup.Status(deviceName)
	if err != nil {
		return 0, errors.Wrapf(err, "can't get status for device %q", deviceName)
	}

	if len(status.Params) == 0 {
		return 0, errors.Errorf("failed to get the number of used blocks, unexpected output from dmsetup status")
	}

	count, err := strconv.ParseInt(status.Params[0], 10, 64)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to parse status params: %q", status.Params[0])
	}

	return count * dmsetup.SectorSize, nil
}

// RemoveDevice completely wipes out thin device from thin-pool and frees it's device ID
func (p *PoolDevice) RemoveDevice(ctx context.Context, deviceName string) error {
	info, err := p.metadata.GetDevice(ctx, deviceName)
	if err != nil {
		return errors.Wrapf(err, "can't query metadata for device %q", deviceName)
	}

	if err := p.DeactivateDevice(ctx, deviceName, true); err != nil {
		return err
	}

	if err := p.deleteDevice(ctx, info); err != nil {
		return err
	}

	// Remove record from meta store and free device ID
	if err := p.metadata.RemoveDevice(ctx, deviceName); err != nil {
		return errors.Wrapf(err, "can't remove device %q metadata from store after removal", deviceName)
	}

	return nil
}

func (p *PoolDevice) deleteDevice(ctx context.Context, info *DeviceInfo) error {
	if err := p.transition(ctx, info.Name, Removing, Removed, func() error {
		// Send 'delete' message to thin-pool
		return dmsetup.DeleteDevice(p.poolName, info.DeviceID)
	}); err != nil {
		return errors.Wrapf(err, "failed to delete device %q (dev id: %d)", info.Name, info.DeviceID)
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
		if err := p.DeactivateDevice(ctx, name, true); err != nil {
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
