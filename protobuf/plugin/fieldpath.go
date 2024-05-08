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

package plugin

import (
	"github.com/containerd/containerd/api/types"
)

// This package is deprecated, public fields are imported for compatibility
var (
	E_FieldpathAll                                                        = types.E_FieldpathAll                                                  //nolint:revive // underscores are not allowed, but this was original name
	E_Fieldpath                                                           = types.E_Fieldpath                                                     //nolint:revive // underscores are not allowed, but this was original name
	File_github_com_containerd_containerd_protobuf_plugin_fieldpath_proto = types.File_github_com_containerd_containerd_api_types_fieldpath_proto //nolint:revive // underscores are not allowed, but this was original name
)
