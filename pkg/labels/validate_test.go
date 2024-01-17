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

package labels

import (
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/pkg/errdefs"
	"github.com/stretchr/testify/assert"
)

func TestValidLabels(t *testing.T) {
	shortStr := "s"
	longStr := strings.Repeat("s", maxSize-1)

	for key, value := range map[string]string{
		"some":   "value",
		shortStr: longStr,
	} {
		if err := Validate(key, value); err != nil {
			t.Fatalf("unexpected error: %v != nil", err)
		}
	}
}

func TestInvalidLabels(t *testing.T) {
	addOneStr := "s"
	maxSizeStr := strings.Repeat("s", maxSize)

	for key, value := range map[string]string{
		maxSizeStr: addOneStr,
	} {
		if err := Validate(key, value); err == nil {
			t.Fatal("expected invalid error")
		} else if !errdefs.IsInvalidArgument(err) {
			t.Fatal("error should be an invalid label error")
		}
	}
}

func TestLongKey(t *testing.T) {
	key := strings.Repeat("s", keyMaxLen+1)
	value := strings.Repeat("v", maxSize-len(key))

	err := Validate(key, value)
	assert.Equal(t, err, nil)

	key = strings.Repeat("s", keyMaxLen+12)
	value = strings.Repeat("v", maxSize-len(key)+1)

	err = Validate(key, value)
	assert.ErrorIs(t, err, errdefs.ErrInvalidArgument)

	key = strings.Repeat("s", keyMaxLen-1)
	value = strings.Repeat("v", maxSize-len(key))

	err = Validate(key, value)
	assert.Equal(t, err, nil)

	key = strings.Repeat("s", keyMaxLen-1)
	value = strings.Repeat("v", maxSize-len(key)-1)

	err = Validate(key, value)
	assert.Equal(t, err, nil)
}
