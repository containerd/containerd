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

package mount

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithAllowMountType(t *testing.T) {
	var o ActivateOptions
	WithAllowMountType("erofs")(&o)
	WithAllowMountType("loop")(&o)

	assert.Equal(t, []string{"erofs", "loop"}, o.AllowMountTypes)
	assert.Empty(t, o.AllowTransforms)
}

func TestWithAllowTransform(t *testing.T) {
	var o ActivateOptions
	WithAllowTransform("format")(&o)
	WithAllowTransform("mkfs")(&o)

	assert.Equal(t, []string{"format", "mkfs"}, o.AllowTransforms)
	assert.Empty(t, o.AllowMountTypes)
}

func TestWithAllowMountTypeAndTransformCombine(t *testing.T) {
	var o ActivateOptions
	WithAllowMountType("block")(&o)
	WithAllowTransform("format")(&o)

	assert.Equal(t, []string{"block"}, o.AllowMountTypes)
	assert.Equal(t, []string{"format"}, o.AllowTransforms)
}
