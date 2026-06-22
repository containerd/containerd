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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"

	"github.com/containerd/containerd/v2/integration/failpoint"
	"github.com/containerd/containerd/v2/pkg/fifosync"
)

// delayExec delays an "exec" command until a trigger is received from the calling test program.  This can be used to
// test races around container lifecycle and exec processes.
func delayExec(ctx context.Context, method invoker) error {
	isExec := strings.Contains(strings.Join(os.Args, ","), ",exec,")
	if !isExec {
		if err := method(ctx); err != nil {
			return err
		}
		return nil
	}
	logrus.Debug("EXEC!")

	if err := delay(); err != nil {
		return err
	}
	if err := method(ctx); err != nil {
		return err
	}
	return nil
}

func delay() error {
	ready, delay, err := fifoFromProcessEnv()
	if err != nil {
		return err
	}
	if err := ready.Trigger(); err != nil {
		return err
	}
	return delay.Wait()
}

// fifoFromProcessEnv finds a fifo specified in the environment variables of an exec process
func fifoFromProcessEnv() (fifosync.Trigger, fifosync.Waiter, error) {
	env, err := processEnvironment()
	if err != nil {
		return nil, nil, err
	}

	readyName, ok := env[failpoint.DelayExecReadyEnv]
	if !ok {
		return nil, nil, fmt.Errorf("fifo: failed to find %q env var in %v", failpoint.DelayExecReadyEnv, env)
	}
	delayName, ok := env[failpoint.DelayExecDelayEnv]
	if !ok {
		return nil, nil, fmt.Errorf("fifo: failed to find %q env var in %v", failpoint.DelayExecDelayEnv, env)
	}
	logrus.WithField("ready", readyName).WithField("delay", delayName).Debug("Found FIFOs!")
	readyFIFO, err := fifosync.NewTrigger(readyName, 0600)
	if err != nil {
		return nil, nil, err
	}
	delayFIFO, err := fifosync.NewWaiter(delayName, 0600)
	if err != nil {
		return nil, nil, err
	}
	return readyFIFO, delayFIFO, nil
}

func processEnvironment() (map[string]string, error) {
	idx := 2
	for ; idx < len(os.Args); idx++ {
		if os.Args[idx] == "--process" {
			break
		}
	}

	if idx >= len(os.Args)-1 || os.Args[idx] != "--process" {
		return nil, errors.New("env: option --process required")
	}

	specFile := os.Args[idx+1]
	f, err := os.OpenFile(specFile, os.O_RDONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("env: failed to open %s: %w", specFile, err)
	}

	b, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("env: failed to read spec from %q", specFile)
	}
	var spec specs.Process
	if err := json.Unmarshal(b, &spec); err != nil {
		return nil, fmt.Errorf("env: failed to unmarshal spec from %q: %w", specFile, err)
	}

	// XXX: env vars can be specified multiple times, but we only keep one
	env := make(map[string]string)
	for _, e := range spec.Env {
		k, v, _ := strings.Cut(e, "=")
		env[k] = v
	}

	return env, nil
}
