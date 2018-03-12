/*
Copyright 2018 The containerd Authors.

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

package util

import (
	"time"

	"golang.org/x/net/context"
)

// deferCleanupTimeout is the default timeout for containerd cleanup operations
// in defer.
const deferCleanupTimeout = 1 * time.Minute

// DeferContext returns a context for containerd cleanup operations in defer.
// A default timeout is applied to avoid cleanup operation pending forever.
// TODO(random-liu): Add namespace after local services are used.
// (containerd/containerd#2183)
func DeferContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), deferCleanupTimeout)
}
