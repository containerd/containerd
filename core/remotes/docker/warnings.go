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

	"github.com/containerd/log"
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
// The src parameter contains information about the request that the warning is
// for. If the warning is for specific content, its descriptor is available via
// src.Desc. src.Desc is nil if the warning is not for a specific content.
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
	if v, ok := ctx.Value(warningSourceKey{}).(WarningSource); ok {
		warnSrc = v
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
	text, ok := strings.CutPrefix(warning, "299 - ")
	if !ok {
		return ""
	}

	text = strings.TrimSpace(text)

	ln := len(text)
	if ln == 0 || text[0] != '"' || text[ln-1] != '"' {
		return ""
	}

	out := strings.Builder{}
	idx := 1 // skip opening quote
	end := ln - 1

	for idx < end {
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

		if !isQdtext(c) {
			return ""
		}
		out.WriteByte(c)
		idx++
	}

	return out.String()
}

func updateWarningSource(ctx context.Context, req *request) context.Context {
	url := req.host.Scheme + "://" + req.host.Host + req.path
	var warnSrc WarningSource
	if v, ok := ctx.Value(warningSourceKey{}).(WarningSource); ok {
		warnSrc = v
	}
	warnSrc.URL = url
	ctx = context.WithValue(ctx, warningSourceKey{}, warnSrc)
	return ctx
}

// isQdtext returns whether c is valid unescaped inside a quoted-string per RFC 7230 §3.2.6.
//
//	qdtext = HTAB / SP / %x21 / %x23-5B / %x5D-7E / %x80-FF
func isQdtext(c byte) bool {
	if c == 0x09 || c == 0x20 { // HTAB, SP
		return true
	}
	if c == 0x21 { // '!'
		return true
	}
	if c >= 0x23 && c <= 0x5B { // '#' to '['
		return true
	}
	if c >= 0x5D && c <= 0x7E { // ']' to '~'
		return true
	}
	if c >= 0x80 { // obs-text
		return true
	}
	return false
}

// isQuotedPairChar returns whether c is valid after a backslash per RFC 7230 §3.2.6.
//
//	quoted-pair = "\" ( HTAB / SP / VCHAR / %x80-FF )
//	VCHAR = %x21-7E
func isQuotedPairChar(c byte) bool {
	if c == 0x09 || c == 0x20 { // HTAB, SP
		return true
	}
	if c >= 0x21 && c <= 0x7E { // VCHAR
		return true
	}
	if c >= 0x80 { // obs-text
		return true
	}
	return false
}

// LogWarningHandler is a WarningHandler that logs warnings using the
// containerd log package.
type LogWarningHandler struct{}

// Warn logs the warning message with source information.
func (LogWarningHandler) Warn(src WarningSource, warning string) {
	fields := log.Fields{}
	if ref := src.Ref.String(); ref != "" {
		fields["ref"] = ref
	}
	if src.Digest != nil {
		fields["digest"] = src.Digest.String()
	}
	log.L.WithFields(fields).Warn("registry warning: " + warning)
}
