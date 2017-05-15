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

package metadata

import "github.com/kubernetes-incubator/cri-containerd/pkg/metadata/store"

// IsNotExistError is a helper function to check whether the error returned
// by metadata store is not exist error.
func IsNotExistError(err error) bool {
	return err.Error() == store.ErrNotExist.Error()
}
