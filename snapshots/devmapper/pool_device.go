//go:build linux

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
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/hashicorp/go-multierror"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/log"
	blkdiscard "github.com/containerd/containerd/snapshots/devmapper/blkdiscard"
	"github.com/containerd/containerd/snapshots/devmapper/dmsetup"
)

// PoolDevice ties together data and metadata volumes, represents thin-pool and manages volumes, snapshots and device ids.
type PoolDevice struct {
	poolName      string
	metadata      *PoolMetadata
	discardBlocks bool
}

// NewPoolDevice creates new thin-pool from existing data and metadata volumes.
// If pool 'poolName' already exists, it'll be reloaded with new parameters.
func NewPoolDevice(ctx context.Context, config *Config) (*PoolDevice, error) {
	log.G(ctx).Infof("initializing pool device %q", config.PoolName)

	version, err := dmsetup.Version()
	if err != nil {
		log.G(ctx).Error("dmsetup not available")
		return nil, err
	}

	log.G(ctx).Infof("using dmsetup:\n%s", version)

	if config.DiscardBlocks {
		blkdiscardVersion, err := blkdiscard.Version()
		if err != nil {
			log.G(ctx).Error("blkdiscard is not available")
			return nil, err
		}
		log.G(ctx).Infof("using blkdiscard:\n%s", blkdiscardVersion)
	}

	dbpath := filepath.Join(config.RootPath, config.PoolName+".db")
	poolMetaStore, err := NewPoolMetadata(dbpath)
	if err != nil {
		return nil, err
	}

	// Make sure pool exists and available
	poolPath := dmsetup.GetFullDevicePath(config.PoolName)
	if _, err := dmsetup.Info(poolPath); err != nil {
		return nil, fmt.Errorf("failed to query pool %q: %w", poolPath, err)
	}

	poolDevice := &PoolDevice{
		poolName:      config.PoolName,
		metadata:      poolMetaStore,
		discardBlocks: config.DiscardBlocks,
	}

	if err := poolDevice.ensureDeviceStates(ctx); err != nil {
		return nil, fmt.Errorf("failed to check devices state: %w", err)
	}

	return poolDevice, nil
}

func skipRetry(err error) bool {
	if err == nil {
		return true // skip retry if no error
	} else if !errors.Is(err, unix.EBUSY) {
		return true // skip retry if error is not due to device or resource busy
	}
	return false
}

func retry(ctx context.Context, f func() error) error {
	var (
		maxRetries = 100
		retryDelay = 100 * time.Millisecond
		retryErr   error
	)

	for attempt := 1; attempt <= maxRetries; attempt++ {
		retryErr = f()

		if skipRetry(retryErr) {
			return retryErr
		}

		// Don't spam logs
		if attempt%10 == 0 {
			log.G(ctx).WithError(retryErr).Warnf("retrying... (%d of %d)", attempt, maxRetries)
		}

		// Devmapper device is busy, give it a bit of time and retry removal
		time.Sleep(retryDelay)
	}

	return retryErr
}

// ensureDeviceStates updates devices to their real state:
//   - marks devices with incomplete states (after crash) as 'Faulty'
//   - activates devices if they are marked as 'Activated' but the dm
//     device is not active, which can happen to a stopped container
//     after a reboot
func (p *PoolDevice) ensureDeviceStates(ctx context.Context) error {
	var faultyDevices []*DeviceInfo
	var activatedDevices []*DeviceInfo

	if err := p.WalkDevices(ctx, func(info *DeviceInfo) error {
		switch info.State {
		case Suspended, Resumed, Deactivated, Removed, Faulty:
		case Activated:
			activatedDevices = append(activatedDevices, info)
		default:
			faultyDevices = append(faultyDevices, info)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to query devices from metastore: %w", err)
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
		return fmt.Errorf("failed to set device %q state to %q: %w", deviceName, tryingState, uerr)
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

	return unwrapError(result)
}

// unwrapError converts multierror.Error to the original error when it is possible.
// multierror 1.1.0 has the similar function named Unwrap, but it requires Go 1.14.
func unwrapError(e *multierror.Error) error {
	if e == nil {
		return nil
	}

	// If the error can be expressed without multierror, return the original error.
	if len(e.Errors) == 1 {
		return e.Errors[0]
	}

	return e.ErrorOrNil()
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
			retErr = p.rollbackActivate(ctx, info, activeErr)
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

func (p *PoolDevice) rollbackActivate(ctx context.Context, info *DeviceInfo, activateErr error) error {
	// Delete the device first.
	delErr := p.deleteDevice(ctx, info)
	if delErr != nil {
		// Failed to rollback, mark the device as faulty and keep metadata in order to
		// preserve the faulty device ID
		return multierror.Append(activateErr, delErr, p.metadata.MarkFaulty(ctx, info.Name))
	}

	// The devmapper device has been successfully deleted, deallocate device ID
	if err := p.RemoveDevice(ctx, info.Name); err != nil {
		return multierror.Append(activateErr, err)
	}

	return activateErr
}

// createDevice creates thin device
func (p *PoolDevice) createDevice(ctx context.Context, info *DeviceInfo) error {
	if err := p.transition(ctx, info.Name, Creating, Created, func() error {
		return dmsetup.CreateDevice(p.poolName, info.DeviceID)
	}); err != nil {
		return fmt.Errorf("failed to create new thin device %q (dev: %d): %w", info.Name, info.DeviceID, err)
	}

	return nil
}

// activateDevice activates thin device
func (p *PoolDevice) activateDevice(ctx context.Context, info *DeviceInfo) error {
	if err := p.transition(ctx, info.Name, Activating, Activated, func() error {
		return dmsetup.ActivateDevice(p.poolName, info.Name, info.DeviceID, info.Size, "")
	}); err != nil {
		return fmt.Errorf("failed to activate new thin device %q (dev: %d): %w", info.Name, info.DeviceID, err)
	}

	return nil
}

// CreateSnapshotDevice creates and activates new thin-device from parent thin-device (makes snapshot)
func (p *PoolDevice) CreateSnapshotDevice(ctx context.Context, deviceName string, snapshotName string, virtualSizeBytes uint64) (retErr error) {
	baseInfo, err := p.metadata.GetDevice(ctx, deviceName)
	if err != nil {
		return fmt.Errorf("failed to query device metadata for %q: %w", deviceName, err)
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
			retErr = p.rollbackActivate(ctx, snapInfo, activeErr)
			return
		}

		// We're unable to create the devmapper device, most likely something wrong with the deviceID
		if devErr != nil {
			retErr = multierror.Append(retErr, p.metadata.MarkFaulty(ctx, snapInfo.Name))
			return
		}
	}()

	// The base device must be suspend before taking a snapshot to
	// avoid corruption.
	// https://github.com/torvalds/linux/blob/v5.7/Documentation/admin-guide/device-mapper/thin-provisioning.rst#internal-snapshots
	if p.IsLoaded(deviceName) {
		log.G(ctx).Debugf("suspending %q before taking its snapshot", deviceName)
		suspendErr := p.SuspendDevice(ctx, deviceName)
		if suspendErr != nil {
			return suspendErr
		}
		defer func() {
			err := p.ResumeDevice(ctx, deviceName)
			if err != nil {
				log.G(ctx).WithError(err).Errorf("failed to resume base device %q after taking its snapshot", baseInfo.Name)
			}
		}()
	}

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
		return fmt.Errorf(
			"failed to create snapshot %q (dev: %d) from %q (dev: %d): %w",
			snapInfo.Name,
			snapInfo.DeviceID,
			baseInfo.Name,
			baseInfo.DeviceID, err,
		)
	}

	return nil
}

// SuspendDevice flushes the outstanding IO and blocks the further IO
func (p *PoolDevice) SuspendDevice(ctx context.Context, deviceName string) error {
	if err := p.transition(ctx, deviceName, Suspending, Suspended, func() error {
		return dmsetup.SuspendDevice(deviceName)
	}); err != nil {
		return fmt.Errorf("failed to suspend device %q: %w", deviceName, err)
	}

	return nil
}

// ResumeDevice resumes IO for the given device
func (p *PoolDevice) ResumeDevice(ctx context.Context, deviceName string) error {
	if err := p.transition(ctx, deviceName, Resuming, Resumed, func() error {
		return dmsetup.ResumeDevice(deviceName)
	}); err != nil {
		return fmt.Errorf("failed to resume device %q: %w", deviceName, err)
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
		return retry(ctx, func() error {
			if !deferred && p.discardBlocks {
				err := dmsetup.DiscardBlocks(deviceName)
				if err != nil {
					if err == dmsetup.ErrInUse {
						log.G(ctx).Warnf("device %q is in use, skipping blkdiscard", deviceName)
					} else {
						return err
					}
				}
			}
			if err := dmsetup.RemoveDevice(deviceName, opts...); err != nil {
				return fmt.Errorf("failed to deactivate device: %w", err)
			}

			return nil
		})
	}); err != nil {
		return fmt.Errorf("failed to deactivate device %q: %w", deviceName, err)
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
//
//	device2: 0 204800 thin 17280 204799
//
// Where 17280 is the number of used sectors
func (p *PoolDevice) GetUsage(deviceName string) (int64, error) {
	status, err := dmsetup.Status(deviceName)
	if err != nil {
		return 0, fmt.Errorf("can't get status for device %q: %w", deviceName, err)
	}

	if len(status.Params) == 0 {
		return 0, errors.New("failed to get the number of used blocks, unexpected output from dmsetup status")
	}

	count, err := strconv.ParseInt(status.Params[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse status params: %q: %w", status.Params[0], err)
	}

	return count * dmsetup.SectorSize, nil
}

// RemoveDevice completely wipes out thin device from thin-pool and frees it's device ID
func (p *PoolDevice) RemoveDevice(ctx context.Context, deviceName string) error {
	info, err := p.metadata.GetDevice(ctx, deviceName)
	if err != nil {
		return fmt.Errorf("can't query metadata for device %q: %w", deviceName, err)
	}

	if err := p.DeactivateDevice(ctx, deviceName, false, true); err != nil {
		return err
	}

	if err := p.deleteDevice(ctx, info); err != nil {
		return err
	}

	// Remove record from meta store and free device ID
	if err := p.metadata.RemoveDevice(ctx, deviceName); err != nil {
		return fmt.Errorf("can't remove device %q metadata from store after removal: %w", deviceName, err)
	}

	return nil
}

func (p *PoolDevice) deleteDevice(ctx context.Context, info *DeviceInfo) error {
	if err := p.transition(ctx, info.Name, Removing, Removed, func() error {
		return retry(ctx, func() error {
			// Send 'delete' message to thin-pool
			e := dmsetup.DeleteDevice(p.poolName, info.DeviceID)
			// Ignores the error if the device has been deleted already.
			if e != nil && !errors.Is(e, unix.ENODATA) {
				return e
			}
			return nil
		})
	}); err != nil {
		return fmt.Errorf("failed to delete device %q (dev id: %d): %w", info.Name, info.DeviceID, err)
	}

	return nil
}

// RemovePool deactivates all child thin-devices and removes thin-pool device
func (p *PoolDevice) RemovePool(ctx context.Context) error {
	deviceNames, err := p.metadata.GetDeviceNames(ctx)
	if err != nil {
		return fmt.Errorf("can't query device names: %w", err)
	}

	var result *multierror.Error

	// Deactivate devices if any
	for _, name := range deviceNames {
		if err := p.DeactivateDevice(ctx, name, true, true); err != nil {
			result = multierror.Append(result, fmt.Errorf("failed to remove %q: %w", name, err))
		}
	}

	if err := dmsetup.RemoveDevice(p.poolName, dmsetup.RemoveWithForce, dmsetup.RemoveWithRetries, dmsetup.RemoveDeferred); err != nil {
		result = multierror.Append(result, fmt.Errorf("failed to remove pool %q: %w", p.poolName, err))
	}

	return result.ErrorOrNil()
}

// MarkDeviceState changes the device's state in metastore
func (p *PoolDevice) MarkDeviceState(ctx context.Context, name string, state DeviceState) error {
	return p.metadata.ChangeDeviceState(ctx, name, state)
}

// WalkDevices iterates all devices in pool metadata
func (p *PoolDevice) WalkDevices(ctx context.Context, cb func(info *DeviceInfo) error) error {
	return p.metadata.WalkDevices(ctx, func(info *DeviceInfo) error {
		return cb(info)
	})
}

// Close closes pool device (thin-pool will not be removed)
func (p *PoolDevice) Close() error {
	return p.metadata.Close()
}
