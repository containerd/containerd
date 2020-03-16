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

	poolDevice := &PoolDevice{
		poolName: config.PoolName,
		metadata: poolMetaStore,
	}

	if err := poolDevice.ensureDeviceStates(ctx); err != nil {
		return nil, errors.Wrap(err, "failed to check devices state")
	}

	return poolDevice, nil
}

// ensureDeviceStates updates devices to their real state:
//   - marks devices with incomplete states (after crash) as 'Faulty'
//   - activates devices if they are marked as 'Activated' but the dm
//     device is not active, which can happen to a stopped container
//     after a reboot
func (p *PoolDevice) ensureDeviceStates(ctx context.Context) error {
	var faultyDevices []*DeviceInfo
	var activatedDevices []*DeviceInfo

	if err := p.metadata.WalkDevices(ctx, func(info *DeviceInfo) error {
		switch info.State {
		case Suspended, Resumed, Deactivated, Removed, Faulty:
		case Activated:
			activatedDevices = append(activatedDevices, info)
		default:
			faultyDevices = append(faultyDevices, info)
		}
		return nil
	}); err != nil {
		return errors.Wrap(err, "failed to query devices from metastore")
	}

	var result *multierror.Error
	for _, dev := range activatedDevices {
		if p.IsActivated(dev.Name) {
			continue
		}

		log.G(ctx).Warnf("devmapper device %q marked as %q but not active, activating it", dev.Name, dev.State)
		if err := p.activateDevice(ctx, dev); err != nil {
			result = multierror.Append(result, err)
		}
	}

	for _, dev := range faultyDevices {
		log.G(ctx).
			WithField("dev_id", dev.DeviceID).
			WithField("parent", dev.ParentName).
			WithField("error", dev.Error).
			Warnf("devmapper device %q has invalid state %q, marking as faulty", dev.Name, dev.State)

		if err := p.metadata.MarkFaulty(ctx, dev.Name); err != nil {
			result = multierror.Append(result, err)
		}
	}

	return multierror.Prefix(result.ErrorOrNil(), "devmapper:")
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

	var (
		metaErr   error
		devErr    error
		activeErr error
	)

	defer func() {
		// We've created a devmapper device, but failed to activate it, try rollback everything
		if activeErr != nil {
			// Delete the device first.
			delErr := p.deleteDevice(ctx, info)
			if delErr != nil {
				// Failed to rollback, mark the device as faulty and keep metadata in order to
				// preserve the faulty device ID
				retErr = multierror.Append(retErr, delErr, p.metadata.MarkFaulty(ctx, info.Name))
				return
			}

			// The devmapper device has been successfully deleted, deallocate device ID
			if err := p.RemoveDevice(ctx, info.Name); err != nil {
				retErr = multierror.Append(retErr, err)
				return
			}

			return
		}

		// We're unable to create the devmapper device, most likely something wrong with the deviceID
		if devErr != nil {
			retErr = multierror.Append(retErr, p.metadata.MarkFaulty(ctx, info.Name))
			return
		}
	}()

	// Save initial device metadata and allocate new device ID from store
	metaErr = p.metadata.AddDevice(ctx, info)
	if metaErr != nil {
		return metaErr
	}

	// Create thin device
	devErr = p.createDevice(ctx, info)
	if devErr != nil {
		return devErr
	}

	// Activate thin device
	activeErr = p.activateDevice(ctx, info)
	if activeErr != nil {
		return activeErr
	}

	return nil
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

	snapInfo := &DeviceInfo{
		Name:       snapshotName,
		Size:       virtualSizeBytes,
		ParentName: deviceName,
		State:      Unknown,
	}

	var (
		metaErr   error
		devErr    error
		activeErr error
	)

	defer func() {
		// We've created a devmapper device, but failed to activate it, try rollback everything
		if activeErr != nil {
			// Delete the device first.
			delErr := p.deleteDevice(ctx, snapInfo)
			if delErr != nil {
				// Failed to rollback, mark the device as faulty and keep metadata in order to
				// preserve the faulty device ID
				retErr = multierror.Append(retErr, delErr, p.metadata.MarkFaulty(ctx, snapInfo.Name))
				return
			}

			// The devmapper device has been successfully deleted, deallocate device ID
			if err := p.RemoveDevice(ctx, snapInfo.Name); err != nil {
				retErr = multierror.Append(retErr, err)
				return
			}

			return
		}

		// We're unable to create the devmapper device, most likely something wrong with the deviceID
		if devErr != nil {
			retErr = multierror.Append(retErr, p.metadata.MarkFaulty(ctx, snapInfo.Name))
			return
		}
	}()

	// Save snapshot metadata and allocate new device ID
	metaErr = p.metadata.AddDevice(ctx, snapInfo)
	if metaErr != nil {
		return metaErr
	}

	// Create thin device snapshot
	devErr = p.createSnapshot(ctx, baseInfo, snapInfo)
	if devErr != nil {
		return devErr
	}

	// Activate the snapshot device
	activeErr = p.activateDevice(ctx, snapInfo)
	if activeErr != nil {
		return activeErr
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

// SuspendDevice flushes the outstanding IO and blocks the further IO
func (p *PoolDevice) SuspendDevice(ctx context.Context, deviceName string) error {
	if err := p.transition(ctx, deviceName, Suspending, Suspended, func() error {
		return dmsetup.SuspendDevice(deviceName)
	}); err != nil {
		return errors.Wrapf(err, "failed to suspend device %q", deviceName)
	}

	return nil
}

// DeactivateDevice deactivates thin device
func (p *PoolDevice) DeactivateDevice(ctx context.Context, deviceName string, deferred, withForce bool) error {
	if !p.IsLoaded(deviceName) {
		return nil
	}

	opts := []dmsetup.RemoveDeviceOpt{dmsetup.RemoveWithRetries}
	if deferred {
		opts = append(opts, dmsetup.RemoveDeferred)
	}
	if withForce {
		opts = append(opts, dmsetup.RemoveWithForce)
	}

	if err := p.transition(ctx, deviceName, Deactivating, Deactivated, func() error {
		return dmsetup.RemoveDevice(deviceName, opts...)
	}); err != nil {
		return errors.Wrapf(err, "failed to deactivate device %q", deviceName)
	}

	return nil
}

// IsActivated returns true if thin-device is activated
func (p *PoolDevice) IsActivated(deviceName string) bool {
	infos, err := dmsetup.Info(deviceName)
	if err != nil || len(infos) != 1 {
		// Couldn't query device info, device not active
		return false
	}

	if devInfo := infos[0]; devInfo.TableLive {
		return true
	}

	return false
}

// IsLoaded returns true if thin-device is visible for dmsetup
func (p *PoolDevice) IsLoaded(deviceName string) bool {
	_, err := dmsetup.Info(deviceName)
	return err == nil
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

	if err := p.DeactivateDevice(ctx, deviceName, false, true); err != nil {
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
		if err := p.DeactivateDevice(ctx, name, true, true); err != nil {
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
