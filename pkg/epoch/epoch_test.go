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

package epoch

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSourceDateEpoch(t *testing.T) {
	if s, ok := os.LookupEnv(SourceDateEpochEnv); ok {
		t.Logf("%s is already set to %q, unsetting", SourceDateEpochEnv, s)
		// see https://github.com/golang/go/issues/52817#issuecomment-1131339120
		t.Setenv(SourceDateEpochEnv, "")
		os.Unsetenv(SourceDateEpochEnv)
	}

	t.Run("WithoutSourceDateEpoch", func(t *testing.T) {
		vp, err := SourceDateEpoch()
		require.NoError(t, err)
		require.Nil(t, vp)
	})

	t.Run("WithEmptySourceDateEpoch", func(t *testing.T) {
		const emptyValue = ""
		t.Setenv(SourceDateEpochEnv, emptyValue)

		vp, err := SourceDateEpoch()
		require.NoError(t, err)
		require.Nil(t, vp)

		vp, err = ParseSourceDateEpoch(emptyValue)
		require.Error(t, err, "value is empty")
		require.Nil(t, vp)
	})

	t.Run("WithSourceDateEpoch", func(t *testing.T) {
		const rfc3339Str = "2022-01-23T12:34:56Z"
		sourceDateEpoch, err := time.Parse(time.RFC3339, rfc3339Str)
		require.NoError(t, err)

		SetSourceDateEpoch(sourceDateEpoch)
		t.Cleanup(UnsetSourceDateEpoch)

		vp, err := SourceDateEpoch()
		require.NoError(t, err)
		require.True(t, vp.Equal(sourceDateEpoch.UTC()))

		vp, err = ParseSourceDateEpoch(fmt.Sprintf("%d", sourceDateEpoch.Unix()))
		require.NoError(t, err)
		require.True(t, vp.Equal(sourceDateEpoch))
	})

	t.Run("WithInvalidSourceDateEpoch", func(t *testing.T) {
		const invalidValue = "foo"
		t.Setenv(SourceDateEpochEnv, invalidValue)

		vp, err := SourceDateEpoch()
		require.ErrorContains(t, err, "invalid SOURCE_DATE_EPOCH value")
		require.Nil(t, vp)

		vp, err = ParseSourceDateEpoch(invalidValue)
		require.ErrorContains(t, err, "invalid value:")
		require.Nil(t, vp)
	})
}
