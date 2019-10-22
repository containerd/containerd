// +build !windows

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

package containerd

import (
	"context"
	"testing"

	"github.com/containerd/containerd/runtime/linux/runctypes"
	"github.com/containerd/containerd/runtime/v2/runc/options"
)

func TestWithNoNewKeyringAddsNoNewKeyringToOptions(t *testing.T) {
	var taskInfo TaskInfo
	var ctx context.Context
	var client Client

	err := WithNoNewKeyring(ctx, &client, &taskInfo)
	if err != nil {
		t.Fatal(err)
	}

	opts := taskInfo.Options.(*runctypes.CreateOptions)

	if !opts.NoNewKeyring {
		t.Fatal("NoNewKeyring set on WithNoNewKeyring")
	}

}

func TestWithNoNewKeyringDoesNotOverwriteOtherOptions(t *testing.T) {
	var taskInfo TaskInfo
	var ctx context.Context
	var client Client

	taskInfo.Options = &runctypes.CreateOptions{NoPivotRoot: true}

	err := WithNoNewKeyring(ctx, &client, &taskInfo)
	if err != nil {
		t.Fatal(err)
	}

	opts := taskInfo.Options.(*runctypes.CreateOptions)

	if !opts.NoPivotRoot {
		t.Fatal("WithNoNewKeyring overwrote other options")
	}
}

func TestWithLoadCgroupstats(t *testing.T) {
	testCases := []struct {
		taskInfo TaskInfo
		input    bool
		hasErr   bool
		enabled  bool
	}{
		{
			taskInfo: TaskInfo{
				runtime: "io.containerd.runc.v1",
			},
			input:   true,
			hasErr:  false,
			enabled: true,
		},
		{
			taskInfo: TaskInfo{
				runtime: "io.containerd.runc.v2",
			},
			input:   false,
			hasErr:  false,
			enabled: false,
		},
		{
			taskInfo: TaskInfo{
				runtime: "io.containerd.runc.v2",
				Options: &runctypes.CreateOptions{},
			},
			input:   true,
			hasErr:  true,
			enabled: false,
		},
		{
			taskInfo: TaskInfo{
				runtime: "io.containerd.runtime.v1.linux",
			},
			input:   true,
			hasErr:  true,
			enabled: false,
		},
	}

	var (
		ctx    context.Context
		client Client
	)

	for idx, tc := range testCases {
		err := WithLoadCgroupstats(tc.input)(ctx, &client, &tc.taskInfo)
		if tc.hasErr != (err != nil) {
			t.Fatalf("[idx=%d] expected hasErr=%v, but got error=%v", idx, tc.hasErr, err)
		}

		if err == nil {
			opts := tc.taskInfo.Options.(*options.Options)
			if got := opts.LoadCgroupstats; got != tc.enabled {
				t.Fatalf("[idx=%d] expected loadCgroupstats=%v, but got %v", idx, tc.enabled, got)

			}
		}
	}
}
