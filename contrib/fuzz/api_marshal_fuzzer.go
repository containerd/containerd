//go:build gofuzz
// +build gofuzz

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

package fuzz

import (
	"fmt"
	"reflect"

	"github.com/containerd/containerd/api/events"
	containers "github.com/containerd/containerd/api/services/containers/v1"
	content "github.com/containerd/containerd/api/services/content/v1"
	diff "github.com/containerd/containerd/api/services/diff/v1"
	servicesEvents "github.com/containerd/containerd/api/services/events/v1"
	images "github.com/containerd/containerd/api/services/images/v1"
	introspection "github.com/containerd/containerd/api/services/introspection/v1"
	leases "github.com/containerd/containerd/api/services/leases/v1"
	namespaces "github.com/containerd/containerd/api/services/namespaces/v1"
	snapshots "github.com/containerd/containerd/api/services/snapshots/v1"
	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	ttrpcEvents "github.com/containerd/containerd/api/services/ttrpc/events/v1"
	version "github.com/containerd/containerd/api/services/version/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/api/types/task"
)

const noOfTargets = 152

// FuzzApiMarshaling implements a fuzzer that
// tests the marshaling and unmarshaling of
// the Containerd API definitions.
func FuzzApiMarshaling(data []byte) int {
	if len(data) < 10 {
		return 0
	}
	op := int(data[0])
	inputData := data[1:]
	if op%noOfTargets == 0 {
		fuzzTypesMount(inputData)
	} else if op%noOfTargets == 1 {
		fuzzTypesDescriptor(inputData)
	} else if op%noOfTargets == 2 {
		fuzzTaskProcess(inputData)
	} else if op%noOfTargets == 3 {
		fuzzTaskProcessInfo(inputData)
	} else if op%noOfTargets == 4 {
		fuzzTypesMetric(inputData)
	} else if op%noOfTargets == 5 {
		fuzzTypesPlatform(inputData)
	} else if op%noOfTargets == 6 {
		fuzzEventsContainerCreate(inputData)
	} else if op%noOfTargets == 7 {
		fuzzEventsContainerCreateRuntime(inputData)
	} else if op%noOfTargets == 8 {
		fuzzEventsContainerUpdate(inputData)
	} else if op%noOfTargets == 9 {
		fuzzEventsContainerDelete(inputData)
	} else if op%noOfTargets == 10 {
		fuzzEventsImageCreate(inputData)
	} else if op%noOfTargets == 11 {
		fuzzEventsImageUpdate(inputData)
	} else if op%noOfTargets == 12 {
		fuzzEventsImageDelete(inputData)
	} else if op%noOfTargets == 13 {
		fuzzEventsContentDelete(inputData)
	} else if op%noOfTargets == 14 {
		fuzzEventsNamespaceCreate(inputData)
	} else if op%noOfTargets == 15 {
		fuzzEventsNamespaceUpdate(inputData)
	} else if op%noOfTargets == 16 {
		fuzzEventsNamespaceDelete(inputData)
	} else if op%noOfTargets == 17 {
		fuzzEventsSnapshotPrepare(inputData)
	} else if op%noOfTargets == 18 {
		fuzzEventsSnapshotCommit(inputData)
	} else if op%noOfTargets == 19 {
		fuzzEventsSnapshotRemove(inputData)
	} else if op%noOfTargets == 20 {
		fuzzEventsTaskCreate(inputData)
	} else if op%noOfTargets == 21 {
		fuzzEventsTaskStart(inputData)
	} else if op%noOfTargets == 22 {
		fuzzEventsTaskDelete(inputData)
	} else if op%noOfTargets == 23 {
		fuzzEventsTaskIO(inputData)
	} else if op%noOfTargets == 24 {
		fuzzEventsTaskExit(inputData)
	} else if op%noOfTargets == 25 {
		fuzzEventsTaskOOM(inputData)
	} else if op%noOfTargets == 26 {
		fuzzEventsTaskExecAdded(inputData)
	} else if op%noOfTargets == 27 {
		fuzzEventsTaskExecStarted(inputData)
	} else if op%noOfTargets == 28 {
		fuzzEventsTaskPaused(inputData)
	} else if op%noOfTargets == 29 {
		fuzzEventsTaskResumed(inputData)
	} else if op%noOfTargets == 30 {
		fuzzEventsTaskCheckpointed(inputData)
	} else if op%noOfTargets == 31 {
		fuzzNamespacesNamespace(inputData)
	} else if op%noOfTargets == 32 {
		fuzzNamespacesGetNamespaceRequest(inputData)
	} else if op%noOfTargets == 33 {
		fuzzNamespacesGetNamespaceResponse(inputData)
	} else if op%noOfTargets == 34 {
		fuzzNamespacesListNamespacesRequest(inputData)
	} else if op%noOfTargets == 35 {
		fuzzNamespacesListNamespacesResponse(inputData)
	} else if op%noOfTargets == 36 {
		fuzzNamespacesCreateNamespaceRequest(inputData)
	} else if op%noOfTargets == 37 {
		fuzzNamespacesCreateNamespaceResponse(inputData)
	} else if op%noOfTargets == 38 {
		fuzzNamespacesUpdateNamespaceRequest(inputData)
	} else if op%noOfTargets == 39 {
		fuzzNamespacesUpdateNamespaceResponse(inputData)
	} else if op%noOfTargets == 40 {
		fuzzNamespacesDeleteNamespaceRequest(inputData)
	} else if op%noOfTargets == 41 {
		fuzzttrpcEventsForwardRequest(inputData)
	} else if op%noOfTargets == 42 {
		fuzzttrpcEventsEnvelope(inputData)
	} else if op%noOfTargets == 43 {
		fuzzVersionVersionResponse(inputData)
	} else if op%noOfTargets == 44 {
		fuzzImagesImage(inputData)
	} else if op%noOfTargets == 45 {
		fuzzImagesGetImageRequest(inputData)
	} else if op%noOfTargets == 46 {
		fuzzImagesGetImageResponset(inputData)
	} else if op%noOfTargets == 47 {
		fuzzImagesCreateImageRequest(inputData)
	} else if op%noOfTargets == 48 {
		fuzzImagesCreateImageResponse(inputData)
	} else if op%noOfTargets == 49 {
		fuzzImagesUpdateImageRequest(inputData)
	} else if op%noOfTargets == 50 {
		fuzzImagesUpdateImageResponse(inputData)
	} else if op%noOfTargets == 51 {
		fuzzImagesListImagesRequest(inputData)
	} else if op%noOfTargets == 52 {
		fuzzImagesListImagesResponse(inputData)
	} else if op%noOfTargets == 53 {
		fuzzImagesDeleteImageRequest(inputData)
	} else if op%noOfTargets == 54 {
		fuzzIntrospectionPlugin(inputData)
	} else if op%noOfTargets == 55 {
		fuzzIntrospectionPluginsRequest(inputData)
	} else if op%noOfTargets == 56 {
		fuzzIntrospectionPluginsResponse(inputData)
	} else if op%noOfTargets == 57 {
		fuzzIntrospectionServerResponse(inputData)
	} else if op%noOfTargets == 58 {
		fuzzDiffApplyRequest(inputData)
	} else if op%noOfTargets == 59 {
		fuzzDiffApplyResponse(inputData)
	} else if op%noOfTargets == 60 {
		fuzzDiffDiffRequest(inputData)
	} else if op%noOfTargets == 61 {
		fuzzDiffDiffResponse(inputData)
	} else if op%noOfTargets == 62 {
		fuzzContentInfo(inputData)
	} else if op%noOfTargets == 63 {
		fuzzContentInfoRequest(inputData)
	} else if op%noOfTargets == 64 {
		fuzzContentInfoResponse(inputData)
	} else if op%noOfTargets == 65 {
		fuzzContentUpdateRequest(inputData)
	} else if op%noOfTargets == 66 {
		fuzzContentUpdateResponse(inputData)
	} else if op%noOfTargets == 67 {
		fuzzContentListContentRequest(inputData)
	} else if op%noOfTargets == 68 {
		fuzzContentListContentResponse(inputData)
	} else if op%noOfTargets == 69 {
		fuzzContentDeleteContentRequest(inputData)
	} else if op%noOfTargets == 70 {
		fuzzContentReadContentRequest(inputData)
	} else if op%noOfTargets == 71 {
		fuzzContentReadContentResponse(inputData)
	} else if op%noOfTargets == 72 {
		fuzzContentStatus(inputData)
	} else if op%noOfTargets == 73 {
		fuzzContentStatusRequest(inputData)
	} else if op%noOfTargets == 74 {
		fuzzContentStatusResponse(inputData)
	} else if op%noOfTargets == 75 {
		fuzzContentListStatusesRequest(inputData)
	} else if op%noOfTargets == 76 {
		fuzzContentListStatusesResponse(inputData)
	} else if op%noOfTargets == 77 {
		fuzzContentWriteContentRequest(inputData)
	} else if op%noOfTargets == 78 {
		fuzzContentWriteContentResponse(inputData)
	} else if op%noOfTargets == 79 {
		fuzzContentAbortRequest(inputData)
	} else if op%noOfTargets == 80 {
		fuzzSnapshotsPrepareSnapshotRequest(inputData)
	} else if op%noOfTargets == 81 {
		fuzzSnapshotsPrepareSnapshotResponse(inputData)
	} else if op%noOfTargets == 82 {
		fuzzSnapshotsViewSnapshotRequest(inputData)
	} else if op%noOfTargets == 83 {
		fuzzSnapshotsViewSnapshotResponse(inputData)
	} else if op%noOfTargets == 84 {
		fuzzSnapshotsMountsRequest(inputData)
	} else if op%noOfTargets == 85 {
		fuzzSnapshotsMountsResponse(inputData)
	} else if op%noOfTargets == 86 {
		fuzzSnapshotsRemoveSnapshotRequest(inputData)
	} else if op%noOfTargets == 87 {
		fuzzSnapshotsCommitSnapshotRequest(inputData)
	} else if op%noOfTargets == 88 {
		fuzzSnapshotsStatSnapshotRequest(inputData)
	} else if op%noOfTargets == 89 {
		fuzzSnapshotsInfo(inputData)
	} else if op%noOfTargets == 90 {
		fuzzSnapshotsStatSnapshotResponse(inputData)
	} else if op%noOfTargets == 91 {
		fuzzSnapshotsUpdateSnapshotRequest(inputData)
	} else if op%noOfTargets == 92 {
		fuzzSnapshotsUpdateSnapshotResponse(inputData)
	} else if op%noOfTargets == 93 {
		fuzzSnapshotsListSnapshotsRequest(inputData)
	} else if op%noOfTargets == 94 {
		fuzzSnapshotsListSnapshotsResponse(inputData)
	} else if op%noOfTargets == 95 {
		fuzzSnapshotsUsageRequest(inputData)
	} else if op%noOfTargets == 96 {
		fuzzSnapshotsUsageResponse(inputData)
	} else if op%noOfTargets == 97 {
		fuzzSnapshotsCleanupRequest(inputData)
	} else if op%noOfTargets == 98 {
		fuzzLeasesLease(inputData)
	} else if op%noOfTargets == 99 {
		fuzzLeasesCreateRequest(inputData)
	} else if op%noOfTargets == 100 {
		fuzzLeasesCreateResponse(inputData)
	} else if op%noOfTargets == 101 {
		fuzzLeasesDeleteRequest(inputData)
	} else if op%noOfTargets == 102 {
		fuzzLeasesListRequest(inputData)
	} else if op%noOfTargets == 103 {
		fuzzLeasesListResponse(inputData)
	} else if op%noOfTargets == 104 {
		fuzzLeasesResource(inputData)
	} else if op%noOfTargets == 105 {
		fuzzLeasesAddResourceRequest(inputData)
	} else if op%noOfTargets == 106 {
		fuzzLeasesDeleteResourceRequest(inputData)
	} else if op%noOfTargets == 107 {
		fuzzLeasesListResourcesRequest(inputData)
	} else if op%noOfTargets == 108 {
		fuzzLeasesListResourcesResponse(inputData)
	} else if op%noOfTargets == 109 {
		fuzzTasksCreateTaskRequest(inputData)
	} else if op%noOfTargets == 110 {
		fuzzTasksCreateTaskResponse(inputData)
	} else if op%noOfTargets == 111 {
		fuzzTasksStartRequest(inputData)
	} else if op%noOfTargets == 112 {
		fuzzTasksStartResponse(inputData)
	} else if op%noOfTargets == 113 {
		fuzzTasksDeleteTaskRequest(inputData)
	} else if op%noOfTargets == 114 {
		fuzzTasksDeleteResponse(inputData)
	} else if op%noOfTargets == 115 {
		fuzzTasksDeleteProcessRequest(inputData)
	} else if op%noOfTargets == 116 {
		fuzzTasksGetRequest(inputData)
	} else if op%noOfTargets == 117 {
		fuzzTasksGetResponse(inputData)
	} else if op%noOfTargets == 118 {
		fuzzTasksListTasksRequest(inputData)
	} else if op%noOfTargets == 119 {
		fuzzTasksListTasksResponse(inputData)
	} else if op%noOfTargets == 120 {
		fuzzTasksKillRequest(inputData)
	} else if op%noOfTargets == 121 {
		fuzzTasksExecProcessRequest(inputData)
	} else if op%noOfTargets == 122 {
		fuzzTasksExecProcessResponse(inputData)
	} else if op%noOfTargets == 123 {
		fuzzTasksResizePtyRequest(inputData)
	} else if op%noOfTargets == 124 {
		fuzzTasksCloseIORequest(inputData)
	} else if op%noOfTargets == 125 {
		fuzzTasksPauseTaskRequest(inputData)
	} else if op%noOfTargets == 126 {
		fuzzTasksResumeTaskRequest(inputData)
	} else if op%noOfTargets == 127 {
		fuzzTasksListPidsRequest(inputData)
	} else if op%noOfTargets == 128 {
		fuzzTasksListPidsResponse(inputData)
	} else if op%noOfTargets == 129 {
		fuzzTasksCheckpointTaskRequest(inputData)
	} else if op%noOfTargets == 130 {
		fuzzTasksCheckpointTaskResponse(inputData)
	} else if op%noOfTargets == 131 {
		fuzzTasksUpdateTaskRequest(inputData)
	} else if op%noOfTargets == 132 {
		fuzzTasksMetricsRequest(inputData)
	} else if op%noOfTargets == 133 {
		fuzzTasksMetricsResponse(inputData)
	} else if op%noOfTargets == 134 {
		fuzzTasksWaitRequest(inputData)
	} else if op%noOfTargets == 135 {
		fuzzTasksWaitResponse(inputData)
	} else if op%noOfTargets == 136 {
		fuzzContainersContainer(inputData)
	} else if op%noOfTargets == 137 {
		fuzzContainersContainer_Runtime(inputData)
	} else if op%noOfTargets == 138 {
		fuzzContainersGetContainerRequest(inputData)
	} else if op%noOfTargets == 139 {
		fuzzContainersGetContainerResponse(inputData)
	} else if op%noOfTargets == 140 {
		fuzzContainersListContainersRequest(inputData)
	} else if op%noOfTargets == 141 {
		fuzzContainersListContainersResponse(inputData)
	} else if op%noOfTargets == 142 {
		fuzzContainersCreateContainerRequest(inputData)
	} else if op%noOfTargets == 143 {
		fuzzContainersCreateContainerResponse(inputData)
	} else if op%noOfTargets == 144 {
		fuzzContainersUpdateContainerRequest(inputData)
	} else if op%noOfTargets == 145 {
		fuzzContainersUpdateContainerResponse(inputData)
	} else if op%noOfTargets == 146 {
		fuzzContainersDeleteContainerRequest(inputData)
	} else if op%noOfTargets == 147 {
		fuzzContainersListContainerMessage(inputData)
	} else if op%noOfTargets == 148 {
		fuzzEventsPublishRequest(inputData)
	} else if op%noOfTargets == 149 {
		fuzzEventsForwardRequest(inputData)
	} else if op%noOfTargets == 150 {
		fuzzEventsSubscribeRequest(inputData)
	} else if op%noOfTargets == 151 {
		fuzzEventsEnvelope(inputData)
	}
	return 1
}

func checkData(correctData1, correctData2 []byte) {
	if len(correctData1) != len(correctData2) {
		panic("Len should be equal.")
	}
}

// Everything below here are the internal targets
// that are invoked in FuzzApiMarshaling()

func fuzzTypesMount(data []byte) {
	m1 := &types.Mount{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &types.Mount{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTypesDescriptor(data []byte) {
	m1 := &types.Descriptor{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &types.Descriptor{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTaskProcess(data []byte) {
	m1 := &task.Process{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &task.Process{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTaskProcessInfo(data []byte) {
	m1 := &task.ProcessInfo{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &task.ProcessInfo{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTypesMetric(data []byte) {
	m1 := &types.Metric{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &types.Metric{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTypesPlatform(data []byte) {
	m1 := &types.Platform{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &types.Platform{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsContainerCreate(data []byte) {
	m1 := &events.ContainerCreate{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.ContainerCreate{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsContainerCreateRuntime(data []byte) {
	m1 := &events.ContainerCreate_Runtime{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.ContainerCreate_Runtime{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsContainerUpdate(data []byte) {
	m1 := &events.ContainerUpdate{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.ContainerUpdate{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsContainerDelete(data []byte) {
	m1 := &events.ContainerDelete{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.ContainerDelete{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsImageCreate(data []byte) {
	m1 := &events.ImageCreate{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.ImageCreate{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsImageUpdate(data []byte) {
	m1 := &events.ImageUpdate{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.ImageUpdate{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsImageDelete(data []byte) {
	m1 := &events.ImageDelete{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.ImageDelete{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsContentDelete(data []byte) {
	m1 := &events.ContentDelete{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.ContentDelete{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsNamespaceCreate(data []byte) {
	m1 := &events.NamespaceCreate{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.NamespaceCreate{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsNamespaceUpdate(data []byte) {
	m1 := &events.NamespaceUpdate{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.NamespaceUpdate{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsNamespaceDelete(data []byte) {
	m1 := &events.NamespaceDelete{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.NamespaceDelete{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsSnapshotPrepare(data []byte) {
	m1 := &events.SnapshotPrepare{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.SnapshotPrepare{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsSnapshotCommit(data []byte) {
	m1 := &events.SnapshotCommit{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.SnapshotCommit{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsSnapshotRemove(data []byte) {
	m1 := &events.SnapshotRemove{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.SnapshotRemove{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsTaskCreate(data []byte) {
	m1 := &events.TaskCreate{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.TaskCreate{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsTaskStart(data []byte) {
	m1 := &events.TaskStart{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.TaskStart{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsTaskDelete(data []byte) {
	m1 := &events.TaskDelete{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.TaskDelete{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsTaskIO(data []byte) {
	m1 := &events.TaskIO{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.TaskIO{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsTaskExit(data []byte) {
	m1 := &events.TaskExit{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.TaskExit{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsTaskOOM(data []byte) {
	m1 := &events.TaskOOM{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.TaskOOM{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsTaskExecAdded(data []byte) {
	m1 := &events.TaskExecAdded{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.TaskExecAdded{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsTaskExecStarted(data []byte) {
	m1 := &events.TaskExecStarted{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.TaskExecStarted{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsTaskPaused(data []byte) {
	m1 := &events.TaskPaused{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.TaskPaused{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsTaskResumed(data []byte) {
	m1 := &events.TaskResumed{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.TaskResumed{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsTaskCheckpointed(data []byte) {
	m1 := &events.TaskCheckpointed{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &events.TaskCheckpointed{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzNamespacesNamespace(data []byte) {
	m1 := &namespaces.Namespace{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &namespaces.Namespace{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzNamespacesGetNamespaceRequest(data []byte) {
	m1 := &namespaces.GetNamespaceRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &namespaces.GetNamespaceRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzNamespacesGetNamespaceResponse(data []byte) {
	m1 := &namespaces.GetNamespaceResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &namespaces.GetNamespaceResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzNamespacesListNamespacesRequest(data []byte) {
	m1 := &namespaces.ListNamespacesRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &namespaces.ListNamespacesRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzNamespacesListNamespacesResponse(data []byte) {
	m1 := &namespaces.ListNamespacesResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &namespaces.ListNamespacesResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzNamespacesCreateNamespaceRequest(data []byte) {
	m1 := &namespaces.CreateNamespaceRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &namespaces.CreateNamespaceRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzNamespacesCreateNamespaceResponse(data []byte) {
	m1 := &namespaces.CreateNamespaceResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &namespaces.CreateNamespaceResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzNamespacesUpdateNamespaceRequest(data []byte) {
	m1 := &namespaces.UpdateNamespaceRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &namespaces.UpdateNamespaceRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzNamespacesUpdateNamespaceResponse(data []byte) {
	m1 := &namespaces.UpdateNamespaceResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &namespaces.UpdateNamespaceResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzNamespacesDeleteNamespaceRequest(data []byte) {
	m1 := &namespaces.DeleteNamespaceRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &namespaces.DeleteNamespaceRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzttrpcEventsForwardRequest(data []byte) {
	m1 := &ttrpcEvents.ForwardRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &ttrpcEvents.ForwardRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzttrpcEventsEnvelope(data []byte) {
	m1 := &ttrpcEvents.Envelope{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &ttrpcEvents.Envelope{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzVersionVersionResponse(data []byte) {
	m1 := &version.VersionResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &version.VersionResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzImagesImage(data []byte) {
	m1 := &images.Image{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &images.Image{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzImagesGetImageRequest(data []byte) {
	m1 := &images.GetImageRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &images.GetImageRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzImagesGetImageResponset(data []byte) {
	m1 := &images.GetImageResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &images.GetImageResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzImagesCreateImageRequest(data []byte) {
	m1 := &images.CreateImageRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &images.CreateImageRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzImagesCreateImageResponse(data []byte) {
	m1 := &images.CreateImageResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &images.CreateImageResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzImagesUpdateImageRequest(data []byte) {
	m1 := &images.UpdateImageRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &images.UpdateImageRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzImagesUpdateImageResponse(data []byte) {
	m1 := &images.UpdateImageResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &images.UpdateImageResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzImagesListImagesRequest(data []byte) {
	m1 := &images.ListImagesRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &images.ListImagesRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzImagesListImagesResponse(data []byte) {
	m1 := &images.ListImagesResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &images.ListImagesResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzImagesDeleteImageRequest(data []byte) {
	m1 := &images.DeleteImageRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &images.DeleteImageRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzIntrospectionPlugin(data []byte) {
	m1 := &introspection.Plugin{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &introspection.Plugin{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzIntrospectionPluginsRequest(data []byte) {
	m1 := &introspection.PluginsRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &introspection.PluginsRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzIntrospectionPluginsResponse(data []byte) {
	m1 := &introspection.PluginsResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &introspection.PluginsResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzIntrospectionServerResponse(data []byte) {
	m1 := &introspection.ServerResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &introspection.ServerResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzDiffApplyRequest(data []byte) {
	m1 := &diff.ApplyRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &diff.ApplyRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzDiffApplyResponse(data []byte) {
	m1 := &diff.ApplyResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &diff.ApplyResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzDiffDiffRequest(data []byte) {
	m1 := &diff.DiffRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &diff.DiffRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzDiffDiffResponse(data []byte) {
	m1 := &diff.DiffResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &diff.DiffResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentInfo(data []byte) {
	m1 := &content.Info{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.Info{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentInfoRequest(data []byte) {
	m1 := &content.InfoRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.InfoRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentInfoResponse(data []byte) {
	m1 := &content.InfoResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.InfoResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentUpdateRequest(data []byte) {
	m1 := &content.UpdateRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.UpdateRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentUpdateResponse(data []byte) {
	m1 := &content.UpdateResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.UpdateResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentListContentRequest(data []byte) {
	m1 := &content.ListContentRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.ListContentRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentListContentResponse(data []byte) {
	m1 := &content.ListContentResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.ListContentResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentDeleteContentRequest(data []byte) {
	m1 := &content.DeleteContentRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.DeleteContentRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentReadContentRequest(data []byte) {
	m1 := &content.ReadContentRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.ReadContentRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentReadContentResponse(data []byte) {
	m1 := &content.ReadContentResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.ReadContentResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentStatus(data []byte) {
	m1 := &content.Status{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.Status{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentStatusRequest(data []byte) {
	m1 := &content.StatusRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.StatusRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentStatusResponse(data []byte) {
	m1 := &content.StatusResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.StatusResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentListStatusesRequest(data []byte) {
	m1 := &content.ListStatusesRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.ListStatusesRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentListStatusesResponse(data []byte) {
	m1 := &content.ListStatusesResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.ListStatusesResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentWriteContentRequest(data []byte) {
	m1 := &content.WriteContentRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.WriteContentRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentWriteContentResponse(data []byte) {
	m1 := &content.WriteContentResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.WriteContentResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContentAbortRequest(data []byte) {
	m1 := &content.WriteContentResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &content.WriteContentResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsPrepareSnapshotRequest(data []byte) {
	m1 := &snapshots.PrepareSnapshotRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.PrepareSnapshotRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsPrepareSnapshotResponse(data []byte) {
	m1 := &snapshots.PrepareSnapshotResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.PrepareSnapshotResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsViewSnapshotRequest(data []byte) {
	m1 := &snapshots.ViewSnapshotRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.ViewSnapshotRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsViewSnapshotResponse(data []byte) {
	m1 := &snapshots.ViewSnapshotResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.ViewSnapshotResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsMountsRequest(data []byte) {
	m1 := &snapshots.MountsRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.MountsRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsMountsResponse(data []byte) {
	m1 := &snapshots.MountsResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.MountsResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsRemoveSnapshotRequest(data []byte) {
	m1 := &snapshots.RemoveSnapshotRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.RemoveSnapshotRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsCommitSnapshotRequest(data []byte) {
	m1 := &snapshots.CommitSnapshotRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.CommitSnapshotRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsStatSnapshotRequest(data []byte) {
	m1 := &snapshots.StatSnapshotRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.StatSnapshotRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsInfo(data []byte) {
	m1 := &snapshots.Info{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.Info{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsStatSnapshotResponse(data []byte) {
	m1 := &snapshots.StatSnapshotResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.StatSnapshotResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsUpdateSnapshotRequest(data []byte) {
	m1 := &snapshots.UpdateSnapshotRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.UpdateSnapshotRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsUpdateSnapshotResponse(data []byte) {
	m1 := &snapshots.UpdateSnapshotResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.UpdateSnapshotResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsListSnapshotsRequest(data []byte) {
	m1 := &snapshots.UpdateSnapshotResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.UpdateSnapshotResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsListSnapshotsResponse(data []byte) {
	m1 := &snapshots.ListSnapshotsResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.ListSnapshotsResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsUsageRequest(data []byte) {
	m1 := &snapshots.UsageRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.UsageRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsUsageResponse(data []byte) {
	m1 := &snapshots.UsageResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.UsageResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzSnapshotsCleanupRequest(data []byte) {
	m1 := &snapshots.CleanupRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &snapshots.CleanupRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzLeasesLease(data []byte) {
	m1 := &leases.Lease{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &leases.Lease{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzLeasesCreateRequest(data []byte) {
	m1 := &leases.CreateRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &leases.CreateRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzLeasesCreateResponse(data []byte) {
	m1 := &leases.CreateResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &leases.CreateResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzLeasesDeleteRequest(data []byte) {
	m1 := &leases.DeleteRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &leases.DeleteRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzLeasesListRequest(data []byte) {
	m1 := &leases.ListRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &leases.ListRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzLeasesListResponse(data []byte) {
	m1 := &leases.ListResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &leases.ListResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzLeasesResource(data []byte) {
	m1 := &leases.Resource{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &leases.Resource{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzLeasesAddResourceRequest(data []byte) {
	m1 := &leases.AddResourceRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &leases.AddResourceRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzLeasesDeleteResourceRequest(data []byte) {
	m1 := &leases.DeleteResourceRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &leases.DeleteResourceRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzLeasesListResourcesRequest(data []byte) {
	m1 := &leases.ListResourcesRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &leases.ListResourcesRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzLeasesListResourcesResponse(data []byte) {
	m1 := &leases.ListResourcesResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &leases.ListResourcesResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksCreateTaskRequest(data []byte) {
	m1 := &tasks.CreateTaskRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.CreateTaskRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksCreateTaskResponse(data []byte) {
	m1 := &tasks.CreateTaskResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.CreateTaskResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksStartRequest(data []byte) {
	m1 := &tasks.StartRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.StartRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksStartResponse(data []byte) {
	m1 := &tasks.StartResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.StartResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksDeleteTaskRequest(data []byte) {
	m1 := &tasks.DeleteTaskRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.DeleteTaskRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksDeleteResponse(data []byte) {
	m1 := &tasks.DeleteResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.DeleteResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksDeleteProcessRequest(data []byte) {
	m1 := &tasks.DeleteProcessRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.DeleteProcessRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksGetRequest(data []byte) {
	m1 := &tasks.GetRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.GetRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksGetResponse(data []byte) {
	m1 := &tasks.GetResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.GetResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksListTasksRequest(data []byte) {
	m1 := &tasks.ListTasksRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.ListTasksRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksListTasksResponse(data []byte) {
	m1 := &tasks.ListTasksResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.ListTasksResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksKillRequest(data []byte) {
	m1 := &tasks.KillRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.KillRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksExecProcessRequest(data []byte) {
	m1 := &tasks.ExecProcessRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.ExecProcessRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksExecProcessResponse(data []byte) {
	m1 := &tasks.ExecProcessResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.ExecProcessResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksResizePtyRequest(data []byte) {
	m1 := &tasks.ResizePtyRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.ResizePtyRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksCloseIORequest(data []byte) {
	m1 := &tasks.CloseIORequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.CloseIORequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksPauseTaskRequest(data []byte) {
	m1 := &tasks.PauseTaskRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.PauseTaskRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksResumeTaskRequest(data []byte) {
	m1 := &tasks.ResumeTaskRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.ResumeTaskRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksListPidsRequest(data []byte) {
	m1 := &tasks.ListPidsRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.ListPidsRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksListPidsResponse(data []byte) {
	m1 := &tasks.ListPidsResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.ListPidsResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksCheckpointTaskRequest(data []byte) {
	m1 := &tasks.CheckpointTaskRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.CheckpointTaskRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksCheckpointTaskResponse(data []byte) {
	m1 := &tasks.CheckpointTaskResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.CheckpointTaskResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksUpdateTaskRequest(data []byte) {
	m1 := &tasks.UpdateTaskRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.UpdateTaskRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksMetricsRequest(data []byte) {
	m1 := &tasks.MetricsRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.MetricsRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksMetricsResponse(data []byte) {
	m1 := &tasks.MetricsResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.MetricsResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksWaitRequest(data []byte) {
	m1 := &tasks.WaitRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.WaitRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzTasksWaitResponse(data []byte) {
	m1 := &tasks.WaitResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &tasks.WaitResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContainersContainer(data []byte) {
	m1 := &containers.Container{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &containers.Container{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContainersContainer_Runtime(data []byte) {
	m1 := &containers.Container_Runtime{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &containers.Container_Runtime{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContainersGetContainerRequest(data []byte) {
	m1 := &containers.GetContainerRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &containers.GetContainerRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContainersGetContainerResponse(data []byte) {
	m1 := &containers.GetContainerResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &containers.GetContainerResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContainersListContainersRequest(data []byte) {
	m1 := &containers.ListContainersRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &containers.ListContainersRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContainersListContainersResponse(data []byte) {
	m1 := &containers.ListContainersResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &containers.ListContainersResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContainersCreateContainerRequest(data []byte) {
	m1 := &containers.CreateContainerRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &containers.CreateContainerRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContainersCreateContainerResponse(data []byte) {
	m1 := &containers.CreateContainerResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &containers.CreateContainerResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContainersUpdateContainerRequest(data []byte) {
	m1 := &containers.UpdateContainerRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &containers.UpdateContainerRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContainersUpdateContainerResponse(data []byte) {
	m1 := &containers.UpdateContainerResponse{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &containers.UpdateContainerResponse{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContainersDeleteContainerRequest(data []byte) {
	m1 := &containers.DeleteContainerRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &containers.DeleteContainerRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzContainersListContainerMessage(data []byte) {
	m1 := &containers.ListContainerMessage{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &containers.ListContainerMessage{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsPublishRequest(data []byte) {
	m1 := &servicesEvents.PublishRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &servicesEvents.PublishRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsForwardRequest(data []byte) {
	m1 := &servicesEvents.ForwardRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &servicesEvents.ForwardRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsSubscribeRequest(data []byte) {
	m1 := &servicesEvents.SubscribeRequest{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &servicesEvents.SubscribeRequest{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}

func fuzzEventsEnvelope(data []byte) {
	m1 := &servicesEvents.Envelope{}
	data2 := data
	err := m1.Unmarshal(data)
	if err != nil {
		return
	}
	correctData1, err := m1.Marshal()
	if err != nil {
		panic(err)
	}
	m2 := &servicesEvents.Envelope{}
	err = m2.Unmarshal(data2)
	if err != nil {
		panic(err)
	}
	correctData2, err := m2.Marshal()
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(m1, m2) {
		fmt.Printf("%+v\n", m1)
		fmt.Printf("%+v\n", m2)
		panic("done")
	}
	checkData(correctData1, correctData2)
}
