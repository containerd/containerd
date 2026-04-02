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
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/sirupsen/logrus"
)

const (
	failpointProfileKey = "oci.runc.failpoint.profile"
)

type invoker func(context.Context) error

type invokerInterceptor func(context.Context, invoker) error

var (
	failpointProfiles = map[string]invokerInterceptor{
		"issue9103":   issue9103KillInitAfterCreate,
		"delayExec":   delayExec,
		"delayUpdate": delayUpdate,
		"doNothing":   doNothing,
	}
)

// setupLog setups messages into log file.
func setupLog() {
	// containerd/go-runc always add --log option
	idx := 2
	for ; idx < len(os.Args); idx++ {
		if os.Args[idx] == "--log" {
			break
		}
	}

	if idx >= len(os.Args)-1 || os.Args[idx] != "--log" {
		panic("option --log required")
	}

	logFile := os.Args[idx+1]
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_SYNC, 0o644)
	if err != nil {
		panic(fmt.Errorf("failed to open %s: %w", logFile, err))
	}

	logrus.SetOutput(f)
	logrus.SetFormatter(new(logrus.JSONFormatter))
}

func main() {
	setupLog()
	var fpProfile invokerInterceptor
	fpProfileFromOci, err := failpointProfileFromOCIAnnotation()
	if err != nil {
		logrus.WithError(err).Warnf("failed to get failpoint profile")
	}
	fpProfileFromfile, err := failpointFromFile()
	if err != nil {
		logrus.WithError(err).Warnf("failed to get failpoint profile")
	}
	if fpProfileFromOci != nil && fpProfileFromfile != nil {
		logrus.WithError(err).Fatal("fpProfileFromOci and  fpProfileFromfile is not nill")
	}

	if fpProfileFromOci != nil {
		fpProfile = fpProfileFromOci
	}

	if fpProfileFromfile != nil {
		fpProfile = fpProfileFromfile
	}
	if fpProfile == nil {
		fpProfile = doNothing
	}
	ctx := context.Background()
	if err := fpProfile(ctx, defaultRuncInvoker); err != nil {
		logrus.WithError(err).Fatal("failed to exec failpoint profile")
	}
}

// defaultRuncInvoker is to call the runc command with same arguments.
func defaultRuncInvoker(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "runc", os.Args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return cmd.Run()
}

func failpointFromFile() (invokerInterceptor, error) {
	data, err := os.ReadFile("/tmp/failpoint_profile.json")
	if err != nil {
		return nil, err
	}
	var result map[string]string
	err = json.Unmarshal(data, &result)
	if err != nil {
		return nil, err
	}
	if failpoint, ok := result["failpoint"]; ok {
		fp, ok := failpointProfiles[failpoint]
		if !ok {
			return nil, fmt.Errorf("no such failpoint profile %s", failpoint)
		}
		return fp, nil
	}
	return nil, fmt.Errorf("can't get failpoint from file /tmp/failpoint_profile.json")
}

// failpointProfileFromOCIAnnotation gets the profile from OCI annotations.
func failpointProfileFromOCIAnnotation() (invokerInterceptor, error) {
	spec, err := oci.ReadSpec(oci.ConfigFilename)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", oci.ConfigFilename, err)
	}

	profileName, ok := spec.Annotations[failpointProfileKey]
	if !ok {
		return nil, fmt.Errorf("failpoint profile is required")
	}
	fp, ok := failpointProfiles[profileName]
	if !ok {
		return nil, fmt.Errorf("no such failpoint profile %s", profileName)
	}
	return fp, nil
}
