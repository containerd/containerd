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

// Package strictjson finds what encoding/json accepts silently and an
// artifact must not carry: members repeated within one object, members the
// target type has no field for, members that name a field only ignoring
// case, bytes that are not valid UTF-8, and data behind the document. The
// command and the three profile packages share it, so that a library caller
// can refuse the same documents the command refuses.
package strictjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"sigs.k8s.io/security-profiles-merger/internal/merge"
)

// scanFrame is an open object or array during DuplicateKeys. It holds the
// name or index of the value it is at rather than that value's path: a path
// carries its whole ancestry, so keeping one per open frame, or building one
// per value, costs memory quadratic in a document that nests deeply or puts
// many values under a long name. A path is built from the stack only when a
// repeated member is reported.
type scanFrame struct {
	// keys holds the folded names of the members seen so far in an object,
	// mapped to whether they were already reported as repeated. It is nil
	// for an array.
	keys map[string]bool
	// member is the name of the object member whose value comes next.
	member string
	// index is the index of the next array element.
	index int
	// wantKey reports whether the next token in an object is a member name.
	wantKey bool
}

// DuplicateKeys returns the paths of object members that occur more than
// once in the same object, at any depth, in document order and each once.
// encoding/json keeps the last of them silently, while other parsers may
// keep the first, so a profile with repeated members can mean different
// things to different readers. Names are compared the way encoding/json
// matches them to struct fields, ignoring case, because a case-sensitive
// parser reads "defaultAction" and "DefaultAction" as different members
// where encoding/json fills one field from both. Every object in a profile
// decodes into a struct, so the comparison applies at every depth. raw must
// be valid JSON; the scan stops at the first syntax error.
func DuplicateKeys(raw []byte) ([]string, int) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	// Numbers are kept as text: they only need to be skipped, and a value
	// out of the float64 range must not stop the scan.
	decoder.UseNumber()

	var (
		stack []*scanFrame
		found pathCollector
	)

	for {
		token, err := decoder.Token()
		if err != nil {
			return found.paths, found.omitted
		}

		var top *scanFrame
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}

		delim, isDelim := token.(json.Delim)

		switch {
		case isClosing(delim):
			stack = stack[:len(stack)-1]
		case top.expectsKey():
			key, _ := token.(string)
			if top.addKey(key) {
				found.addLazily(func() string { return memberPath(stack) })
			}
		case isDelim:
			top.startValue()

			stack = append(stack, newScanFrame(delim == '{'))
		default:
			top.startValue()
		}
	}
}

func isClosing(delim json.Delim) bool {
	return delim == '}' || delim == ']'
}

func newScanFrame(object bool) *scanFrame {
	frame := &scanFrame{keys: nil, member: "", index: 0, wantKey: false}
	if object {
		frame.keys = map[string]bool{}
		frame.wantKey = true
	}

	return frame
}

// expectsKey reports whether the next token is a member name. A nil frame
// is the document root.
func (frame *scanFrame) expectsKey() bool {
	return frame != nil && frame.wantKey
}

// addKey records a member name and reports whether this is the first time
// the name repeats, which is when its path is collected.
func (frame *scanFrame) addKey(key string) bool {
	frame.member = key
	frame.wantKey = false

	folded := foldName(key)

	reported, seen := frame.keys[folded]
	if !seen {
		frame.keys[folded] = false

		return false
	}

	if reported {
		return false
	}

	frame.keys[folded] = true

	return true
}

// startValue advances the frame past the value that starts next. A nil frame
// is the document root.
func (frame *scanFrame) startValue() {
	if frame == nil {
		return
	}

	if frame.keys != nil {
		frame.wantKey = true

		return
	}

	frame.index++
}

// maxPathBytes is how much of a path memberPath builds before it stops. A
// path is only ever shown through merge.QuoteBounded, which cuts it at
// merge.MaxQuotedBytes, so a path past that only has to stay past it. A
// document chooses both how deep it nests and how long its names are, and
// appending every level copies the path so far: a five-megabyte document of
// five thousand levels under thousand-byte names took most of a minute to
// name the members it repeats.
const maxPathBytes = merge.MaxQuotedBytes + utf8.UTFMax

// memberPath returns the path of the member the innermost frame is at, or
// the start of it once it is longer than any report shows. Every outer
// frame is at the value holding the next one: an object at its current
// member, an array at the element before its next index.
func memberPath(stack []*scanFrame) string {
	path := ""

	for _, frame := range stack {
		if len(path) > maxPathBytes {
			break
		}

		if frame.keys != nil {
			path = joinFieldPath(path, frame.member)

			continue
		}

		path += "[" + strconv.Itoa(frame.index-1) + "]"
	}

	return path
}

// MaxReportedPaths is the number of field paths one message lists, the same
// ceiling the library puts on the failures it joins.
const MaxReportedPaths = merge.MaxJoinedErrors

// pathCollector gathers the field paths of one message, bounded the way the
// library bounds its own joined failures: past merge.MaxJoinedErrors it
// counts what it leaves out rather than keeping it. A document chooses both
// how many paths it holds and how long each one is, since a path carries its
// whole ancestry, so an unbounded collector turns a profile of a few hundred
// kilobytes into hundreds of megabytes of warning.
type pathCollector struct {
	paths   []string
	omitted int
}

func (c *pathCollector) add(path string) {
	c.addLazily(func() string { return path })
}

// addLazily is add for a path that is only worth building when it is kept.
func (c *pathCollector) addLazily(path func() string) {
	if len(c.paths) >= MaxReportedPaths {
		c.omitted++

		return
	}

	c.paths = append(c.paths, path())
}

// foldName returns the name in the case-folded form encoding/json uses to
// match a member to a struct field.
func foldName(name string) string {
	var builder strings.Builder

	builder.Grow(len(name))

	for _, char := range name {
		if char < utf8.RuneSelf {
			builder.WriteString(strings.ToUpper(string(char)))

			continue
		}

		builder.WriteRune(foldRune(char))
	}

	return builder.String()
}

// foldRune returns the smallest rune in the simple case folding orbit of
// char, as encoding/json does.
func foldRune(char rune) rune {
	for {
		next := unicode.SimpleFold(char)
		if next <= char {
			return next
		}

		char = next
	}
}

// fieldPathsError wraps kind with the quoted paths, pluralizing the kind
// when there are several, as in `unknown fields "a", "b"`. Each path is
// quoted with a length bound and omitted counts the ones the collector left
// out, so that the message a runtime logs is bounded however the document
// that produced it was shaped.
func fieldPathsError(kind error, paths []string, omitted int) error {
	quoted := make([]string, len(paths))
	for idx, field := range paths {
		quoted[idx] = merge.QuoteBounded(field)
	}

	if len(paths) == 1 && omitted == 0 {
		return fmt.Errorf("%w %s", kind, quoted[0])
	}

	listed := strings.Join(quoted, ", ")
	if omitted > 0 {
		listed = fmt.Sprintf("%s and %d more", listed, omitted)
	}

	return fmt.Errorf("%ws %s", kind, listed)
}
