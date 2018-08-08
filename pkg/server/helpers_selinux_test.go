// +build selinux

/*
Copyright 2017 The Kubernetes Authors.

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

package server

import (
	"testing"

	"github.com/opencontainers/selinux/go-selinux"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"
)

func TestInitSelinuxOpts(t *testing.T) {
	if !selinux.GetEnabled() {
		t.Skip("selinux is not enabled")
	}

	for desc, test := range map[string]struct {
		selinuxOpt   *runtime.SELinuxOption
		processLabel string
		mountLabels  []string
		expectErr    bool
	}{
		"Should return empty strings for processLabel and mountLabel when selinuxOpt is nil": {
			selinuxOpt:   nil,
			processLabel: "",
			mountLabels:  []string{"", ""},
		},
		"Should return empty strings for processLabel and mountLabel when selinuxOpt has been initialized partially": {
			selinuxOpt: &runtime.SELinuxOption{
				User:  "",
				Role:  "user_r",
				Type:  "",
				Level: "s0:c1,c2",
			},
			processLabel: "",
			mountLabels:  []string{"", ""},
		},
		"Should be resolved correctly when selinuxOpt has been initialized completely": {
			selinuxOpt: &runtime.SELinuxOption{
				User:  "user_u",
				Role:  "user_r",
				Type:  "user_t",
				Level: "s0:c1,c2",
			},
			processLabel: "user_u:user_r:user_t:s0:c1,c2",
			mountLabels:  []string{"user_u:object_r:container_file_t:s0:c1,c2", "user_u:object_r:svirt_sandbox_file_t:s0:c1,c2"},
		},
		"Should return error when the format of 'level' is not correct": {
			selinuxOpt: &runtime.SELinuxOption{
				User:  "user_u",
				Role:  "user_r",
				Type:  "user_t",
				Level: "s0,c1,c2",
			},
			expectErr: true,
		},
	} {
		t.Run(desc, func(t *testing.T) {
			processLabel, mountLabel, err := initSelinuxOpts(test.selinuxOpt)
			if test.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.processLabel, processLabel)
				assert.Contains(t, test.mountLabels, mountLabel)
			}
		})
	}
}

func TestCheckSelinuxLevel(t *testing.T) {
	for desc, test := range map[string]struct {
		level     string
		expectErr bool
	}{
		"s0": {
			level: "s0",
		},
		"s0-s0": {
			level: "s0-s0",
		},
		"s0:c0": {
			level: "s0:c0",
		},
		"s0:c0.c3": {
			level: "s0:c0.c3",
		},
		"s0:c0,c3": {
			level: "s0:c0,c3",
		},
		"s0-s0:c0,c3": {
			level: "s0-s0:c0,c3",
		},
		"s0-s0:c0,c3.c6": {
			level: "s0-s0:c0,c3.c6",
		},
		"s0-s0:c0,c3.c6,c8.c10": {
			level: "s0-s0:c0,c3.c6,c8.c10",
		},
		"s0-s0:c0,c3.c6,c8,c10": {
			level: "s0-s0:c0,c3.c6",
		},
		"s0,c0,c3": {
			level:     "s0,c0,c3",
			expectErr: true,
		},
		"s0:c0.c3.c6": {
			level:     "s0:c0.c3.c6",
			expectErr: true,
		},
		"s0-s0,c0,c3": {
			level:     "s0-s0,c0,c3",
			expectErr: true,
		},
		"s0-s0:c0.c3.c6": {
			level:     "s0-s0:c0.c3.c6",
			expectErr: true,
		},
		"s0-s0:c0,c3.c6.c8": {
			level:     "s0-s0:c0,c3.c6.c8",
			expectErr: true,
		},
	} {
		t.Run(desc, func(t *testing.T) {
			ok, err := checkSelinuxLevel(test.level)
			if test.expectErr {
				assert.Error(t, err)
				assert.False(t, ok)
			} else {
				assert.NoError(t, err)
				assert.True(t, ok)
			}
		})
	}
}
