//go:build linux
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
	"fmt"
)

const (
	maxDeviceID = 0xffffff // Device IDs are 24-bit numbers
)

// DeviceState represents current devmapper device state reflected in meta store
type DeviceState int

const (
	// Unknown means that device just allocated and no operations were performed
	Unknown DeviceState = iota
	// Creating means that device is going to be created
	Creating
	// Created means that devices successfully created
	Created
	// Activating means that device is going to be activated
	Activating
	// Activated means that device successfully activated
	Activated
	// Suspending means that device is going to be suspended
	Suspending
	// Suspended means that device successfully suspended
	Suspended
	// Resuming means that device is going to be resumed from suspended state
	Resuming
	// Resumed means that device successfully resumed
	Resumed
	// Deactivating means that device is going to be deactivated
	Deactivating
	// Deactivated means that device successfully deactivated
	Deactivated
	// Removing means that device is going to be removed
	Removing
	// Removed means that device successfully removed but not yet deleted from meta store
	Removed
	// Faulty means that the device is errored and the snapshotter failed to rollback it
	Faulty
)

func (s DeviceState) String() string {
	switch s {
	case Creating:
		return "Creating"
	case Created:
		return "Created"
	case Activating:
		return "Activating"
	case Activated:
		return "Activated"
	case Suspending:
		return "Suspending"
	case Suspended:
		return "Suspended"
	case Resuming:
		return "Resuming"
	case Resumed:
		return "Resumed"
	case Deactivating:
		return "Deactivating"
	case Deactivated:
		return "Deactivated"
	case Removing:
		return "Removing"
	case Removed:
		return "Removed"
	case Faulty:
		return "Faulty"
	default:
		return fmt.Sprintf("unknown %d", s)
	}
}

// DeviceInfo represents metadata for thin device within thin-pool
type DeviceInfo struct {
	// DeviceID is a 24-bit number assigned to a device within thin-pool device
	DeviceID uint32 `json:"device_id"`
	// Size is a thin device size
	Size uint64 `json:"size"`
	// Name is a device name to be used in /dev/mapper/
	Name string `json:"name"`
	// ParentName is a name of parent device (if snapshot)
	ParentName string `json:"parent_name"`
	// State represents current device state
	State DeviceState `json:"state"`
	// Error details if device state change failed
	Error string `json:"error"`
}
