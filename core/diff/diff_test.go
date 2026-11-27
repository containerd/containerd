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

package diff

import (
	"context"
	_ "crypto/sha256" // required for digest.Canonical fallback path
	_ "crypto/sha512" // required for sha384 and sha512 digest support
	"testing"

	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestWithDigestAlgorithm(t *testing.T) {
	tests := []struct {
		name    string
		algo    digest.Algorithm
		wantErr bool
		want    digest.Algorithm
	}{
		{name: "empty is a no-op", algo: "", wantErr: false, want: ""},
		{name: "sha256 accepted", algo: digest.SHA256, wantErr: false, want: digest.SHA256},
		{name: "sha512 accepted", algo: digest.SHA512, wantErr: false, want: digest.SHA512},
		{name: "sha384 accepted", algo: digest.SHA384, wantErr: false, want: digest.SHA384},
		{name: "unknown rejected", algo: digest.Algorithm("not-a-real-algorithm"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			err := WithDigestAlgorithm(tt.algo)(&cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("WithDigestAlgorithm(%q): err = %v, wantErr %v", tt.algo, err, tt.wantErr)
			}
			if tt.wantErr {
				if !errdefs.IsInvalidArgument(err) {
					t.Fatalf("WithDigestAlgorithm(%q): error does not wrap ErrInvalidArgument: %v", tt.algo, err)
				}
				return
			}
			if cfg.DigestAlgorithm != tt.want {
				t.Fatalf("WithDigestAlgorithm(%q): cfg.DigestAlgorithm = %q, want %q", tt.algo, cfg.DigestAlgorithm, tt.want)
			}
		})
	}
}

func TestWithApplyDigestAlgorithm(t *testing.T) {
	tests := []struct {
		name    string
		algo    digest.Algorithm
		wantErr bool
		want    digest.Algorithm
	}{
		{name: "empty is a no-op", algo: "", wantErr: false, want: ""},
		{name: "sha256 accepted", algo: digest.SHA256, wantErr: false, want: digest.SHA256},
		{name: "sha512 accepted", algo: digest.SHA512, wantErr: false, want: digest.SHA512},
		{name: "sha384 accepted", algo: digest.SHA384, wantErr: false, want: digest.SHA384},
		{name: "unknown rejected", algo: digest.Algorithm("not-a-real-algorithm"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg ApplyConfig
			err := WithApplyDigestAlgorithm(tt.algo)(context.Background(), ocispec.Descriptor{}, &cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("WithApplyDigestAlgorithm(%q): err = %v, wantErr %v", tt.algo, err, tt.wantErr)
			}
			if tt.wantErr {
				if !errdefs.IsInvalidArgument(err) {
					t.Fatalf("WithApplyDigestAlgorithm(%q): error does not wrap ErrInvalidArgument: %v", tt.algo, err)
				}
				return
			}
			if cfg.DigestAlgorithm != tt.want {
				t.Fatalf("WithApplyDigestAlgorithm(%q): cfg.DigestAlgorithm = %q, want %q", tt.algo, cfg.DigestAlgorithm, tt.want)
			}
		})
	}
}
