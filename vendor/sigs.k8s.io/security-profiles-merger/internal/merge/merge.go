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

// Package merge holds what the three profile packages and the command share
// beyond the public spm package: the generic merge primitives (Fold and the
// slice helpers), the bounds on error reports (JoinLimited, BoundedError,
// QuoteBounded) and the quoting of profile values for output (SafeText,
// SafeName).
package merge

import (
	"cmp"
	"path"
	"slices"
	"strings"

	"sigs.k8s.io/security-profiles-merger/spm"
)

// Fold merges a slice of profiles using pairwise reduction. A single profile
// is cloned; two or more are merged left to right. Profiles must be non-nil:
// callers validate them, reporting ErrNilProfile with the index, before
// folding.
func Fold[T any](
	profiles []*T,
	clone func(*T) *T,
	mergeFn func(*T, *T) (*T, error),
) (*T, error) {
	if len(profiles) == 0 {
		return nil, spm.ErrNoProfiles
	}

	if len(profiles) == 1 {
		return clone(profiles[0]), nil
	}

	result, err := mergeFn(profiles[0], profiles[1])
	if err != nil {
		return nil, err
	}

	for idx := 2; idx < len(profiles); idx++ {
		result, err = mergeFn(result, profiles[idx])
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}

// FormatSliceDiff formats a SliceDiff as a prefixed diff string, listing the
// removed items with a "-" and then the added items with a "+", as in
// "caps:-CHOWN,+KILL". An item holding bytes that are not safe to print is
// quoted (see SafeText): a diff is computed over profiles nothing validated,
// and its rendering is what a runtime logs.
func FormatSliceDiff[T ~string](prefix string, diff spm.SliceDiff[T]) string {
	items := make([]string, 0, len(diff.Removed)+len(diff.Added))

	for _, r := range diff.Removed {
		items = append(items, "-"+SafeText(string(r)))
	}

	for _, a := range diff.Added {
		items = append(items, "+"+SafeText(string(a)))
	}

	return prefix + ":" + strings.Join(items, ",")
}

// DiffSlice compares two slices as sets and returns, in this order, the
// elements only right has (added) and the elements only left has (removed).
// Duplicates within a slice are ignored. Results are sorted, which is why T
// must be ordered. Returns nil, nil when the sets are equal.
func DiffSlice[T cmp.Ordered](left, right []T) ([]T, []T) {
	if len(left) == 0 && len(right) == 0 {
		return nil, nil
	}

	leftSet := make(map[T]struct{}, len(left))
	for _, item := range left {
		leftSet[item] = struct{}{}
	}

	rightSet := make(map[T]struct{}, len(right))
	for _, item := range right {
		rightSet[item] = struct{}{}
	}

	var added, removed []T

	for item := range leftSet {
		if _, ok := rightSet[item]; !ok {
			removed = append(removed, item)
		}
	}

	for item := range rightSet {
		if _, ok := leftSet[item]; !ok {
			added = append(added, item)
		}
	}

	slices.Sort(added)
	slices.Sort(removed)

	return added, removed
}

// IntersectSlice returns the elements present in both left and right, each
// once, in the order left holds them.
func IntersectSlice[T comparable](left, right []T) []T {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}

	rightSet := make(map[T]struct{}, len(right))
	for _, val := range right {
		rightSet[val] = struct{}{}
	}

	result := make([]T, 0, min(len(left), len(right)))
	seen := make(map[T]struct{}, len(left))

	for _, val := range left {
		if _, ok := rightSet[val]; !ok {
			continue
		}

		if _, dup := seen[val]; !dup {
			seen[val] = struct{}{}
			result = append(result, val)
		}
	}

	return result
}

// UnionSlice returns the elements of left and then of right, each once.
//
// The lists it is given are rights, architectures and flags: a handful of
// elements, merged once per rule and per ancestor of a rule, so it is the
// allocations that cost rather than the comparisons. A linear scan of the
// result needs no map, and past a few elements the map takes over so that a
// list a profile chose the length of stays linear.
func UnionSlice[T comparable](left, right []T) []T {
	if len(left)+len(right) == 0 {
		return nil
	}

	if len(left)+len(right) > smallUnion {
		return DeduplicateSlice(slices.Concat(left, right))
	}

	result := make([]T, 0, len(left)+len(right))

	for _, list := range [2][]T{left, right} {
		for _, val := range list {
			if !slices.Contains(result, val) {
				result = append(result, val)
			}
		}
	}

	return result
}

// smallUnion is the combined length up to which UnionSlice scans instead of
// hashing.
const smallUnion = 16

// IsAbsPath reports whether a profile path starts at the root. Profile paths
// are Linux paths whatever the host is, so this uses slash semantics rather
// than the host's path separator.
func IsAbsPath(profilePath string) bool {
	return path.IsAbs(profilePath)
}

// ClonePtr returns a shallow copy of the pointed-to value, or nil if ptr is nil.
func ClonePtr[T any](ptr *T) *T {
	if ptr == nil {
		return nil
	}

	val := *ptr

	return &val
}

// DeduplicateSlice returns a new slice with duplicate elements removed,
// preserving the order of first occurrence. The result never shares a backing
// array with items: an empty input yields nil rather than the caller's slice,
// so appending to the result cannot write into the caller's array.
func DeduplicateSlice[T comparable](items []T) []T {
	if len(items) == 0 {
		return nil
	}

	seen := make(map[T]struct{}, len(items))
	result := make([]T, 0, len(items))

	for _, item := range items {
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			result = append(result, item)
		}
	}

	return result
}
