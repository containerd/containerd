//go:build windows

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

package lcow

import "testing"

func TestIsSupportedSnapshotterFileSystem(t *testing.T) {
	tests := []struct {
		name      string
		fsType    string
		supported bool
	}{
		{name: "NTFS", fsType: "NTFS", supported: true},
		{name: "case insensitive NTFS", fsType: "nTfS", supported: true},
		{name: "ReFS", fsType: "ReFS", supported: true},
		{name: "case insensitive ReFS", fsType: "refs", supported: true},
		{name: "FAT32", fsType: "FAT32", supported: false},
		{name: "empty", fsType: "", supported: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := isSupportedSnapshotterFileSystem(test.fsType); actual != test.supported {
				t.Fatalf("isSupportedSnapshotterFileSystem(%q) = %t, want %t", test.fsType, actual, test.supported)
			}
		})
	}
}

func TestNewSnapshotter(t *testing.T) {
	snapshotter, err := NewSnapshotter(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshotter.Close(); err != nil {
		t.Fatal(err)
	}
}
