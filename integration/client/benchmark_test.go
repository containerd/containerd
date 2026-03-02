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
	"strings"
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
	// Simulate injecting a large number of Kubernetes service environment variables.
	// see  https://github.com/kubernetes/kubernetes/issues/121787
	// so that it will take up more memory when fetching the container list.
	envs = append(envs, generateK8sServiceEnvVarsList()...)
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

const (
	fixedIP       = "172.20.200.100"
	fixedPort     = 8080
	servicePrefix = "long-k8s-service-for-test-env-var-"
	portTemplate  = "tcp://%s:%d"
)

func generateK8sServiceEnvVarsList() []string {
	var envVarsList []string
	for i := 1; i <= 4000; i++ {
		serviceName := fmt.Sprintf("%s%03d-extra-long-suffix-to-increase-length-abcdefghijklmnop",
			servicePrefix, i)
		if len(serviceName) > 50 {
			serviceName = serviceName[:50]
		}
		serviceName = strings.TrimSuffix(serviceName, "-")
		envPrefix := strings.ToUpper(strings.ReplaceAll(serviceName, "-", "_"))
		portStr := fmt.Sprintf("%d", fixedPort)
		fullPort := fmt.Sprintf(portTemplate, fixedIP, fixedPort)
		envVarsList = append(envVarsList,
			fmt.Sprintf("%s_SERVICE_HOST=%s", envPrefix, fixedIP),
			fmt.Sprintf("%s_SERVICE_PORT=%s", envPrefix, portStr),
			fmt.Sprintf("%s_PORT=%s", envPrefix, fullPort),
			fmt.Sprintf("%s_PORT_%d_TCP=%s", envPrefix, fixedPort, fullPort),
			fmt.Sprintf("%s_PORT_%d_TCP_PROTO=tcp", envPrefix, fixedPort),
			fmt.Sprintf("%s_PORT_%d_TCP_PORT=%s", envPrefix, fixedPort, portStr),
			fmt.Sprintf("%s_PORT_%d_TCP_ADDR=%s", envPrefix, fixedPort, fixedIP),
		)
	}
	return envVarsList
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
