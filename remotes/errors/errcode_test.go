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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorsUnmarshalJSON(t *testing.T) {
	type args struct {
		data []byte
	}
	tests := []struct {
		name    string
		errs    *Errors
		args    args
		want    *Errors
		wantErr bool
	}{
		{
			name: "test unauthorized error",
			errs: new(Errors),
			args: args{
				data: []byte(`{"errors":[{"code":"UNAUTHORIZED","message":"authentication required","detail":"The access controller was unable to authenticate the client. Often this will be accompanied by a Www-Authenticate HTTP response header indicating how to authenticate."}]}`),
			},
			wantErr: false,
			want: &Errors{
				Error{
					Code:    "UNAUTHORIZED",
					Message: "authentication required",
					Detail:  `The access controller was unable to authenticate the client. Often this will be accompanied by a Www-Authenticate HTTP response header indicating how to authenticate.`,
				},
			},
		},
		{
			name: "test unknown error",
			errs: new(Errors),
			args: args{
				data: []byte(`{"errors":[{"code":"RANGE_INVALID","message":"invalid content range","detail":"When a layer is uploaded, the provided range is checked against the uploaded chunk. This error is returned if the range is out of order."}]}`),
			},
			wantErr: false,
			want: &Errors{
				Error{
					Code:    "UNKNOWN",
					Message: "invalid content range",
					Detail:  "When a layer is uploaded, the provided range is checked against the uploaded chunk. This error is returned if the range is out of order.",
				},
			},
		},
		{
			name: "test unknown error again",
			errs: new(Errors),
			args: args{
				data: []byte(`{"errors":[{"code":"UNKNOWN"}]}`),
			},
			wantErr: false,
			want: &Errors{
				ErrorCode("UNKNOWN"),
			},
		},
		{
			name: "test with error",
			errs: new(Errors),
			args: args{
				data: []byte(`{"errors":`),
			},
			wantErr: true,
			want:    new(Errors),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.errs.UnmarshalJSON(tt.args.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("Errors.UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !assert.Equal(t, tt.errs, tt.want) {
				t.Errorf("Errors unmarshal except: %v, actual: %v", tt.want, tt.errs)
			}
		})
	}
}

func TestErrorsMarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		errs    Errors
		want    []byte
		wantErr bool
	}{
		{
			name:    "test null",
			errs:    Errors{},
			want:    []byte(`{}`),
			wantErr: false,
		},
		{
			name: "test normal error",
			errs: Errors{
				Error{
					Code:    "UNKNOWN",
					Message: "unknown error",
					Detail:  "Generic error returned when the error does not have an API classification.",
				},
			},
			want:    []byte(`{"errors":[{"code":"UNKNOWN","message":"unknown error","detail":"Generic error returned when the error does not have an API classification."}]}`),
			wantErr: false,
		},
		{
			name: "test unknown error",
			errs: Errors{
				ErrorCode("UNKNOWN"),
			},
			want:    []byte(`{"errors":[{"code":"UNKNOWN","message":"unknown error"}]}`),
			wantErr: false,
		},
		{
			name: "test unknown error",
			errs: Errors{
				ErrorCode("invalid"),
			},
			want:    []byte(`{"errors":[{"code":"UNKNOWN","message":"unknown error"}]}`),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.errs.MarshalJSON()
			if (err != nil) != tt.wantErr {
				t.Errorf("Errors.MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			fmt.Println(string(got))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Errors.MarshalJSON() = %v, want %v", got, tt.want)
			}
		})
	}
}
