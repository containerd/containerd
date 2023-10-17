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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestNewUnexpectedStatusErr(t *testing.T) {
	type args struct {
		resp *http.Response
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
		want    string
	}{
		{
			name: "test normal",
			args: args{
				resp: &http.Response{
					StatusCode: http.StatusBadRequest,
					Status:     http.StatusText(http.StatusBadRequest),
					Request: &http.Request{
						Method: http.MethodGet,
						URL:    mustParseURL("http://127.0.0.1:3000/ns"),
					},
					Body: io.NopCloser(strings.NewReader(`{"errors":[{"code":"UNKNOWN","message":"unknown error","detail":"Generic error returned when the error does not have an API classification."}]}`)),
				},
			},
			wantErr: true,
			want:    "unexpected status code(400) from GET request to http://127.0.0.1:3000/ns: unknown error",
		},
		{
			name: "test response is invalid json",
			args: args{
				resp: &http.Response{
					StatusCode: http.StatusBadRequest,
					Status:     http.StatusText(http.StatusBadRequest),
					Request: &http.Request{
						Method: http.MethodGet,
						URL:    mustParseURL("http://127.0.0.1:3000/ns"),
					},
					Body: io.NopCloser(strings.NewReader(`{"errors":`)),
				},
			},
			wantErr: true,
			want:    "unexpected status code(400) from GET request to http://127.0.0.1:3000/ns: ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewUnexpectedStatusErr(tt.args.resp)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewUnexpectedStatusErr() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if err.Error() != tt.want {
					t.Errorf("error message except: %v, actual: %v", tt.want, err.Error())
				}
			}
		})
	}
}

func mustParseURL(u string) *url.URL {
	result, err := url.Parse(u)
	if err != nil {
		panic(fmt.Sprintf("parse url(%s) failed: %s", u, err))
	}
	return result
}
