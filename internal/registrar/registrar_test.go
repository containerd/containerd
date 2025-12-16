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

	assertlib "github.com/stretchr/testify/assert"
)

func TestRegistrar(t *testing.T) {
	r := NewRegistrar()
	assert := assertlib.New(t)
	var err error
	var resErr *ReservedErr

	t.Logf("should be able to reserve a name<->key mapping")
	assert.NoError(r.Reserve("test-name-1", "test-id-1"))

	t.Logf("should be able to reserve a new name<->key mapping")
	assert.NoError(r.Reserve("test-name-2", "test-id-2"))

	t.Logf("should be able to reserve the same name<->key mapping")
	assert.NoError(r.Reserve("test-name-1", "test-id-1"))

	t.Logf("should not be able to reserve conflict name<->key mapping")
	err = r.Reserve("test-name-1", "test-id-conflict")
	assert.Error(err)
	assert.ErrorAs(err, &resErr)
	assert.ErrorContains(resErr, `name "test-name-1" is reserved for "test-id-1"`)
	err = r.Reserve("test-name-conflict", "test-id-2")
	assert.Error(err)
	assert.ErrorAs(err, &resErr)
	assert.ErrorContains(resErr, `key "test-id-2" is reserved for "test-name-2"`)

	t.Logf("should be able to release name<->key mapping by key")
	r.ReleaseByKey("test-id-1")

	t.Logf("should be able to release name<->key mapping by name")
	r.ReleaseByName("test-name-2")

	t.Logf("should be able to reserve new name<->key mapping after release")
	assert.NoError(r.Reserve("test-name-1", "test-id-new"))
	assert.NoError(r.Reserve("test-name-new", "test-id-2"))

	t.Logf("should be able to reserve same name/key name<->key")
	assert.NoError(r.Reserve("same-name-id", "same-name-id"))
}
