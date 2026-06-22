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
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.etcd.io/bbolt"
)

var (
	testCtx = context.Background()
)

func TestPoolMetadata_AddDevice(t *testing.T) {
	store := createStore(t)
	defer cleanupStore(t, store)

	expected := &DeviceInfo{
		Name:       "test2",
		ParentName: "test1",
		Size:       1,
		State:      Activated,
	}

	err := store.AddDevice(testCtx, expected)
	assert.NoError(t, err)

	result, err := store.GetDevice(testCtx, "test2")
	assert.NoError(t, err)

	assert.Equal(t, expected.Name, result.Name)
	assert.Equal(t, expected.ParentName, result.ParentName)
	assert.Equal(t, expected.Size, result.Size)
	assert.Equal(t, expected.State, result.State)
	assert.NotZero(t, result.DeviceID, 0)
	assert.Equal(t, expected.DeviceID, result.DeviceID)
}

func TestPoolMetadata_AddDeviceRollback(t *testing.T) {
	store := createStore(t)
	defer cleanupStore(t, store)

	err := store.AddDevice(testCtx, &DeviceInfo{Name: ""})
	assert.True(t, err != nil)

	_, err = store.GetDevice(testCtx, "")
	assert.Equal(t, ErrNotFound, err)
}

func TestPoolMetadata_AddDeviceDuplicate(t *testing.T) {
	store := createStore(t)
	defer cleanupStore(t, store)

	err := store.AddDevice(testCtx, &DeviceInfo{Name: "test"})
	assert.NoError(t, err)

	err = store.AddDevice(testCtx, &DeviceInfo{Name: "test"})
	assert.True(t, errors.Is(err, ErrAlreadyExists))
}

func TestPoolMetadata_ReuseDeviceID(t *testing.T) {
	store := createStore(t)
	defer cleanupStore(t, store)

	info1 := &DeviceInfo{Name: "test1"}
	err := store.AddDevice(testCtx, info1)
	assert.NoError(t, err)

	info2 := &DeviceInfo{Name: "test2"}
	err = store.AddDevice(testCtx, info2)
	assert.NoError(t, err)

	assert.NotEqual(t, info1.DeviceID, info2.DeviceID)
	assert.NotZero(t, info1.DeviceID)

	err = store.RemoveDevice(testCtx, info2.Name)
	assert.NoError(t, err)

	info3 := &DeviceInfo{Name: "test3"}
	err = store.AddDevice(testCtx, info3)
	assert.NoError(t, err)

	assert.Equal(t, info2.DeviceID, info3.DeviceID)
}

func TestPoolMetadata_RemoveDevice(t *testing.T) {
	store := createStore(t)
	defer cleanupStore(t, store)

	err := store.AddDevice(testCtx, &DeviceInfo{Name: "test"})
	assert.NoError(t, err)

	err = store.RemoveDevice(testCtx, "test")
	assert.NoError(t, err)

	_, err = store.GetDevice(testCtx, "test")
	assert.Equal(t, ErrNotFound, err)
}

func TestPoolMetadata_UpdateDevice(t *testing.T) {
	store := createStore(t)
	defer cleanupStore(t, store)

	oldInfo := &DeviceInfo{
		Name:       "test1",
		ParentName: "test2",
		Size:       3,
		State:      Activated,
	}

	err := store.AddDevice(testCtx, oldInfo)
	assert.NoError(t, err)

	err = store.UpdateDevice(testCtx, oldInfo.Name, func(info *DeviceInfo) error {
		info.ParentName = "test5"
		info.Size = 6
		info.State = Created
		return nil
	})

	assert.NoError(t, err)

	newInfo, err := store.GetDevice(testCtx, "test1")
	assert.NoError(t, err)

	assert.Equal(t, "test1", newInfo.Name)
	assert.Equal(t, "test5", newInfo.ParentName)
	assert.EqualValues(t, newInfo.Size, 6)
	assert.Equal(t, Created, newInfo.State)
}

func TestPoolMetadata_MarkFaulty(t *testing.T) {
	store := createStore(t)
	defer cleanupStore(t, store)

	info := &DeviceInfo{Name: "test"}
	err := store.AddDevice(testCtx, info)
	assert.NoError(t, err)

	err = store.MarkFaulty(testCtx, "test")
	assert.NoError(t, err)

	saved, err := store.GetDevice(testCtx, info.Name)
	assert.NoError(t, err)
	assert.Equal(t, saved.State, Faulty)
	assert.True(t, saved.DeviceID > 0)

	// Make sure a device ID marked as faulty as well
	err = store.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(deviceIDBucketName)
		key := strconv.FormatUint(uint64(saved.DeviceID), 10)
		value := bucket.Get([]byte(key))
		assert.Equal(t, value[0], byte(deviceFaulty))
		return nil
	})
	assert.NoError(t, err)
}

func TestPoolMetadata_WalkDevices(t *testing.T) {
	store := createStore(t)
	defer cleanupStore(t, store)

	err := store.AddDevice(testCtx, &DeviceInfo{Name: "device1", DeviceID: 1, State: Created})
	assert.NoError(t, err)

	err = store.AddDevice(testCtx, &DeviceInfo{Name: "device2", DeviceID: 2, State: Faulty})
	assert.NoError(t, err)

	called := 0
	err = store.WalkDevices(testCtx, func(info *DeviceInfo) error {
		called++
		switch called {
		case 1:
			assert.Equal(t, "device1", info.Name)
			assert.Equal(t, uint32(1), info.DeviceID)
			assert.Equal(t, Created, info.State)
		case 2:
			assert.Equal(t, "device2", info.Name)
			assert.Equal(t, uint32(2), info.DeviceID)
			assert.Equal(t, Faulty, info.State)
		default:
			t.Error("unexpected walk call")
		}

		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, called, 2)
}

func TestPoolMetadata_GetDeviceNames(t *testing.T) {
	store := createStore(t)
	defer cleanupStore(t, store)

	err := store.AddDevice(testCtx, &DeviceInfo{Name: "test1"})
	assert.NoError(t, err)

	err = store.AddDevice(testCtx, &DeviceInfo{Name: "test2"})
	assert.NoError(t, err)

	names, err := store.GetDeviceNames(testCtx)
	assert.NoError(t, err)
	assert.Len(t, names, 2)

	assert.Equal(t, "test1", names[0])
	assert.Equal(t, "test2", names[1])
}

func createStore(t *testing.T) (store *PoolMetadata) {
	path := filepath.Join(t.TempDir(), "test.db")
	metadata, err := NewPoolMetadata(path)
	assert.NoError(t, err)

	return metadata
}

func cleanupStore(t *testing.T, store *PoolMetadata) {
	err := store.Close()
	assert.Nil(t, err, "failed to close metadata store")
}
