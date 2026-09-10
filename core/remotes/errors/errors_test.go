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

package errors

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrUnexpectedStatus(t *testing.T) {
	reqURL, err := url.Parse("https://example.com/v2/image/manifests/latest")
	require.NoError(t, err)

	t.Run("with Retry-After header", func(t *testing.T) {
		resp := &http.Response{
			Status:     "429 Too Many Requests",
			StatusCode: http.StatusTooManyRequests,
			Header: http.Header{
				"Retry-After": []string{"120"},
			},
			Body: io.NopCloser(strings.NewReader("rate limit reached")),
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    reqURL,
			},
		}

		err := NewUnexpectedStatusErr(resp)
		require.Error(t, err)

		statusErr, ok := err.(ErrUnexpectedStatus)
		require.True(t, ok)
		assert.Equal(t, "429 Too Many Requests", statusErr.Status)
		assert.Equal(t, http.StatusTooManyRequests, statusErr.StatusCode)
		assert.Equal(t, "120", statusErr.RetryAfter)
		assert.Equal(t, "unexpected status from GET request to https://example.com/v2/image/manifests/latest: 429 Too Many Requests (Retry-After: 120)", statusErr.Error())
	})

	t.Run("without Retry-After header", func(t *testing.T) {
		resp := &http.Response{
			Status:     "500 Internal Server Error",
			StatusCode: http.StatusInternalServerError,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("error")),
			Request: &http.Request{
				Method: http.MethodHead,
				URL:    reqURL,
			},
		}

		err := NewUnexpectedStatusErr(resp)
		require.Error(t, err)

		statusErr, ok := err.(ErrUnexpectedStatus)
		require.True(t, ok)
		assert.Equal(t, "", statusErr.RetryAfter)
		assert.Equal(t, "unexpected status from HEAD request to https://example.com/v2/image/manifests/latest: 500 Internal Server Error", statusErr.Error())
	})

	t.Run("typeurl registration", func(t *testing.T) {
		err := ErrUnexpectedStatus{
			Status:        "429 Too Many Requests",
			StatusCode:    429,
			RetryAfter:    "60",
			RequestURL:    "https://example.com",
			RequestMethod: "GET",
		}

		any, errMarshal := typeurl.MarshalAny(&err)
		require.NoError(t, errMarshal)

		unmarshaled, errUnmarshal := typeurl.UnmarshalAny(any)
		require.NoError(t, errUnmarshal)

		statusErr, ok := unmarshaled.(*ErrUnexpectedStatus)
		require.True(t, ok)
		assert.Equal(t, "60", statusErr.RetryAfter)
		assert.Equal(t, 429, statusErr.StatusCode)
	})
}
