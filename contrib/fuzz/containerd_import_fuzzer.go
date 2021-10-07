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
	"bytes"
	"context"
	"sync"
	"time"

	fuzz "github.com/AdaLogics/go-fuzz-headers"

	"github.com/containerd/containerd"
	_ "github.com/containerd/containerd/cmd/containerd"
	"github.com/containerd/containerd/cmd/containerd/command"
	"github.com/containerd/containerd/namespaces"
)

const (
	defaultRoot    = "/var/lib/containerd"
	defaultState   = "/tmp/containerd"
	defaultAddress = "/tmp/containerd/containerd.sock"
)

var (
	initDaemon sync.Once
)

func startDaemon() {
	args := []string{"--log-level", "debug"}
	go func() {
		// This is similar to invoking the
		// containerd binary.
		// See contrib/fuzz/oss_fuzz_build.sh
		// for more info.
		command.StartDaemonForFuzzing(args)
	}()
	time.Sleep(time.Second * 4)
}

func fuzzContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = namespaces.WithNamespace(ctx, "fuzzing-namespace")
	return ctx, cancel
}

func FuzzContainerdImport(data []byte) int {
	initDaemon.Do(startDaemon)

	client, err := containerd.New(defaultAddress)
	if err != nil {
		return 0
	}
	defer client.Close()

	f := fuzz.NewConsumer(data)

	noOfImports, err := f.GetInt()
	if err != nil {
		return 0
	}
	maxImports := 20
	ctx, cancel := fuzzContext()
	defer cancel()
	for i := 0; i < noOfImports%maxImports; i++ {
		tarBytes, err := f.GetBytes()
		if err != nil {
			return 0
		}
		_, _ = client.Import(ctx, bytes.NewReader(tarBytes))
	}
	return 1
}
