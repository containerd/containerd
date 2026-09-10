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

package config_test

import (
	"fmt"
	"log"

	runtimeconfig "github.com/containerd/containerd/v2/core/runtime/config"
)

func ExampleLoad() {
	runtimes, err := runtimeconfig.Load[runtimeconfig.Runtime]("/etc/containerd/runtimes")
	if err != nil {
		log.Fatal(err)
	}

	runtime := runtimes["kata"]
	fmt.Printf("%s uses %s\n", runtime.Type, runtime.Snapshotter)
}

func ExampleLoad_consumerSpecificFields() {
	type criRuntime struct {
		Type                 string   `toml:"runtime_type"`
		PodAnnotations       []string `toml:"pod_annotations"`
		ContainerAnnotations []string `toml:"container_annotations"`
	}

	runtimes, err := runtimeconfig.Load[criRuntime]("/etc/containerd/runtimes")
	if err != nil {
		log.Fatal(err)
	}

	runtime := runtimes["kata"]
	fmt.Printf("%s accepts pod annotations %v\n", runtime.Type, runtime.PodAnnotations)
}
