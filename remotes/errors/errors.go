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
			errors := Errors{}
			err := (&errors).UnmarshalJSON(b)
			if err != nil {
				log.G(context.Background()).Debugf("Unmarshal response body failed: %v", err)
			} else {
				for _, err := range errors {
					e, ok := err.(Error)
					if ok {
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
