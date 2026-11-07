/*
Copyright The Kubernetes Authors.

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

package merge

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"sigs.k8s.io/security-profiles-merger/spm"
)

// MaxQuotedBytes bounds how much of a caller-supplied value QuoteBounded
// renders. A validation error names the input that failed, and for an
// artifact that input is attacker-controlled and unbounded, so a runtime
// logging the error would otherwise write the artifact back out at whatever
// size its author chose.
const MaxQuotedBytes = 64

// MaxJoinedErrors bounds how many errors JoinLimited reports individually.
// Validation collects every failure, and a profile can hold as many failures
// as it holds entries, so the count needs a ceiling for the same reason the
// individual values do.
const MaxJoinedErrors = 32

// QuoteBounded renders value as a double-quoted Go string, truncated to
// MaxQuotedBytes on a rune boundary. A truncated value is followed by an
// ellipsis outside the quotes, so the elision is never mistaken for part of
// the value. Use it instead of %q wherever the value comes from a profile
// rather than from this package.
func QuoteBounded(value string) string {
	if len(value) <= MaxQuotedBytes {
		return strconv.Quote(value)
	}

	return strconv.Quote(value[:runeBoundary(value, MaxQuotedBytes)]) + "..."
}

// runeBoundary returns the largest index up to limit at which text can be
// cut without splitting a UTF-8 sequence. A sequence is at most utf8.UTFMax
// bytes long, so the cut backs off by less than that: text that holds no
// rune start within reach is not valid UTF-8, where there is no sequence to
// keep whole and backing off further would cut away what is to be shown.
func runeBoundary(text string, limit int) int {
	for back := range utf8.UTFMax {
		if utf8.RuneStart(text[limit-back]) {
			return limit - back
		}
	}

	return limit
}

// MaxMessageBytes bounds a message built from text the caller did not
// write, such as the literal a JSON decoder quotes back from the document
// it refused.
const MaxMessageBytes = 512

// BoundedText truncates text to MaxMessageBytes on a rune boundary, marking
// the elision so that it is never mistaken for the text. A decoder quotes
// the literal it refused, and a document chooses how long that is: a
// megabyte-long number otherwise reaches a runtime's log as a megabyte of
// error.
func BoundedText(text string) string {
	if len(text) <= MaxMessageBytes {
		return text
	}

	return text[:runeBoundary(text, MaxMessageBytes)] + "..."
}

// BoundedError returns err with its message bounded by BoundedText. errors.Is
// and errors.As still see err itself, so a caller matching a decoder's error
// type is unaffected. It returns nil for a nil err.
func BoundedError(err error) error {
	if err == nil {
		return nil
	}

	return &boundedError{err: err}
}

// boundedError is the result of BoundedError.
type boundedError struct {
	err error
}

func (b *boundedError) Error() string { return BoundedText(b.err.Error()) }

func (b *boundedError) Unwrap() error { return b.err }

// JoinLimited joins up to MaxJoinedErrors non-nil errors, following them with
// a count of the ones it left out. It returns nil when every error is nil and
// the error itself when exactly one is non-nil, matching errors.Join.
//
// A result of JoinLimited given as one of the errors is flattened into the
// list rather than counted as one: validators build their report from the
// reports of their checks, and the bound has to hold for the whole of it,
// not once per check.
//
// The omitted errors stay omitted: a caller that needs to match one with
// errors.Is must not rely on a failure past the limit being present. A
// truncated result matches spm.ErrMoreProblems, so the truncation itself is
// visible rather than silent.
func JoinLimited(errs ...error) error {
	var report limitedError

	for _, err := range errs {
		report.add(err)
	}

	switch {
	case len(report.kept) == 0:
		return nil
	case len(report.kept) == 1 && report.omitted == 0:
		return report.kept[0]
	default:
		return &report
	}
}

// limitedError is the result of JoinLimited: the failures it kept and how
// many it left out.
type limitedError struct {
	kept    []error
	omitted int
}

// Error lists the kept failures one per line, as errors.Join does, followed
// by the count of the omitted ones.
func (l *limitedError) Error() string {
	lines := make([]string, 0, len(l.kept)+1)
	for _, err := range l.kept {
		lines = append(lines, err.Error())
	}

	if l.omitted > 0 {
		lines = append(lines, fmt.Sprintf("%d %v", l.omitted, spm.ErrMoreProblems))
	}

	return strings.Join(lines, "\n")
}

// Unwrap returns the kept failures, and spm.ErrMoreProblems when any were left
// out, so that errors.Is sees both.
func (l *limitedError) Unwrap() []error {
	if l.omitted == 0 {
		return l.kept
	}

	return append(slices.Clone(l.kept), spm.ErrMoreProblems)
}

func (l *limitedError) add(err error) {
	if err == nil {
		return
	}

	//nolint:errorlint // only a report itself is flattened, not one wrapped
	if nested, ok := err.(*limitedError); ok {
		for _, kept := range nested.kept {
			l.add(kept)
		}

		l.omitted += nested.omitted

		return
	}

	if len(l.kept) < MaxJoinedErrors {
		l.kept = append(l.kept, err)

		return
	}

	l.omitted++
}
