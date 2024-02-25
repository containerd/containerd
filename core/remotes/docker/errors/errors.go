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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

var _ error = ErrUnexpectedStatus{}

// ErrUnexpectedStatus is returned if a registry API request returned with unexpected HTTP status
type ErrUnexpectedStatus struct {
	Status                    string
	StatusCode                int
	Body                      Errors
	RequestURL, RequestMethod string
}

func (e ErrUnexpectedStatus) Error() string {
	errorMessage := fmt.Sprintf("unexpected status from %s request to %s: %s", e.RequestMethod, e.RequestURL, e.Status)
	if e.Body.Len() > 0 {
		errorMessage += fmt.Sprintf("\n%s", e.Body.Error())
	}
	return errorMessage
}

// NewUnexpectedStatusErr creates an ErrUnexpectedStatus from HTTP response
func NewUnexpectedStatusErr(resp *http.Response) error {
	var b []byte
	if resp.Body != nil {
		b, _ = io.ReadAll(io.LimitReader(resp.Body, 64000)) // 64KB
	}

	var registryErr Errors
	json.Unmarshal(b, &registryErr)

	err := ErrUnexpectedStatus{
		Body:          registryErr,
		Status:        resp.Status,
		StatusCode:    resp.StatusCode,
		RequestMethod: resp.Request.Method,
	}
	if resp.Request.URL != nil {
		err.RequestURL = resp.Request.URL.String()
	}
	return err
}
