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

package util

import (
	"errors"
	"fmt"
	"net/url"
	"testing"

	remoteerrors "github.com/containerd/containerd/v2/core/remotes/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeError_SimpleURLError(t *testing.T) {
	// Create a url.Error with sensitive info
	originalURL := "https://storage.blob.core.windows.net/container/blob?sig=SECRET&sv=2020"
	urlErr := &url.Error{
		Op:  "Get",
		URL: originalURL,
		Err: fmt.Errorf("connection timeout"),
	}

	// Sanitize
	sanitized := SanitizeError(urlErr)
	require.NotNil(t, sanitized)

	// Check it's a sanitizedError with correct properties
	sanitizedErr, ok := sanitized.(*sanitizedError)
	require.True(t, ok, "Should return *sanitizedError type")
	assert.Equal(t, urlErr, sanitizedErr.original)
	assert.Equal(t, originalURL, sanitizedErr.originalURL)
	assert.Equal(t, "https://storage.blob.core.windows.net/container/blob?sig=%5BREDACTED%5D&sv=%5BREDACTED%5D", sanitizedErr.sanitizedURL)

	// Test Error() method - verifies ReplaceAll functionality
	expected := "Get \"https://storage.blob.core.windows.net/container/blob?sig=%5BREDACTED%5D&sv=%5BREDACTED%5D\": connection timeout"
	assert.Equal(t, expected, sanitized.Error())
}

func TestSanitizeError_WrappedError(t *testing.T) {
	originalURL := "https://storage.blob.core.windows.net/blob?sig=SECRET&sv=2020"
	urlErr := &url.Error{
		Op:  "Get",
		URL: originalURL,
		Err: fmt.Errorf("timeout"),
	}

	wrappedErr := fmt.Errorf("image pull failed: %w", urlErr)

	// Sanitize
	sanitized := SanitizeError(wrappedErr)

	// Test Error() method with wrapped error - verifies ReplaceAll works in wrapped context
	sanitizedMsg := sanitized.Error()
	assert.NotContains(t, sanitizedMsg, "SECRET", "Secret should be sanitized")
	assert.Contains(t, sanitizedMsg, "image pull failed", "Wrapper message should be preserved")
	assert.Contains(t, sanitizedMsg, "%5BREDACTED%5D", "Should contain sanitized marker")

	// Should still be able to unwrap to url.Error
	var targetURLErr *url.Error
	assert.True(t, errors.As(sanitized, &targetURLErr),
		"Should be able to find *url.Error in sanitized error chain")

	// Verify url.Error properties are preserved
	assert.Equal(t, "Get", targetURLErr.Op)
	assert.Contains(t, targetURLErr.Err.Error(), "timeout")
}

func TestSanitizeError_UnexpectedStatusError(t *testing.T) {
	// Registry non-2xx responses (e.g. 503 from an S3-backed blob redirect)
	// return an ErrUnexpectedStatus, whose message embeds the raw signed URL
	// but which is not a *url.Error.
	originalURL := "https://prod-us-east-1-starport-layer-bucket.s3.us-east-1.amazonaws.com/blob?X-Amz-Security-Token=SECRET_TOKEN&X-Amz-Signature=SECRET_SIG&rid=abc123"
	statusErr := remoteerrors.ErrUnexpectedStatus{
		Status:        "503 Slow Down",
		StatusCode:    503,
		RequestURL:    originalURL,
		RequestMethod: "GET",
	}

	sanitized := SanitizeError(statusErr)
	require.NotNil(t, sanitized)

	// Check it's a sanitizedError with correct properties
	sanitizedErr, ok := sanitized.(*sanitizedError)
	require.True(t, ok, "Should return *sanitizedError type")
	assert.Equal(t, originalURL, sanitizedErr.originalURL)

	// Every query param value must be redacted.
	msg := sanitized.Error()
	assert.NotContains(t, msg, "SECRET_TOKEN", "Security token should be sanitized")
	assert.NotContains(t, msg, "SECRET_SIG", "Signature should be sanitized")
	assert.NotContains(t, msg, "abc123", "All query params should be sanitized")
	assert.Contains(t, msg, "%5BREDACTED%5D", "Should contain sanitized marker")
	assert.Contains(t, msg, "unexpected status from GET request to", "Status message should be preserved")
	assert.Contains(t, msg, "503 Slow Down", "Status text should be preserved")
}

func TestSanitizeError_WrappedUnexpectedStatusError(t *testing.T) {
	// Mirrors the real log path: the fetch error is wrapped by httpReadSeeker
	// and again by the image-pull layer. errors.As must find the value-type
	// ErrUnexpectedStatus through the wrappers.
	originalURL := "https://prod-us-east-1-starport-layer-bucket.s3.us-east-1.amazonaws.com/blob?X-Amz-Signature=SECRET_SIG&X-Amz-Credential=SECRET_CRED"
	statusErr := remoteerrors.ErrUnexpectedStatus{
		Status:        "503 Slow Down",
		StatusCode:    503,
		RequestURL:    originalURL,
		RequestMethod: "GET",
	}
	wrappedErr := fmt.Errorf("failed to copy: httpReadSeeker: failed open: %w", statusErr)

	sanitized := SanitizeError(wrappedErr)

	msg := sanitized.Error()
	assert.NotContains(t, msg, "SECRET_SIG", "Signature should be sanitized")
	assert.NotContains(t, msg, "SECRET_CRED", "Credential should be sanitized")
	assert.Contains(t, msg, "%5BREDACTED%5D", "Should contain sanitized marker")
	assert.Contains(t, msg, "httpReadSeeker: failed open", "Wrapper message should be preserved")

	// Should still be able to unwrap to the original ErrUnexpectedStatus.
	var targetStatusErr remoteerrors.ErrUnexpectedStatus
	assert.True(t, errors.As(sanitized, &targetStatusErr),
		"Should be able to find ErrUnexpectedStatus in sanitized error chain")
	assert.Equal(t, 503, targetStatusErr.StatusCode)
}

func TestSanitizeError_UnexpectedStatusNoQueryParams(t *testing.T) {
	// A registry error whose URL has no query params has nothing to redact and
	// must pass through unchanged.
	statusErr := remoteerrors.ErrUnexpectedStatus{
		Status:        "500 Internal Server Error",
		StatusCode:    500,
		RequestURL:    "https://registry.example.com/v2/image/blobs/sha256:abc",
		RequestMethod: "GET",
	}

	sanitized := SanitizeError(statusErr)

	assert.Equal(t, statusErr, sanitized,
		"Registry errors without query params should pass through unchanged")
}

func TestSanitizeError_NonURLError(t *testing.T) {
	// Regular error without url.Error
	regularErr := fmt.Errorf("some error occurred")

	sanitized := SanitizeError(regularErr)

	// Should return the exact same error object
	assert.Equal(t, regularErr, sanitized,
		"Non-URL errors should pass through unchanged")
}

func TestSanitizeError_NilError(t *testing.T) {
	sanitized := SanitizeError(nil)
	assert.Nil(t, sanitized, "nil error should return nil")
}

func TestSanitizeError_NoQueryParams(t *testing.T) {
	// URL without any query parameters
	urlErr := &url.Error{
		Op:  "Get",
		URL: "https://registry.example.com/v2/image/manifests/latest",
		Err: fmt.Errorf("not found"),
	}

	sanitized := SanitizeError(urlErr)

	// Should return the same error object (no sanitization needed)
	assert.Equal(t, urlErr, sanitized,
		"Errors without query params should pass through unchanged")
}

func TestSanitizedError_Unwrap(t *testing.T) {
	originalURL := "https://storage.blob.core.windows.net/blob?sig=SECRET"
	urlErr := &url.Error{
		Op:  "Get",
		URL: originalURL,
		Err: fmt.Errorf("timeout"),
	}

	sanitized := SanitizeError(urlErr)

	// Should be able to unwrap
	unwrapped := errors.Unwrap(sanitized)
	assert.NotNil(t, unwrapped, "Should be able to unwrap sanitized error")
	assert.Equal(t, urlErr, unwrapped, "Unwrapped should be the original error")
}
