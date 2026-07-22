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
	"net/url"
	"strings"

	remoteerrors "github.com/containerd/containerd/v2/core/remotes/errors"
)

// SanitizeError sanitizes an error by redacting sensitive information in URLs.
// If the error contains a *url.Error or an ErrUnexpectedStatus, it parses and
// sanitizes the embedded URL. Otherwise, it returns the error unchanged.
func SanitizeError(err error) error {
	if err == nil {
		return nil
	}

	// Determine the raw URL embedded in the error message, if any.
	var rawURL string
	var urlErr *url.Error
	var statusErr remoteerrors.ErrUnexpectedStatus
	switch {
	case errors.As(err, &urlErr):
		rawURL = urlErr.URL
	case errors.As(err, &statusErr):
		// ErrUnexpectedStatus embeds the raw request URL (including signed
		// query params) in its message but is not a *url.Error.
		rawURL = statusErr.RequestURL
	default:
		// No sanitization needed for non-URL errors
		return err
	}

	sanitizedURL := sanitizeURL(rawURL)
	if sanitizedURL == rawURL {
		return err
	}
	return &sanitizedError{
		original:     err,
		originalURL:  rawURL,
		sanitizedURL: sanitizedURL,
	}
}

// sanitizeURL properly parses a URL and redacts all query parameters.
func sanitizeURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		// If URL parsing fails, return original (malformed URLs shouldn't leak tokens)
		return rawURL
	}

	// Check if URL has query parameters
	query := parsed.Query()
	if len(query) == 0 {
		return rawURL
	}

	// Redact all query parameters
	for param := range query {
		query.Set(param, "[REDACTED]")
	}

	// Reconstruct URL with sanitized query
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// sanitizedError wraps an error whose message embeds a URL with a sanitized URL.
type sanitizedError struct {
	original     error
	originalURL  string
	sanitizedURL string
}

// Error returns the error message with the sanitized URL.
func (e *sanitizedError) Error() string {
	// Replace all occurrences of the original URL with the sanitized version
	return strings.ReplaceAll(e.original.Error(), e.originalURL, e.sanitizedURL)
}

// Unwrap returns the original error for error chain traversal.
func (e *sanitizedError) Unwrap() error {
	return e.original
}
