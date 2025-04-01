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
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/containerd/errdefs"
	bolt "go.etcd.io/bbolt"
	errbolt "go.etcd.io/bbolt/errors"
)

type (
	// DeviceInfoCallback is a callback used for device updates
	DeviceInfoCallback func(deviceInfo *DeviceInfo) error
)

type deviceIDState byte

const (
	deviceFree deviceIDState = iota
	deviceTaken
	deviceFaulty
)

// Bucket names
var (
	devicesBucketName  = []byte("devices")    // Contains thin devices metadata <device_name>=<DeviceInfo>
	deviceIDBucketName = []byte("device_ids") // Tracks used device ids <device_id_[0..maxDeviceID)>=<byte_[0/1]>
)

var (
	// ErrNotFound represents an error returned when object not found in meta store
	ErrNotFound = errdefs.ErrNotFound
	// ErrAlreadyExists represents an error returned when object can't be duplicated in meta store
	ErrAlreadyExists = errdefs.ErrAlreadyExists
)

// PoolMetadata keeps device info for the given thin-pool device, generates next available device ids,
// and tracks devmapper transaction numbers
type PoolMetadata struct {
	db *bolt.DB
}

// NewPoolMetadata creates new or opens existing pool metadata database
func NewPoolMetadata(dbfile string) (*PoolMetadata, error) {
	db, err := bolt.Open(dbfile, 0600, nil)
	if err != nil {
		return nil, err
	}

	metadata := &PoolMetadata{db: db}
	if err := metadata.ensureDatabaseInitialized(); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return metadata, nil
}

// ensureDatabaseInitialized creates buckets required for metadata store in order
// to avoid bucket existence checks across the code
func (m *PoolMetadata) ensureDatabaseInitialized() error {
	return m.db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(devicesBucketName); err != nil {
			return err
		}

		if _, err := tx.CreateBucketIfNotExists(deviceIDBucketName); err != nil {
			return err
		}

		return nil
	})
}

// AddDevice saves device info to database.
func (m *PoolMetadata) AddDevice(ctx context.Context, info *DeviceInfo) error {
	err := m.db.Update(func(tx *bolt.Tx) error {
		devicesBucket := tx.Bucket(devicesBucketName)

		// Make sure device name is unique. If there is already a device with the same name,
		// but in Faulty state, give it a try with another devmapper device ID.
		// See https://github.com/containerd/containerd/pull/3436 for more context.
		var existing DeviceInfo
		if err := getObject(devicesBucket, info.Name, &existing); err == nil && existing.State != Faulty {
			return fmt.Errorf("device %q is already there %+v: %w", info.Name, existing, ErrAlreadyExists)
		}

		// Find next available device ID
		deviceID, err := getNextDeviceID(tx)
		if err != nil {
			return err
		}

		info.DeviceID = deviceID

		return putObject(devicesBucket, info.Name, info, true)
	})

	if err != nil {
		return fmt.Errorf("failed to save metadata for device %q (parent: %q): %w", info.Name, info.ParentName, err)
	}

	return nil
}

// ChangeDeviceState changes the device state given the device name in devices bucket.
func (m *PoolMetadata) ChangeDeviceState(ctx context.Context, name string, state DeviceState) error {
	return m.UpdateDevice(ctx, name, func(deviceInfo *DeviceInfo) error {
		deviceInfo.State = state
		return nil
	})
}

// MarkFaulty marks the given device and corresponding devmapper device ID as faulty.
// The snapshotter might attempt to recreate a device in 'Faulty' state with another devmapper ID in
// subsequent calls, and in case of success its status will be changed to 'Created' or 'Activated'.
// The devmapper dev ID will remain in 'deviceFaulty' state until manually handled by a user.
func (m *PoolMetadata) MarkFaulty(ctx context.Context, name string) error {
	return m.db.Update(func(tx *bolt.Tx) error {
		var (
			device    = DeviceInfo{}
			devBucket = tx.Bucket(devicesBucketName)
		)

		if err := getObject(devBucket, name, &device); err != nil {
			return err
		}

		device.State = Faulty

		if err := putObject(devBucket, name, &device, true); err != nil {
			return err
		}

		return markDeviceID(tx, device.DeviceID, deviceFaulty)
	})
}

// getNextDeviceID finds the next free device ID by taking a cursor
// through the deviceIDBucketName bucket and finding the next sequentially
// unassigned ID. Device ID state is marked by a byte deviceFree or
// deviceTaken. Low device IDs will be reused sooner.
func getNextDeviceID(tx *bolt.Tx) (uint32, error) {
	bucket := tx.Bucket(deviceIDBucketName)
	cursor := bucket.Cursor()

	// Check if any device id can be reused.
	// Bolt stores its keys in byte-sorted order within a bucket.
	// This makes sequential iteration extremely fast.
	for key, taken := cursor.First(); key != nil; key, taken = cursor.Next() {
		isFree := taken[0] == byte(deviceFree)
		if !isFree {
			continue
		}

		parsedID, err := strconv.ParseUint(string(key), 10, 32)
		if err != nil {
			return 0, err
		}

		id := uint32(parsedID)
		if err := markDeviceID(tx, id, deviceTaken); err != nil {
			return 0, err
		}

		return id, nil
	}

	// Try allocate new device ID
	seq, err := bucket.NextSequence()
	if err != nil {
		return 0, err
	}

	if seq >= maxDeviceID {
		return 0, errors.New("dm-meta: couldn't find free device key")
	}

	id := uint32(seq)
	if err := markDeviceID(tx, id, deviceTaken); err != nil {
		return 0, err
	}

	return id, nil
}

// markDeviceID marks a device as deviceFree or deviceTaken
func markDeviceID(tx *bolt.Tx, deviceID uint32, state deviceIDState) error {
	var (
		bucket = tx.Bucket(deviceIDBucketName)
		key    = strconv.FormatUint(uint64(deviceID), 10)
		value  = []byte{byte(state)}
	)

	if err := bucket.Put([]byte(key), value); err != nil {
		return fmt.Errorf("failed to free device id %q: %w", key, err)
	}

	return nil
}

// UpdateDevice updates device info in metadata store.
// The callback should be used to indicate whether device info update was successful or not.
// An error returned from the callback will rollback the update transaction in the database.
// Name and Device ID are not allowed to change.
func (m *PoolMetadata) UpdateDevice(ctx context.Context, name string, fn DeviceInfoCallback) error {
	return m.db.Update(func(tx *bolt.Tx) error {
		var (
			device = &DeviceInfo{}
			bucket = tx.Bucket(devicesBucketName)
		)

		if err := getObject(bucket, name, device); err != nil {
			return fmt.Errorf("failed to get object %w", err)
		}

		// Don't allow changing these values, keep things in sync with devmapper
		name := device.Name
		devID := device.DeviceID

		if err := fn(device); err != nil {
			return fmt.Errorf("failed to run DeviceInfoCallback %w", err)
		}

		if name != device.Name {
			return fmt.Errorf("failed to update device info, name didn't match: %q %q", name, device.Name)
		}

		if devID != device.DeviceID {
			return fmt.Errorf("failed to update device info, device id didn't match: %d %d", devID, device.DeviceID)
		}

		return putObject(bucket, name, device, true)
	})
}

// GetDevice retrieves device info by name from database
func (m *PoolMetadata) GetDevice(ctx context.Context, name string) (*DeviceInfo, error) {
	var (
		dev DeviceInfo
		err error
	)

	err = m.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(devicesBucketName)
		return getObject(bucket, name, &dev)
	})

	return &dev, err
}

// RemoveDevice removes device info from store.
func (m *PoolMetadata) RemoveDevice(ctx context.Context, name string) error {
	return m.db.Update(func(tx *bolt.Tx) error {
		var (
			device = &DeviceInfo{}
			bucket = tx.Bucket(devicesBucketName)
		)

		if err := getObject(bucket, name, device); err != nil {
			return err
		}

		if err := bucket.Delete([]byte(name)); err != nil {
			return fmt.Errorf("failed to delete device info for %q: %w", name, err)
		}

		return markDeviceID(tx, device.DeviceID, deviceFree)
	})
}

// WalkDevices walks all devmapper devices in metadata store and invokes the callback with device info.
// The provided callback function must not modify the bucket.
func (m *PoolMetadata) WalkDevices(ctx context.Context, cb func(info *DeviceInfo) error) error {
	return m.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(devicesBucketName)
		return bucket.ForEach(func(key, value []byte) error {
			device := &DeviceInfo{}
			if err := json.Unmarshal(value, device); err != nil {
				return fmt.Errorf("failed to unmarshal %s: %w", key, err)
			}

			return cb(device)
		})
	})
}

// GetDeviceNames retrieves the list of device names currently stored in database
func (m *PoolMetadata) GetDeviceNames(ctx context.Context) ([]string, error) {
	var (
		names []string
		err   error
	)

	err = m.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(devicesBucketName)
		return bucket.ForEach(func(k, _ []byte) error {
			names = append(names, string(k))
			return nil
		})
	})

	if err != nil {
		return nil, err
	}

	return names, nil
}

// Close closes metadata store
func (m *PoolMetadata) Close() error {
	if err := m.db.Close(); err != nil && err != errbolt.ErrDatabaseNotOpen {
		return err
	}

	return nil
}

func putObject(bucket *bolt.Bucket, key string, obj interface{}, overwrite bool) error {
	keyBytes := []byte(key)

	if !overwrite && bucket.Get(keyBytes) != nil {
		return fmt.Errorf("object with key %q already exists", key)
	}

	data, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("failed to marshal object with key %q: %w", key, err)
	}

	if err := bucket.Put(keyBytes, data); err != nil {
		return fmt.Errorf("failed to insert object with key %q: %w", key, err)
	}

	return nil
}

func getObject(bucket *bolt.Bucket, key string, obj interface{}) error {
	data := bucket.Get([]byte(key))
	if data == nil {
		return ErrNotFound
	}

	if obj != nil {
		if err := json.Unmarshal(data, obj); err != nil {
			return fmt.Errorf("failed to unmarshal object with key %q: %w", key, err)
		}
	}

	return nil
}
