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

package config

import (
	"context"
	"errors"
	"fmt"

	kernel "github.com/containerd/containerd/v2/pkg/kernelversion"
)

var kernelGreaterEqualThan = kernel.GreaterEqualThan

func ValidateEnableUnprivileged(ctx context.Context, c *RuntimeConfig) error {
	if c.EnableUnprivilegedICMP || c.EnableUnprivilegedPorts {
		fourDotEleven := kernel.KernelVersion{Kernel: 4, Major: 11}
		ok, err := kernelGreaterEqualThan(fourDotEleven)
		if err != nil {
			return fmt.Errorf("check current system kernel version error: %w", err)
		}
		if !ok {
			return errors.New("unprivileged_icmp and unprivileged_port require kernel version greater than or equal to 4.11")
		}
	}
	return nil
}

var kernelSupportsRro bool

func init() {
	var err error
	kernelSupportsRro, err = kernelGreaterEqualThan(kernel.KernelVersion{Kernel: 5, Major: 12})
	if err != nil {
		panic(fmt.Errorf("check current system kernel version error: %w", err))
	}
}
