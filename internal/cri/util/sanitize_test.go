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
	assert.Equal(t, urlErr, sanitizedErr.urlError)
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
