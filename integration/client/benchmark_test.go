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

package client

import (
	"context"
	"fmt"
	"iter"
	"testing"

	. "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/oci"
)

func BenchmarkContainerCreate(b *testing.B) {
	client, err := newClient(b, address)
	if err != nil {
		b.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext(b)
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		b.Error(err)
		return
	}
	spec, err := oci.GenerateSpec(ctx, client, &containers.Container{ID: b.Name()}, oci.WithImageConfig(image), withTrue())
	if err != nil {
		b.Error(err)
		return
	}
	var containers []Container
	defer func() {
		for _, c := range containers {
			if err := c.Delete(ctx, WithSnapshotCleanup); err != nil {
				b.Error(err)
			}
		}
	}()

	// reset the timer before creating containers
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("%s-%d", b.Name(), i)
		container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithSpec(spec))
		if err != nil {
			b.Error(err)
			return
		}
		containers = append(containers, container)
	}
	b.StopTimer()
}

func BenchmarkContainerGet(b *testing.B) {
	client, err := newClient(b, address)
	if err != nil {
		b.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext(b)
	defer cancel()
	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		b.Error(err)
		return
	}
	var containers []Container
	defer func() {
		for _, c := range containers {
			if err := c.Delete(ctx, WithSnapshotCleanup); err != nil {
				b.Error(err)
			}
		}
	}()

	var envs []string
	// Randomly generate a large list of environment variables to create a container,
	// so that it will take up more memory when fetching the container list.
	for key := range 4000 {
		envs = append(envs, fmt.Sprintf("SFTPV6_CFBFC1C7_C8A8_00C_94AC_4EFD3CE927D2_PORT_0001_TCP_PORT%d=value_%d", key, key))
	}
	for i := 0; i < 500; i++ {
		id := fmt.Sprintf("%s-%d", b.Name(), i)
		container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), oci.WithEnv(envs), withExitStatus(7)))
		if err != nil {
			b.Error(err)
			return
		}
		b.Logf("create container %s", container.ID())
		containers = append(containers, container)
	}
	b.Run("use client.ContainersIter", benchmarkGetContainers(ctx, true, client))
	b.Run("use client.Containers", benchmarkGetContainers(ctx, false, client))
}

func benchmarkGetContainers(ctx context.Context, useIter bool, client *Client) func(b *testing.B) {
	return func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var filter []string
			var containers iter.Seq2[Container, error]
			var err error
			if useIter {
				containers, err = client.ContainersIter(ctx, filter...)
				if err != nil {
					b.Fatal(err)
				}
			} else {
				ctrs, err := client.Containers(ctx, filter...)
				if err != nil {
					b.Fatal(err)
				}
				containers = sliceIter(ctrs)
			}
			for con, err := range containers {
				if err != nil {
					b.Fatal(err)
				}
				_, err := con.Info(ctx, WithoutRefreshedMetadata)
				if err != nil {
					b.Fatal(err)
				}
			}
		}
	}
}

func sliceIter(ctrs []Container) iter.Seq2[Container, error] {
	return func(yield func(Container, error) bool) {
		for _, c := range ctrs {
			if !yield(c, nil) {
				return
			}
		}
	}
}

func BenchmarkContainerStart(b *testing.B) {
	client, err := newClient(b, address)
	if err != nil {
		b.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext(b)
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		b.Error(err)
		return
	}
	spec, err := oci.GenerateSpec(ctx, client, &containers.Container{ID: b.Name()}, oci.WithImageConfig(image), withTrue())
	if err != nil {
		b.Error(err)
		return
	}
	var containers []Container
	defer func() {
		for _, c := range containers {
			if err := c.Delete(ctx, WithSnapshotCleanup); err != nil {
				b.Error(err)
			}
		}
	}()

	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("%s-%d", b.Name(), i)
		container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithSpec(spec))
		if err != nil {
			b.Error(err)
			return
		}
		containers = append(containers, container)

	}
	// reset the timer before starting tasks
	b.ResetTimer()
	for _, c := range containers {
		task, err := c.NewTask(ctx, empty())
		if err != nil {
			b.Error(err)
			return
		}
		defer task.Delete(ctx)
		if err := task.Start(ctx); err != nil {
			b.Error(err)
			return
		}
	}
	b.StopTimer()
}
