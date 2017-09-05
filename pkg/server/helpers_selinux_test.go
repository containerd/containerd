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
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"
)

func TestInitSelinuxOpts(t *testing.T) {
	if !selinux.GetEnabled() {
		return
	}

	for desc, test := range map[string]struct {
		selinuxOpt   *runtime.SELinuxOption
		processLabel string
		mountLabels  []string
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
	} {
		t.Logf("TestCase %q", desc)
		processLabel, mountLabel, err := initSelinuxOpts(test.selinuxOpt)
		assert.NoError(t, err)
		assert.Equal(t, test.processLabel, processLabel)
		assert.Contains(t, test.mountLabels, mountLabel)
	}
}
