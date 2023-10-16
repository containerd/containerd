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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/containerd/log"
)

var _ error = ErrUnexpectedStatus{}

// ErrUnexpectedStatus is returned if a registry API request returned with unexpected HTTP status
type ErrUnexpectedStatus struct {
	Status                    string
	StatusCode                int
	Body                      []byte
	RequestURL, RequestMethod string
	Message                   []string
}

func (e ErrUnexpectedStatus) Error() string {
	return fmt.Sprintf("unexpected status code(%d) from %s request to %s: %s", e.StatusCode, e.RequestMethod, e.RequestURL, strings.Join(e.Message, ","))
}

// NewUnexpectedStatusErr creates an ErrUnexpectedStatus from HTTP response
func NewUnexpectedStatusErr(resp *http.Response) error {
	var b []byte
	var message []string
	if resp.Body != nil {
		b, _ = io.ReadAll(io.LimitReader(resp.Body, 64000)) // 64KB
		if len(b) > 0 {
			var r ErrorResponse
			err := json.Unmarshal(b, &r)
			if err != nil {
				log.G(context.Background()).Debugf("Unmarshal response body failed: %v", err)
			} else {
				for _, e := range r.Errors {
					if len(e.Message) > 0 {
						message = append(message, e.Message)
					}
				}
			}
		}
	}
	err := ErrUnexpectedStatus{
		Body:          b,
		Status:        resp.Status,
		StatusCode:    resp.StatusCode,
		RequestMethod: resp.Request.Method,
		Message:       message,
	}
	if resp.Request.URL != nil {
		err.RequestURL = resp.Request.URL.String()
	}
	return err
}

// ---------------------------- begin ----------------------------
// Borrow code from here: https://github.com/opencontainers/distribution-spec/blob/v1.1.0-rc3/specs-go/v1/error.go

// ErrorResponse is returned by a registry on an invalid request.
type ErrorResponse struct {
	Errors []ErrorInfo `json:"errors"`
}

// ErrRegistry is the string returned by and ErrorResponse error.
var ErrRegistry = "distribution: registry returned error"

// Error implements the Error interface.
func (er *ErrorResponse) Error() string {
	return ErrRegistry
}

// Detail returns an ErrorInfo
func (er *ErrorResponse) Detail() []ErrorInfo {
	return er.Errors
}

// ErrorInfo describes a server error returned from a registry.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail"`
}

// ---------------------------- end ----------------------------
