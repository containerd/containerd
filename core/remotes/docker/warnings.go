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

package docker

import (
	"context"
	"encoding/hex"
	"net/http"
	"strings"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/v2/pkg/reference"
)

// WarningSource contains the information about the request that caused the warning.
type WarningSource struct {
	// Ref is the reference specification of the content that the warning is for.
	Ref reference.Spec

	// Desc is the descriptor of the content that the warning is for.
	// Can be nil if the warning is not for a specific content.
	Desc *ocispec.Descriptor

	// Digest is the digest of the content that the warning is for.
	// Can be nil if the warning is not for a specific content.
	Digest *digest.Digest
}

// WarningHandler is a function that receives warnings from the registry.
// Warnings are extracted from HTTP Warning headers as defined in RFC 7234.
// The warning parameter contains the warn-text from the header.
// The descriptor parameter contains the descriptor of the content that the warning is for.
// The descriptor is nil if the warning is not for a specific content.
type WarningHandler interface {
	Warn(src WarningSource, warning string)
}

type warningHandlerFunc func(src WarningSource, warning string)

func (f warningHandlerFunc) Warn(src WarningSource, warning string) {
	f(src, warning)
}

type warningSourceKey struct{}

// reportWarnings extracts and reports warnings from HTTP Warning headers.
// Per RFC 7234 and OCI distribution spec, warnings use warn-code 299,
// warn-agent "-", and the format: Warning: 299 - "message"
func reportWarnings(ctx context.Context, header http.Header, handler WarningHandler) {
	if handler == nil {
		return
	}

	var warnSrc WarningSource
	if v := ctx.Value(warningSourceKey{}); v != nil {
		warnSrc = v.(WarningSource)
	}

	reportWarningsWithSource(header, handler, warnSrc)
}

func reportWarningsWithSource(header http.Header, handler WarningHandler, warnSrc WarningSource) {
	for _, warning := range header.Values("Warning") {
		// Parse RFC 7234 warning format: warn-code warn-agent "warn-text"
		// Expected format for OCI: 299 - "message"
		if text := parseWarningText(warning); text != "" {
			handler.Warn(warnSrc, text)
		}
	}
}

// parseWarningText extracts the warn-text from an RFC 7234 Warning header value.
// Expected format: 299 - "message"
// Per RFC 7230 section 3.2.6, quoted-string values may contain:
// - quoted-pair: backslash followed by any character (e.g., \", \\)
// - percent-encoding: %xx where xx is a hex value
func parseWarningText(warning string) string {
	before, text, ok := strings.Cut(warning, "299 - ")

	if strings.TrimSpace(before) != "" {
		return ""
	}

	// Not an OCI registry warning
	if !ok {
		return ""
	}

	text = strings.TrimSpace(text)

	ln := len(text)
	// Invalid warn-text (must be a quoted-string per RFC 7234)
	if ln == 0 || text[0] != '"' || text[ln-1] != '"' {
		return ""
	}

	out := strings.Builder{}
	idx := 1 // skip opening quote

	for idx < ln {
		c := text[idx]

		if c == '\\' && len(text) > idx+1 {
			nextC := text[idx+1]
			out.WriteByte(nextC)
			idx += 2
			continue
		}

		// Handle percent-encoding (%xx)
		if c == '%' && idx+2 < ln {
			decoded, err := hex.DecodeString(text[idx+1 : idx+3])
			if err == nil && len(decoded) == 1 {
				out.WriteByte(decoded[0])
				idx += 3
				continue
			}
			// Invalid percent-encoding, treat as literal
		}

		if c == '"' {
			return out.String()
		}
		out.WriteByte(c)
		idx++
	}

	// No closing quote found, invalid warning header
	return ""
}

func updateWarningSource(ctx context.Context, ref reference.Spec) context.Context {
	var warnSrc WarningSource
	if v := ctx.Value(warningSourceKey{}); v != nil {
		warnSrc = v.(WarningSource)
	}
	warnSrc.Ref = ref
	ctx = context.WithValue(ctx, warningSourceKey{}, warnSrc)
	return ctx
}
