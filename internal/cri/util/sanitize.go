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
)

// SanitizeError sanitizes an error by redacting sensitive information in URLs.
// If the error contains a *url.Error, it parses and sanitizes the URL.
// Otherwise, it returns the error unchanged.
func SanitizeError(err error) error {
	if err == nil {
		return nil
	}

	// Check if the error is or contains a *url.Error
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		// Parse and sanitize the URL
		sanitizedURL := sanitizeURL(urlErr.URL)
		if sanitizedURL != urlErr.URL {
			// Wrap with sanitized url.Error
			return &sanitizedError{
				original:     err,
				sanitizedURL: sanitizedURL,
				urlError:     urlErr,
			}
		}
		return err
	}

	// No sanitization needed for non-URL errors
	return err
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

// sanitizedError wraps an error containing a *url.Error with a sanitized URL.
type sanitizedError struct {
	original     error
	sanitizedURL string
	urlError     *url.Error
}

// Error returns the error message with the sanitized URL.
func (e *sanitizedError) Error() string {
	// Replace all occurrences of the original URL with the sanitized version
	return strings.ReplaceAll(e.original.Error(), e.urlError.URL, e.sanitizedURL)
}

// Unwrap returns the original error for error chain traversal.
func (e *sanitizedError) Unwrap() error {
	return e.original
}
