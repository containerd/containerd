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

package container

import (
	"strings"
	"testing"
	"time"

	cio "github.com/containerd/containerd/v2/pkg/cri/io"
	"github.com/containerd/containerd/v2/pkg/cri/store/label"
	"github.com/containerd/containerd/v2/pkg/cri/store/stats"
	"github.com/containerd/containerd/v2/pkg/errdefs"

	"github.com/opencontainers/selinux/go-selinux"
	assertlib "github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestContainerStore(t *testing.T) {
	metadatas := map[string]Metadata{
		"1": {
			ID:        "1",
			Name:      "Container-1",
			SandboxID: "Sandbox-1",
			Config: &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{
					Name:    "TestPod-1",
					Attempt: 1,
				},
			},
			ImageRef:     "TestImage-1",
			StopSignal:   "SIGTERM",
			LogPath:      "/test/log/path/1",
			ProcessLabel: "junk:junk:junk:c1,c2",
		},
		"2abcd": {
			ID:        "2abcd",
			Name:      "Container-2abcd",
			SandboxID: "Sandbox-2abcd",
			Config: &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{
					Name:    "TestPod-2abcd",
					Attempt: 2,
				},
			},
			StopSignal:   "SIGTERM",
			ImageRef:     "TestImage-2",
			LogPath:      "/test/log/path/2",
			ProcessLabel: "junk:junk:junk:c1,c2",
		},
		"4a333": {
			ID:        "4a333",
			Name:      "Container-4a333",
			SandboxID: "Sandbox-4a333",
			Config: &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{
					Name:    "TestPod-4a333",
					Attempt: 3,
				},
			},
			StopSignal:   "SIGTERM",
			ImageRef:     "TestImage-3",
			LogPath:      "/test/log/path/3",
			ProcessLabel: "junk:junk:junk:c1,c3",
		},
		"4abcd": {
			ID:        "4abcd",
			Name:      "Container-4abcd",
			SandboxID: "Sandbox-4abcd",
			Config: &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{
					Name:    "TestPod-4abcd",
					Attempt: 1,
				},
			},
			StopSignal:   "SIGTERM",
			ImageRef:     "TestImage-4abcd",
			ProcessLabel: "junk:junk:junk:c1,c4",
		},
	}
	statuses := map[string]Status{
		"1": {
			Pid:        1,
			CreatedAt:  time.Now().UnixNano(),
			StartedAt:  time.Now().UnixNano(),
			FinishedAt: time.Now().UnixNano(),
			ExitCode:   1,
			Reason:     "TestReason-1",
			Message:    "TestMessage-1",
		},
		"2abcd": {
			Pid:        2,
			CreatedAt:  time.Now().UnixNano(),
			StartedAt:  time.Now().UnixNano(),
			FinishedAt: time.Now().UnixNano(),
			ExitCode:   2,
			Reason:     "TestReason-2abcd",
			Message:    "TestMessage-2abcd",
		},
		"4a333": {
			Pid:        3,
			CreatedAt:  time.Now().UnixNano(),
			StartedAt:  time.Now().UnixNano(),
			FinishedAt: time.Now().UnixNano(),
			ExitCode:   3,
			Reason:     "TestReason-4a333",
			Message:    "TestMessage-4a333",
			Starting:   true,
		},
		"4abcd": {
			Pid:        4,
			CreatedAt:  time.Now().UnixNano(),
			StartedAt:  time.Now().UnixNano(),
			FinishedAt: time.Now().UnixNano(),
			ExitCode:   4,
			Reason:     "TestReason-4abcd",
			Message:    "TestMessage-4abcd",
			Removing:   true,
		},
	}

	stats := map[string]*stats.ContainerStats{
		"1": {
			Timestamp:            time.Now(),
			UsageCoreNanoSeconds: 1,
		},
		"2abcd": {
			Timestamp:            time.Now(),
			UsageCoreNanoSeconds: 2,
		},
		"4a333": {
			Timestamp:            time.Now(),
			UsageCoreNanoSeconds: 3,
		},
		"4abcd": {
			Timestamp:            time.Now(),
			UsageCoreNanoSeconds: 4,
		},
	}
	assert := assertlib.New(t)
	containers := map[string]Container{}
	for id := range metadatas {
		container, err := NewContainer(
			metadatas[id],
			WithFakeStatus(statuses[id]),
		)
		assert.NoError(err)
		containers[id] = container
	}

	s := NewStore(label.NewStore())
	reserved := map[string]bool{}
	s.labels.Reserver = func(label string) {
		reserved[strings.SplitN(label, ":", 4)[3]] = true
	}
	s.labels.Releaser = func(label string) {
		reserved[strings.SplitN(label, ":", 4)[3]] = false
	}

	t.Logf("should be able to add container")
	for _, c := range containers {
		assert.NoError(s.Add(c))
	}

	t.Logf("should be able to get container")
	genTruncIndex := func(normalName string) string { return normalName[:(len(normalName)+1)/2] }
	for id, c := range containers {
		got, err := s.Get(genTruncIndex(id))
		assert.NoError(err)
		assert.Equal(c, got)
		assert.Nil(c.Stats)
	}

	t.Logf("should be able to list containers")
	cs := s.List()
	assert.Len(cs, len(containers))

	t.Logf("should be able to update stats on container")
	for id := range containers {
		err := s.UpdateContainerStats(id, stats[id])
		assert.NoError(err)
	}

	// Validate stats were updated
	cs = s.List()
	assert.Len(cs, len(containers))
	for _, c := range cs {
		assert.Equal(stats[c.ID], c.Stats)
	}

	if selinux.GetEnabled() {
		t.Logf("should have reserved labels (requires -tag selinux)")
		assert.Equal(map[string]bool{
			"c1,c2": true,
			"c1,c3": true,
			"c1,c4": true,
		}, reserved)
	}

	cntrNum := len(containers)
	for testID, v := range containers {
		truncID := genTruncIndex(testID)

		t.Logf("add should return already exists error for duplicated container")
		assert.Equal(errdefs.ErrAlreadyExists, s.Add(v))

		t.Logf("should be able to delete container")
		s.Delete(truncID)
		cntrNum--
		cs = s.List()
		assert.Len(cs, cntrNum)

		t.Logf("get should return not exist error after deletion")
		c, err := s.Get(truncID)
		assert.Equal(Container{}, c)
		assert.Equal(errdefs.ErrNotFound, err)
	}

	if selinux.GetEnabled() {
		t.Logf("should have released all labels (requires -tag selinux)")
		assert.Equal(map[string]bool{
			"c1,c2": false,
			"c1,c3": false,
			"c1,c4": false,
		}, reserved)
	}
}

func TestWithContainerIO(t *testing.T) {
	meta := Metadata{
		ID:        "1",
		Name:      "Container-1",
		SandboxID: "Sandbox-1",
		Config: &runtime.ContainerConfig{
			Metadata: &runtime.ContainerMetadata{
				Name:    "TestPod-1",
				Attempt: 1,
			},
		},
		ImageRef:   "TestImage-1",
		StopSignal: "SIGTERM",
		LogPath:    "/test/log/path",
	}
	status := Status{
		Pid:        1,
		CreatedAt:  time.Now().UnixNano(),
		StartedAt:  time.Now().UnixNano(),
		FinishedAt: time.Now().UnixNano(),
		ExitCode:   1,
		Reason:     "TestReason-1",
		Message:    "TestMessage-1",
	}
	assert := assertlib.New(t)

	c, err := NewContainer(meta, WithFakeStatus(status))
	assert.NoError(err)
	assert.Nil(c.IO)

	c, err = NewContainer(
		meta,
		WithFakeStatus(status),
		WithContainerIO(&cio.ContainerIO{}),
	)
	assert.NoError(err)
	assert.NotNil(c.IO)
}
