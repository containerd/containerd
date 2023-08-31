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

package blockio

import (
	"fmt"
	"sync"

	"github.com/intel/goresctrl/pkg/blockio"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"

	"github.com/containerd/containerd/log"
)

var (
	enabled          bool
	enabledMu        sync.RWMutex
	reconfigFilePath string
)

// IsEnabled checks whether blockio is enabled.
func IsEnabled() bool {
	enabledMu.RLock()
	defer enabledMu.RUnlock()

	return enabled
}

// SetConfig updates blockio config with a given config path.
func SetConfig(configFilePath string, alwaysReconfigure bool) error {
	enabledMu.Lock()
	defer enabledMu.Unlock()

	enabled = false
	if configFilePath == "" {
		log.L.Debug("No blockio config file specified, blockio not configured")
		return nil
	}

	if err := blockio.SetConfigFromFile(configFilePath, true); err != nil {
		return fmt.Errorf("blockio not enabled: %w", err)
	}
	enabled = true
	if alwaysReconfigure {
		reconfigFilePath = configFilePath
	} else {
		reconfigFilePath = ""
	}
	return nil
}

// ClassNameToLinuxOCI converts blockio class name into the LinuxBlockIO
// structure in the OCI runtime spec.
func ClassNameToLinuxOCI(className string) (*runtimespec.LinuxBlockIO, error) {
	enabledMu.Lock()
	defer enabledMu.Unlock()

	if !enabled {
		return nil, nil
	}
	if err := reconfigure(); err != nil {
		return nil, err
	}
	return blockio.OciLinuxBlockIO(className)
}

// ContainerClassFromAnnotations examines container and pod annotations of a
// container and returns its blockio class.
func ContainerClassFromAnnotations(containerName string, containerAnnotations, podAnnotations map[string]string) (string, error) {
	return blockio.ContainerClassFromAnnotations(containerName, containerAnnotations, podAnnotations)
}

// reconfigure re-reads and applies blockio configuration. This can
// result in new blockio parameters even if the configuration file
// contents have not changed: applying triggers rescanning block
// devices in the system and rematching devices to wildcards in the
// configuration.
func reconfigure() error {
	if reconfigFilePath == "" {
		return nil
	}
	err := blockio.SetConfigFromFile(reconfigFilePath, true)
	if err != nil {
		log.L.Error("blockio reconfiguration error: %w", err)
	}
	return err
}
