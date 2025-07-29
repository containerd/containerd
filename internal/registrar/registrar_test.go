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

package registrar

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistrar(t *testing.T) {
	r := NewRegistrar()
	require := require.New(t)

	t.Logf("should be able to reserve a name<->key mapping")
	require.NoError(r.Reserve("test-name-1", "test-id-1"))

	t.Logf("should be able to reserve a new name<->key mapping")
	require.NoError(r.Reserve("test-name-2", "test-id-2"))

	t.Logf("should be able to reserve the same name<->key mapping")
	require.NoError(r.Reserve("test-name-1", "test-id-1"))

	t.Logf("should not be able to reserve conflict name<->key mapping")
	require.Error(r.Reserve("test-name-1", "test-id-conflict"))
	require.Error(r.Reserve("test-name-conflict", "test-id-2"))

	t.Logf("should be able to release name<->key mapping by key")
	r.ReleaseByKey("test-id-1")

	t.Logf("should be able to release name<->key mapping by name")
	r.ReleaseByName("test-name-2")

	t.Logf("should be able to reserve new name<->key mapping after release")
	require.NoError(r.Reserve("test-name-1", "test-id-new"))
	require.NoError(r.Reserve("test-name-new", "test-id-2"))

	t.Logf("should be able to reserve same name/key name<->key")
	require.NoError(r.Reserve("same-name-id", "same-name-id"))
}
