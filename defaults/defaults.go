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

package defaults

const (
	// DefaultMaxRecvMsgSize defines the default maximum message size for
	// receiving protobufs passed over the GRPC API.
	DefaultMaxRecvMsgSize = 16 << 20
	// DefaultMaxSendMsgSize defines the default maximum message size for
	// sending protobufs passed over the GRPC API.
	DefaultMaxSendMsgSize = 16 << 20
	// DefaultRuntimeNSLabel defines the namespace label to check for the
	// default runtime
	DefaultRuntimeNSLabel = "containerd.io/defaults/runtime"
	// DefaultSnapshotterNSLabel defines the namespace label to check for the
	// default snapshotter
	DefaultSnapshotterNSLabel = "containerd.io/defaults/snapshotter"
	// DefaultWindowSize defines the default window size for stream
	// issue: https://github.com/containerd/containerd/issues/6190
	// reference： https://github.com/earthly/buildkit/pull/42/files
	DefaultWindowSize = 2 << 20
	// DefaultConnWindowSize defines the default window size for a connection
	// issue: https://github.com/containerd/containerd/issues/6190
	// reference： https://github.com/earthly/buildkit/pull/42/files
	DefaultConnWindowSize = 2 << 19
)
