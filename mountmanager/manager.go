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

package mountmanager

import "context"

/*
   Mount manager adds new RPC APIs to containerd that allow other processes (like shim) to call containerd to
   mount/unmount snapshots instead of doing that themselves. This allows for a cleaner interface and greater
   extensibility. The creation, mount, unmount & cleanup of snapshots can now be handled in a single
   process. Different snapshotters that can't necessarily conform to the standard serialized mount/unmount
   call API can use this API to extend the mount functionality.

   For example, on Windows when using CimFS, the mounts needs to be refcounted and reused if a certain
   snapshot is already mounted. This plugin can handle such situations.
*/

// MountManager is the interface that every mount manager plugin needs to implement
type MountManager interface {
	// Both Mount & Unmount take in a context and an arbitrary interface{} that contains the data specific
	// to the implementing mount manager plugin. This plugin is expected to correctly handle the type
	// conversions of these values. The returned value is also an interface{} and this value will be
	// forwarded to the call as it is. It's the caller's responsibility to make sense of that data.
	Mount(ctx context.Context, data interface{}) (interface{}, error)
	Unmount(ctx context.Context, data interface{}) (interface{}, error)
	// Allows the mount manager to cleanup any resources before containerd is shutting down
	Close()
}
