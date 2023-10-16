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

package images

import (
	"context"
	"time"

	"github.com/containerd/containerd/pkg/cri/store/snapshot"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ImageFsInfo returns information of the filesystem that is used to store images.
// TODO(windows): Usage for windows is always 0 right now. Support this for windows.
// TODO(random-liu): Handle storage consumed by content store
func (c *CRIImageService) ImageFsInfo(ctx context.Context, r *runtime.ImageFsInfoRequest) (*runtime.ImageFsInfoResponse, error) {
	snapshots := c.snapshotStore.List()
	snapshotterFSInfos := map[string]snapshot.Snapshot{}

	for _, sn := range snapshots {
		if info, ok := snapshotterFSInfos[sn.Key.Snapshotter]; ok {
			// Use the oldest timestamp as the timestamp of imagefs info.
			if sn.Timestamp < info.Timestamp {
				info.Timestamp = sn.Timestamp
			}
			info.Size += sn.Size
			info.Inodes += sn.Inodes
			snapshotterFSInfos[sn.Key.Snapshotter] = info
		} else {
			snapshotterFSInfos[sn.Key.Snapshotter] = snapshot.Snapshot{
				Timestamp: sn.Timestamp,
				Size:      sn.Size,
				Inodes:    sn.Inodes,
			}
		}
	}

	var imageFilesystems []*runtime.FilesystemUsage

	// Currently kubelet always consumes the first entry of the returned array,
	// so put the default snapshotter as the first entry for compatibility.
	if info, ok := snapshotterFSInfos[c.config.Snapshotter]; ok {
		imageFilesystems = append(imageFilesystems, &runtime.FilesystemUsage{
			Timestamp:  info.Timestamp,
			FsId:       &runtime.FilesystemIdentifier{Mountpoint: c.imageFSPaths[c.config.Snapshotter]},
			UsedBytes:  &runtime.UInt64Value{Value: info.Size},
			InodesUsed: &runtime.UInt64Value{Value: info.Inodes},
		})
		delete(snapshotterFSInfos, c.config.Snapshotter)
	} else {
		imageFilesystems = append(imageFilesystems, &runtime.FilesystemUsage{
			Timestamp:  time.Now().UnixNano(),
			FsId:       &runtime.FilesystemIdentifier{Mountpoint: c.imageFSPaths[c.config.Snapshotter]},
			UsedBytes:  &runtime.UInt64Value{Value: 0},
			InodesUsed: &runtime.UInt64Value{Value: 0},
		})
	}

	for snapshotter, info := range snapshotterFSInfos {
		imageFilesystems = append(imageFilesystems, &runtime.FilesystemUsage{
			Timestamp:  info.Timestamp,
			FsId:       &runtime.FilesystemIdentifier{Mountpoint: c.imageFSPaths[snapshotter]},
			UsedBytes:  &runtime.UInt64Value{Value: info.Size},
			InodesUsed: &runtime.UInt64Value{Value: info.Inodes},
		})
	}

	return &runtime.ImageFsInfoResponse{ImageFilesystems: imageFilesystems}, nil
}
