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

package adaptation

import (
	"github.com/containerd/nri/pkg/api"
)

//
// Alias types, consts and functions from api for the runtime.
//

// Aliased request/response/event types for api/api.proto.
// nolint
type (
	RegisterPluginRequest    = api.RegisterPluginRequest
	RegisterPluginResponse   = api.Empty
	UpdateContainersRequest  = api.UpdateContainersRequest
	UpdateContainersResponse = api.UpdateContainersResponse

	ConfigureRequest    = api.ConfigureRequest
	ConfigureResponse   = api.ConfigureResponse
	SynchronizeRequest  = api.SynchronizeRequest
	SynchronizeResponse = api.SynchronizeResponse

	CreateContainerRequest  = api.CreateContainerRequest
	CreateContainerResponse = api.CreateContainerResponse
	UpdateContainerRequest  = api.UpdateContainerRequest
	UpdateContainerResponse = api.UpdateContainerResponse
	StopContainerRequest    = api.StopContainerRequest
	StopContainerResponse   = api.StopContainerResponse

	StateChangeEvent            = api.StateChangeEvent
	StateChangeResponse         = api.StateChangeResponse
	RunPodSandboxRequest        = api.RunPodSandboxRequest
	StopPodSandboxRequest       = api.StopPodSandboxRequest
	RemovePodSandboxRequest     = api.RemovePodSandboxRequest
	StartContainerRequest       = api.StartContainerRequest
	StartContainerResponse      = api.StartContainerResponse
	RemoveContainerRequest      = api.RemoveContainerRequest
	RemoveContainerResponse     = api.RemoveContainerResponse
	PostCreateContainerRequest  = api.PostCreateContainerRequest
	PostCreateContainerResponse = api.PostCreateContainerResponse
	PostStartContainerRequest   = api.PostStartContainerRequest
	PostStartContainerResponse  = api.PostStartContainerResponse
	PostUpdateContainerRequest  = api.PostUpdateContainerRequest
	PostUpdateContainerResponse = api.PostUpdateContainerResponse

	PodSandbox               = api.PodSandbox
	LinuxPodSandbox          = api.LinuxPodSandbox
	Container                = api.Container
	ContainerAdjustment      = api.ContainerAdjustment
	LinuxContainerAdjustment = api.LinuxContainerAdjustment
	ContainerUpdate          = api.ContainerUpdate
	LinuxContainerUpdate     = api.LinuxContainerUpdate
	ContainerEviction        = api.ContainerEviction
	ContainerState           = api.ContainerState
	KeyValue                 = api.KeyValue
	Mount                    = api.Mount
	LinuxContainer           = api.LinuxContainer
	LinuxNamespace           = api.LinuxNamespace
	LinuxResources           = api.LinuxResources
	LinuxCPU                 = api.LinuxCPU
	LinuxMemory              = api.LinuxMemory
	LinuxDevice              = api.LinuxDevice
	LinuxDeviceCgroup        = api.LinuxDeviceCgroup
	HugepageLimit            = api.HugepageLimit
	Hooks                    = api.Hooks
	Hook                     = api.Hook
	POSIXRlimit              = api.POSIXRlimit

	EventMask = api.EventMask
)

// Aliased consts for api/api.proto.
// nolint
const (
	Event_UNKNOWN               = api.Event_UNKNOWN
	Event_RUN_POD_SANDBOX       = api.Event_RUN_POD_SANDBOX
	Event_STOP_POD_SANDBOX      = api.Event_STOP_POD_SANDBOX
	Event_REMOVE_POD_SANDBOX    = api.Event_REMOVE_POD_SANDBOX
	Event_CREATE_CONTAINER      = api.Event_CREATE_CONTAINER
	Event_POST_CREATE_CONTAINER = api.Event_POST_CREATE_CONTAINER
	Event_START_CONTAINER       = api.Event_START_CONTAINER
	Event_POST_START_CONTAINER  = api.Event_POST_START_CONTAINER
	Event_UPDATE_CONTAINER      = api.Event_UPDATE_CONTAINER
	Event_POST_UPDATE_CONTAINER = api.Event_POST_UPDATE_CONTAINER
	Event_STOP_CONTAINER        = api.Event_STOP_CONTAINER
	Event_REMOVE_CONTAINER      = api.Event_REMOVE_CONTAINER
	ValidEvents                 = api.ValidEvents

	ContainerState_CONTAINER_UNKNOWN = api.ContainerState_CONTAINER_UNKNOWN
	ContainerState_CONTAINER_CREATED = api.ContainerState_CONTAINER_CREATED
	ContainerState_CONTAINER_PAUSED  = api.ContainerState_CONTAINER_PAUSED
	ContainerState_CONTAINER_RUNNING = api.ContainerState_CONTAINER_RUNNING
	ContainerState_CONTAINER_STOPPED = api.ContainerState_CONTAINER_STOPPED
	ContainerState_CONTAINER_EXITED  = api.ContainerState_CONTAINER_STOPPED
)

// Aliased types for api/optional.go.
// nolint
type (
	OptionalString   = api.OptionalString
	OptionalInt      = api.OptionalInt
	OptionalInt32    = api.OptionalInt32
	OptionalUInt32   = api.OptionalUInt32
	OptionalInt64    = api.OptionalInt64
	OptionalUInt64   = api.OptionalUInt64
	OptionalBool     = api.OptionalBool
	OptionalFileMode = api.OptionalFileMode
)

// Aliased functions for api/optional.go.
// nolint
var (
	String   = api.String
	Int      = api.Int
	Int32    = api.Int32
	UInt32   = api.UInt32
	Int64    = api.Int64
	UInt64   = api.UInt64
	Bool     = api.Bool
	FileMode = api.FileMode
)

// Aliased functions for api/types.go.
// nolint
var (
	FromOCIMounts          = api.FromOCIMounts
	FromOCIHooks           = api.FromOCIHooks
	FromOCILinuxNamespaces = api.FromOCILinuxNamespaces
	FromOCILinuxDevices    = api.FromOCILinuxDevices
	FromOCILinuxResources  = api.FromOCILinuxResources
	DupStringSlice         = api.DupStringSlice
	DupStringMap           = api.DupStringMap
	IsMarkedForRemoval     = api.IsMarkedForRemoval
	MarkForRemoval         = api.MarkForRemoval
)
