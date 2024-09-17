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

package integration

import (
	"fmt"
	"path/filepath"
	"testing"

	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestImageMount(t *testing.T) {
	testImage, err := getTestImage()
	if err != nil {
		t.Fatal(err)
	}
	testMountImage := testImage
	mountPath := "c:\\image-mount"
	EnsureImageExists(t, testMountImage)
	testImageMount(t, testImage, testMountImage, mountPath, []string{
		"cmd",
		"/c",
		"dir",
		"/b",
		filepath.Join(mountPath, "License.txt"),
	}, []string{
		fmt.Sprintf("%s %s %s", criruntime.Stdout, criruntime.LogTagFull, "License.txt"),
	})
}
