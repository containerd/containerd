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

package plugin

import (
	"testing"

	"github.com/containerd/containerd/v2/services"
)

func mockPluginFilter(*Registration) bool {
	return false
}

// TestContainerdPlugin tests the logic of Graph, use the containerd's plugin
func TestContainerdPlugin(t *testing.T) {
	// Plugin types commonly used by containerd
	const (
		InternalPlugin         Type = "io.containerd.internal.v1"
		RuntimePlugin          Type = "io.containerd.runtime.v1"
		RuntimePluginV2        Type = "io.containerd.runtime.v2"
		ServicePlugin          Type = "io.containerd.service.v1"
		GRPCPlugin             Type = "io.containerd.grpc.v1"
		SnapshotPlugin         Type = "io.containerd.snapshotter.v1"
		TaskMonitorPlugin      Type = "io.containerd.monitor.v1"
		DiffPlugin             Type = "io.containerd.differ.v1"
		MetadataPlugin         Type = "io.containerd.metadata.v1"
		ContentPlugin          Type = "io.containerd.content.v1"
		GCPlugin               Type = "io.containerd.gc.v1"
		LeasePlugin            Type = "io.containerd.lease.v1"
		TracingProcessorPlugin Type = "io.containerd.tracing.processor.v1"
	)

	var register Registry
	register = register.Register(&Registration{
		Type: TaskMonitorPlugin,
		ID:   "cgroups",
	}).Register(&Registration{
		Type: ServicePlugin,
		ID:   services.TasksService,
		Requires: []Type{
			RuntimePlugin,
			RuntimePluginV2,
			MetadataPlugin,
			TaskMonitorPlugin,
		},
	}).Register(&Registration{
		Type: ServicePlugin,
		ID:   services.IntrospectionService,
	}).Register(&Registration{
		Type: ServicePlugin,
		ID:   services.NamespacesService,
		Requires: []Type{
			MetadataPlugin,
		},
	}).Register(&Registration{
		Type: GRPCPlugin,
		ID:   "namespaces",
		Requires: []Type{
			ServicePlugin,
		},
	}).Register(&Registration{
		Type: GRPCPlugin,
		ID:   "content",
		Requires: []Type{
			ServicePlugin,
		},
	}).Register(&Registration{
		Type: GRPCPlugin,
		ID:   "containers",
		Requires: []Type{
			ServicePlugin,
		},
	}).Register(&Registration{
		Type: ServicePlugin,
		ID:   services.ContainersService,
		Requires: []Type{
			MetadataPlugin,
		},
	}).Register(&Registration{
		Type: GRPCPlugin,
		ID:   "events",
	}).Register(&Registration{
		Type: GRPCPlugin,
		ID:   "leases",
		Requires: []Type{
			LeasePlugin,
		},
	}).Register(&Registration{
		Type: LeasePlugin,
		ID:   "manager",
		Requires: []Type{
			MetadataPlugin,
		},
	}).Register(&Registration{
		Type: GRPCPlugin,
		ID:   "diff",
		Requires: []Type{
			ServicePlugin,
		},
	}).Register(&Registration{
		Type: ServicePlugin,
		ID:   services.DiffService,
		Requires: []Type{
			DiffPlugin,
		},
	}).Register(&Registration{
		Type: ServicePlugin,
		ID:   services.SnapshotsService,
		Requires: []Type{
			MetadataPlugin,
		},
	}).Register(&Registration{
		Type: GRPCPlugin,
		ID:   "snapshots",
		Requires: []Type{
			ServicePlugin,
		},
	}).Register(&Registration{
		Type: GRPCPlugin,
		ID:   "version",
	}).Register(&Registration{
		Type: GRPCPlugin,
		ID:   "images",
		Requires: []Type{
			ServicePlugin,
		},
	}).Register(&Registration{
		Type: GCPlugin,
		ID:   "scheduler",
		Requires: []Type{
			MetadataPlugin,
		},
	}).Register(&Registration{
		Type: RuntimePluginV2,
		ID:   "task",
		Requires: []Type{
			MetadataPlugin,
		},
	}).Register(&Registration{
		Type: GRPCPlugin,
		ID:   "tasks",
		Requires: []Type{
			ServicePlugin,
		},
	}).Register(&Registration{
		Type:     GRPCPlugin,
		ID:       "introspection",
		Requires: []Type{"*"},
	}).Register(&Registration{
		Type: ServicePlugin,
		ID:   services.ContentService,
		Requires: []Type{
			MetadataPlugin,
		},
	}).Register(&Registration{
		Type: GRPCPlugin,
		ID:   "healthcheck",
	}).Register(&Registration{
		Type: InternalPlugin,
		ID:   "opt",
	}).Register(&Registration{
		Type: GRPCPlugin,
		ID:   "cri",
		Requires: []Type{
			ServicePlugin,
		},
	}).Register(&Registration{
		Type: RuntimePlugin,
		ID:   "linux",
		Requires: []Type{
			MetadataPlugin,
		},
	}).Register(&Registration{
		Type: InternalPlugin,
		Requires: []Type{
			ServicePlugin,
		},
		ID: "restart",
	}).Register(&Registration{
		Type: DiffPlugin,
		ID:   "walking",
		Requires: []Type{
			MetadataPlugin,
		},
	}).Register(&Registration{
		Type: SnapshotPlugin,
		ID:   "native",
	}).Register(&Registration{
		Type: SnapshotPlugin,
		ID:   "overlayfs",
	}).Register(&Registration{
		Type: ContentPlugin,
		ID:   "content",
	}).Register(&Registration{
		Type: MetadataPlugin,
		ID:   "bolt",
		Requires: []Type{
			ContentPlugin,
			SnapshotPlugin,
		},
	}).Register(&Registration{
		Type: TracingProcessorPlugin,
		ID:   "otlp",
	}).Register(&Registration{
		Type: InternalPlugin,
		ID:   "tracing",
		Requires: []Type{
			TracingProcessorPlugin,
		},
	})

	ordered := register.Graph(mockPluginFilter)
	expectedURI := []string{
		"io.containerd.monitor.v1.cgroups",
		"io.containerd.content.v1.content",
		"io.containerd.snapshotter.v1.native",
		"io.containerd.snapshotter.v1.overlayfs",
		"io.containerd.metadata.v1.bolt",
		"io.containerd.runtime.v1.linux",
		"io.containerd.runtime.v2.task",
		"io.containerd.service.v1.tasks-service",
		"io.containerd.service.v1.introspection-service",
		"io.containerd.service.v1.namespaces-service",
		"io.containerd.service.v1.containers-service",
		"io.containerd.differ.v1.walking",
		"io.containerd.service.v1.diff-service",
		"io.containerd.service.v1.snapshots-service",
		"io.containerd.service.v1.content-service",
		"io.containerd.grpc.v1.namespaces",
		"io.containerd.grpc.v1.content",
		"io.containerd.grpc.v1.containers",
		"io.containerd.grpc.v1.events",
		"io.containerd.lease.v1.manager",
		"io.containerd.grpc.v1.leases",
		"io.containerd.grpc.v1.diff",
		"io.containerd.grpc.v1.snapshots",
		"io.containerd.grpc.v1.version",
		"io.containerd.grpc.v1.images",
		"io.containerd.gc.v1.scheduler",
		"io.containerd.grpc.v1.tasks",
		"io.containerd.grpc.v1.healthcheck",
		"io.containerd.internal.v1.opt",
		"io.containerd.grpc.v1.cri",
		"io.containerd.internal.v1.restart",
		"io.containerd.tracing.processor.v1.otlp",
		"io.containerd.internal.v1.tracing",
		"io.containerd.grpc.v1.introspection",
	}
	cmpOrdered(t, ordered, expectedURI)
}

func cmpOrdered(t *testing.T, ordered []Registration, expectedURI []string) {
	if len(ordered) != len(expectedURI) {
		t.Fatalf("ordered compare failed, %d != %d", len(ordered), len(expectedURI))
	}
	for i := range ordered {
		if ordered[i].URI() != expectedURI[i] {
			t.Fatalf("graph failed, expected: %s, but return: %s", expectedURI[i], ordered[i].URI())
		}
	}
}

// TestPluginGraph tests the logic of Graph
func TestPluginGraph(t *testing.T) {
	for _, testcase := range []struct {
		input       []*Registration
		expectedURI []string
		filter      DisableFilter
	}{
		// test requires *
		{
			input: []*Registration{
				{
					Type: "grpc",
					ID:   "introspection",
					Requires: []Type{
						"*",
					},
				},
				{
					Type: "service",
					ID:   "container",
				},
			},
			expectedURI: []string{
				"service.container",
				"grpc.introspection",
			},
		},
		// test requires
		{
			input: []*Registration{
				{
					Type: "service",
					ID:   "container",
					Requires: []Type{
						"metadata",
					},
				},
				{
					Type: "metadata",
					ID:   "bolt",
				},
			},
			expectedURI: []string{
				"metadata.bolt",
				"service.container",
			},
		},
		{
			input: []*Registration{
				{
					Type: "metadata",
					ID:   "bolt",
					Requires: []Type{
						"content",
						"snapshotter",
					},
				},
				{
					Type: "snapshotter",
					ID:   "overlayfs",
				},
				{
					Type: "content",
					ID:   "content",
				},
			},
			expectedURI: []string{
				"content.content",
				"snapshotter.overlayfs",
				"metadata.bolt",
			},
		},
		// test disable
		{
			input: []*Registration{
				{
					Type: "content",
					ID:   "content",
				},
				{
					Type: "disable",
					ID:   "disable",
				},
			},
			expectedURI: []string{
				"content.content",
			},
			filter: func(r *Registration) bool {
				return r.Type == "disable"
			},
		},
	} {
		var register Registry
		for _, in := range testcase.input {
			register = register.Register(in)
		}
		var filter DisableFilter = mockPluginFilter
		if testcase.filter != nil {
			filter = testcase.filter
		}
		ordered := register.Graph(filter)
		cmpOrdered(t, ordered, testcase.expectedURI)
	}
}
