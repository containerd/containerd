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
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func rightAfter(t1, t2 time.Time) bool {
	if t2.Equal(t1) {
		return true
	}
	threshold := 10 * time.Millisecond
	if runtime.GOOS == "windows" {
		// Low timer resolution on Windows
		threshold *= 10
	}
	return t2.After(t1) && t2.Before(t1.Add(threshold))
}

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

		now := time.Now().UTC()
		v := SourceDateEpochOrNow()
		require.True(t, rightAfter(now, v), "now: %s, v: %s", now, v)
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

		now := time.Now().UTC()
		v := SourceDateEpochOrNow()
		require.True(t, rightAfter(now, v), "now: %s, v: %s", now, v)
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

		v := SourceDateEpochOrNow()
		require.True(t, v.Equal(sourceDateEpoch))
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

		now := time.Now().UTC()
		v := SourceDateEpochOrNow()
		require.True(t, rightAfter(now, v), "now: %s, v: %s", now, v)
	})
}
