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
	"net"
	"net/http"
	"strings"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// WarningSource contains the information about the request that caused the warning.
type WarningSource struct {
	// URL is the URL of the request that caused the warning.
	URL string

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
		nextC := byte(0)
		if len(text) > idx+1 {
			nextC = text[idx+1]
		}

		// Check for escaped quote
		if c == '\\' && nextC == '"' {
			out.WriteByte('"')
			idx += 2
			continue
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

func updateWarningSource(ctx context.Context, req *request) context.Context {
	url := req.host.Scheme + "://" + req.host.Host + req.path
	var warnSrc WarningSource
	if v := ctx.Value(warningSourceKey{}); v != nil {
		warnSrc = v.(WarningSource)
	}
	warnSrc.URL = url
	ctx = context.WithValue(ctx, warningSourceKey{}, warnSrc)
	return ctx
}
