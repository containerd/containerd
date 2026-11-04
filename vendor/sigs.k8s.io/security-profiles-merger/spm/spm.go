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

// Package spm holds the few declarations the seccomp, apparmor and landlock
// packages have in common, so that each names them once rather than three
// times:
//
//   - SliceDiff, the one diff shape all three produce.
//     seccomp.SliceDiff, landlock.RightsDiff and apparmor.StringSliceDiff
//     are alias declarations of it, so a value of one is a value of the
//     others.
//   - InputError and the sentinel errors below, which each package
//     re-exports, so that errors.Is against a sentinel here matches an
//     error any of the three returned.
//   - MaxPathLen, the path length limit of apparmor and landlock.
//   - Diff, the one method the three diff results share.
//
// Nothing needs to import this package: each profile package re-exports
// what it uses under its own name. Import it to match a sentinel without
// picking one of the three packages arbitrarily, or to hold a diff whose
// profile type was decided elsewhere.
//
// It is deliberately small. The three profile types have no common shape,
// and no operation over "a profile" can say anything before it knows which
// of the three it has, so anything beyond this belongs in the package that
// knows the type. The spm command, whose name this package shares, is the
// worked example: it dispatches over a table of per-type function values.
package spm

import (
	"errors"
	"fmt"
	"strconv"
)

var (
	// ErrNoProfiles is returned when no profiles are provided.
	ErrNoProfiles = errors.New("at least one profile is required")
	// ErrNilProfile is returned when a nil profile is provided.
	ErrNilProfile = errors.New("profile must not be nil")
	// ErrEmptyPath is returned when a path rule contains an empty string.
	ErrEmptyPath = errors.New("empty path")
	// ErrRelativePath is returned by the apparmor and landlock packages
	// when a path does not start with "/". Neither an AppArmor file rule
	// nor a Landlock path rule accepts one.
	ErrRelativePath = errors.New("relative path (must be absolute)")
	// ErrPathTooLong is returned by the apparmor and landlock packages when
	// a path is longer than MaxPathLen bytes, which is longer than any
	// Linux path. It is checked before the path is scanned, so an oversized
	// path costs no further work; only the count limits of ValidateArtifact
	// and ValidateStrict run before it.
	ErrPathTooLong = errors.New("path exceeds " + strconv.Itoa(MaxPathLen) + " bytes")
	// ErrMoreProblems stands in for the validation failures a report left
	// out. A profile holds as many failures as it holds rules, and an
	// artifact chooses how many that is, so every validator bounds what it
	// reports and matches this instead of the rest. A caller dispatching on
	// a sentinel must therefore treat a match here as "and possibly others":
	// a failure the profile holds can be absent from the error that reports
	// it.
	ErrMoreProblems = errors.New("more problems omitted")

	// ErrDuplicateKey is returned by UnmarshalStrict of every package when
	// a member occurs more than once in one JSON object, compared the way
	// encoding/json matches a member to a field, which ignores case.
	// encoding/json keeps the last of them silently while other parsers
	// keep the first, so such a profile means different things to different
	// readers.
	ErrDuplicateKey = errors.New("duplicate key")
	// ErrUnknownField is returned by UnmarshalStrict of every package when
	// the document holds a member the profile type has no field for, which
	// encoding/json drops: a misspelled member loses the rule it was meant
	// to carry, and a member of a newer format is lost in the permissive
	// direction.
	ErrUnknownField = errors.New("unknown field")
	// ErrMisspelledField is returned by UnmarshalStrict of every package
	// when a member names a field only ignoring case, such as "Syscalls"
	// or "\u017fyscalls" for "syscalls". encoding/json fills the field from
	// it, while a reader that compares names exactly drops it, so the two
	// read different rules from one profile.
	ErrMisspelledField = errors.New("misspelled field")
	// ErrMissingField is returned by UnmarshalStrict when the document
	// leaves out, or sets to null, a member that has no value standing for
	// its absence. encoding/json decodes the zero value then, which for the
	// port of a Landlock network rule is a port of its own: 0, the one a
	// socket binds to for an ephemeral port.
	ErrMissingField = errors.New("missing field")
	// ErrInvalidUTF8 is returned by UnmarshalStrict of every package when
	// the document holds a byte that is not valid UTF-8, or a \u escape
	// spelling half a surrogate pair. encoding/json replaces both with
	// U+FFFD, so documents that differ in them decode to the same profile.
	ErrInvalidUTF8 = errors.New(
		"invalid UTF-8, which the JSON decoder replaces with U+FFFD, " +
			"so distinct profiles can decode alike",
	)
	// ErrUnexpectedData is returned by UnmarshalStrict of every package
	// when the document is followed by anything but whitespace. A profile
	// is one JSON value, and a second one behind it would be dropped.
	ErrUnexpectedData = errors.New("unexpected data after the profile")
)

// InputError is returned by Intersect and Union of every package when one of
// the profiles they were given is nil or fails validation. Index is that
// profile's position among the arguments and Err what its validation
// reported.
//
// A caller merging inputs of different standing needs to know which one
// failed, not only that one did: a runtime refusing an artifact rejects a
// workload, while one refusing its own baseline has a node to fix. Match it
// with errors.As; errors.Is sees through it to the sentinels of Err.
type InputError struct {
	Index int
	Err   error
}

func (e *InputError) Error() string {
	return fmt.Sprintf("validate profile %d: %v", e.Index, e.Err)
}

func (e *InputError) Unwrap() error { return e.Err }

// MaxPathLen is the longest path the apparmor and landlock packages accept,
// in bytes: PATH_MAX, which no Linux path exceeds.
const MaxPathLen = 4096

// SliceDiff represents added and removed items in a set-like slice. It is
// what every package's slice diff is: seccomp.SliceDiff, landlock.RightsDiff
// and apparmor.StringSliceDiff all name this type.
type SliceDiff[T comparable] struct {
	Added   []T `json:"added,omitempty"`
	Removed []T `json:"removed,omitempty"`
}

// Diff is the part of a profile diff that does not depend on the profile
// type: whether the two profiles compared equal. seccomp.ProfileDiff,
// apparmor.ProfileDiff and landlock.ProfileDiff all satisfy it, by value and
// by pointer.
//
// It exists for code that holds a diff whose type was decided elsewhere, for
// example a command that picked the profile type from the input and now only
// needs the verdict. Everything else a diff carries is type-specific and
// lives on the concrete type.
type Diff interface {
	// IsEqual reports whether the two compared profiles were equal.
	IsEqual() bool
}
