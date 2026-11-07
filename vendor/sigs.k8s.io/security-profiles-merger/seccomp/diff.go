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

package seccomp

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"

	"sigs.k8s.io/security-profiles-merger/internal/merge"
	"sigs.k8s.io/security-profiles-merger/spm"
)

// ProfileDiff describes the differences between two seccomp profiles.
type ProfileDiff struct {
	// Equal is true when the two profiles load the same filter, compared in
	// the normalized form Diff describes, even where they are spelled
	// differently.
	Equal bool `json:"equal"`

	// DefaultAction is set when the default actions differ.
	DefaultAction *ActionDiff `json:"defaultAction,omitempty"`

	// DefaultErrnoRet is set when the default errno return values differ.
	DefaultErrnoRet *UintPtrDiff `json:"defaultErrnoRet,omitempty"`

	// Architectures is set when the architecture lists differ.
	Architectures *SliceDiff[specs.Arch] `json:"architectures,omitempty"`

	// Flags is set when the flag lists differ.
	Flags *SliceDiff[specs.LinuxSeccompFlag] `json:"flags,omitempty"`

	// ListenerPath is set when the listener paths differ.
	ListenerPath *StringDiff `json:"listenerPath,omitempty"`

	// ListenerMetadata is set when the listener metadata differs.
	ListenerMetadata *StringDiff `json:"listenerMetadata,omitempty"`

	// Syscalls is set when the syscall entries differ.
	Syscalls *SyscallsDiff `json:"syscalls,omitempty"`
}

// IsEqual reports Equal: whether the two profiles compare equal in the
// normalized form Diff describes.
func (d ProfileDiff) IsEqual() bool { return d.Equal }

// ActionDiff represents a change in seccomp action.
type ActionDiff struct {
	Left  specs.LinuxSeccompAction `json:"left"`
	Right specs.LinuxSeccompAction `json:"right"`
}

// UintPtrDiff represents a change in an optional uint value.
type UintPtrDiff struct {
	Left  *uint `json:"left"`
	Right *uint `json:"right"`
}

// StringDiff represents a change in a string value.
type StringDiff struct {
	Left  string `json:"left"`
	Right string `json:"right"`
}

// SliceDiff represents added and removed items in a set-like slice. It is
// [spm.SliceDiff], which the apparmor and landlock diffs name as well.
type SliceDiff[T comparable] = spm.SliceDiff[T]

// SyscallsDiff describes differences in the syscall entries.
type SyscallsDiff struct {
	Added   []SyscallEntry  `json:"added,omitempty"`
	Removed []SyscallEntry  `json:"removed,omitempty"`
	Changed []SyscallChange `json:"changed,omitempty"`
}

// SyscallEntry represents a single syscall with its action and arguments.
type SyscallEntry struct {
	Name     string                   `json:"name"`
	Action   specs.LinuxSeccompAction `json:"action"`
	ErrnoRet *uint                    `json:"errnoRet,omitempty"`
	Args     []specs.LinuxSeccompArg  `json:"args,omitempty"`
}

// SyscallChange represents a syscall present in both profiles with differences.
type SyscallChange struct {
	Name  string          `json:"name"`
	Left  []SyscallDetail `json:"left"`
	Right []SyscallDetail `json:"right"`
}

// SyscallDetail holds the action, errno, and args of a syscall entry.
type SyscallDetail struct {
	Action   specs.LinuxSeccompAction `json:"action"`
	ErrnoRet *uint                    `json:"errnoRet,omitempty"`
	Args     []specs.LinuxSeccompArg  `json:"args,omitempty"`
}

// Diff compares two seccomp profiles by the rules a runtime loads from them
// and returns a structured diff. Unlike Intersect and Union, it does not
// validate the profiles. It returns ErrNilProfile if either profile is nil.
//
// Entries are normalized as a merge reads them: entries equal to the
// profile default are ignored, an unconditional entry hides the conditional
// entries for its syscall, conditions compare as libseccomp evaluates them,
// exact duplicates are dropped, and errno values and SCMP_ACT_KILL_THREAD
// are canonicalized. Rules are not rewritten beyond that, so
// Diff(p, Intersect(p)) reports exactly what Intersect settled. Diff does
// not check whether libseccomp accepts the rules: two profiles holding the
// same rules in a different order compare equal although a runtime may
// load only one of them. A syscall past the read budget is compared by the
// entries that load it rather than by its rules.
//
// Architectures are compared with the architecture of the running program
// (see NativeArchitecture) implied on both sides, so a profile destined for
// another architecture compares differently here than on the target node;
// use DiffForArch to name that architecture. See the Diff section of the
// package documentation.
func Diff(left, right *specs.LinuxSeccomp) (*ProfileDiff, error) {
	native, ok := NativeArchitecture()
	if !ok {
		// No architecture is implied, so every listed one is compared.
		// Only a GOARCH with no seccomp architecture constant gets here,
		// which no platform this module builds for has.
		native = ""
	}

	return DiffForArch(native, left, right)
}

// DiffForArch compares two seccomp profiles as a node running the given
// native architecture would load them, and is otherwise identical to Diff.
// Runtimes always cover the native architecture whether or not a profile
// lists it, so it is implied on both sides and never reported as a
// difference. Pass the empty Arch to imply none, which compares the
// architecture lists as written.
//
// Use this rather than Diff whenever the profiles are destined for a node
// that may not run the same architecture as the caller, as a control plane
// comparing profiles for a mixed-architecture cluster does.
func DiffForArch(
	native specs.Arch, left, right *specs.LinuxSeccomp,
) (*ProfileDiff, error) {
	if left == nil || right == nil {
		return nil, ErrNilProfile
	}

	diff := &ProfileDiff{
		Equal:            true,
		DefaultAction:    nil,
		DefaultErrnoRet:  nil,
		Architectures:    nil,
		Flags:            nil,
		ListenerPath:     nil,
		ListenerMetadata: nil,
		Syscalls:         nil,
	}

	diffDefaultAction(diff, left, right)
	diffDefaultErrnoRet(diff, left, right)
	diffArchitectures(diff, native, left, right)
	diffFlags(diff, left, right)
	diffListener(diff, left, right)
	diffSyscallEntries(diff, left, right)

	return diff, nil
}

// DiffSyscalls compares two bare syscall slices and returns the syscall
// portion of a profile diff, or nil when they are equal. Entries are
// compared as described for Diff, except that without a profile default no
// entry is ignored for equaling it. Multi-name entries are normalized to
// one-name-per-entry and argument filters are sorted before comparison, so
// entries differing only in filter order compare equal. This is the
// syscall-slice analogue of Diff, matching IntersectSyscalls and
// UnionSyscalls.
//
// This function does not validate its inputs.
func DiffSyscalls(left, right []specs.LinuxSyscall) *SyscallsDiff {
	return diffSyscallLists(left, right, nil, nil)
}

// diffSyscallLists compares two syscall lists by the rules a runtime loads
// from them and returns nil when they are equal. leftDef and rightDef are
// the profile defaults, or nil for bare lists.
//
// A syscall read in summarized form on either side (see collectRules) is
// listed by the two clauses a collapse could pick rather than by its rules,
// and two different rule sets can share those. Such a syscall is compared by
// the entries that load its rules instead, which costs one pass over the
// lists: it is equal only where both sides load the same rules from entries
// in the same order.
func diffSyscallLists(left, right []specs.LinuxSyscall, leftDef, rightDef *clause) *SyscallsDiff {
	summarized := summarizedNames(left, leftDef)
	for name := range summarizedNames(right, rightDef) {
		if summarized == nil {
			summarized = make(map[string]bool)
		}

		summarized[name] = true
	}

	var leftLoaded, rightLoaded map[string][]string
	if len(summarized) > 0 {
		leftLoaded = loadedEntryKeys(left, leftDef, summarized)
		rightLoaded = loadedEntryKeys(right, rightDef, summarized)
	}

	return diffSyscallMaps(
		buildSyscallMap(left, leftDef), buildSyscallMap(right, rightDef),
		func(name string) (bool, bool) {
			if !summarized[name] {
				return false, false
			}

			return true, slices.Equal(leftLoaded[name], rightLoaded[name])
		},
	)
}

// loadedEntryKeys returns, for every syscall in only, a key per entry that
// loads rules for it, in entry order and without exact repeats, which add
// nothing at load time. Two lists with the same keys for a syscall load the
// same rules for it in the same order.
func loadedEntryKeys(
	syscalls []specs.LinuxSyscall, def *clause, only map[string]bool,
) map[string][]string {
	keys := make(map[string][]string)
	seen := make(map[string]map[string]struct{})

	for idx := range syscalls {
		entry := &syscalls[idx]
		if !slices.ContainsFunc(entry.Names, func(name string) bool { return only[name] }) {
			continue
		}

		key := loadedEntryKey(entry, def)
		if key == "" {
			continue
		}

		for _, name := range entry.Names {
			if !only[name] {
				continue
			}

			byKey, ok := seen[name]
			if !ok {
				byKey = make(map[string]struct{})
				seen[name] = byKey
			}

			if _, dup := byKey[key]; dup {
				continue
			}

			byKey[key] = struct{}{}
			keys[name] = append(keys[name], key)
		}
	}

	return keys
}

// loadedEntryKey formats the rules one entry loads for each of its names,
// or returns "" when it loads none. Every clause key is prefixed with its
// length, since Diff does not validate and an action may hold any byte.
func loadedEntryKey(entry *specs.LinuxSyscall, def *clause) string {
	var builder strings.Builder

	for _, next := range entryClauses(entry) {
		if def != nil && next.sameResult(*def) {
			continue
		}

		key := clauseKey(next)
		builder.WriteString(strconv.Itoa(len(key)))
		builder.WriteByte(':')
		builder.WriteString(key)
	}

	return builder.String()
}

// diffSyscallMaps compares two per-name entry maps and returns nil when
// they are equal. decided reports for a syscall whether its entries are
// compared some other way, and if so whether they are equal.
func diffSyscallMaps(
	leftMap, rightMap map[string][]SyscallEntry,
	decided func(name string) (bool, bool),
) *SyscallsDiff {
	var result SyscallsDiff

	leftNames := slices.Sorted(maps.Keys(leftMap))
	collectRemovedSyscalls(&result, leftNames, leftMap, rightMap)
	collectAddedSyscalls(&result, slices.Sorted(maps.Keys(rightMap)), leftMap, rightMap)
	collectChangedSyscalls(&result, leftNames, leftMap, rightMap, decided)

	if len(result.Added) == 0 &&
		len(result.Removed) == 0 &&
		len(result.Changed) == 0 {
		return nil
	}

	return &result
}

func diffDefaultAction(
	diff *ProfileDiff, left, right *specs.LinuxSeccomp,
) {
	leftAction := defaultClause(left).action
	rightAction := defaultClause(right).action

	if leftAction != rightAction {
		diff.Equal = false
		diff.DefaultAction = &ActionDiff{
			Left:  leftAction,
			Right: rightAction,
		}
	}
}

func diffDefaultErrnoRet(
	diff *ProfileDiff, left, right *specs.LinuxSeccomp,
) {
	leftRet := outputErrno(left.DefaultAction, left.DefaultErrnoRet)
	rightRet := outputErrno(right.DefaultAction, right.DefaultErrnoRet)

	if !equalUintPtr(leftRet, rightRet) {
		diff.Equal = false
		diff.DefaultErrnoRet = &UintPtrDiff{
			Left:  leftRet,
			Right: rightRet,
		}
	}
}

func equalUintPtr(first, second *uint) bool {
	if first == nil && second == nil {
		return true
	}

	if first == nil || second == nil {
		return false
	}

	return *first == *second
}

// diffArchitectures compares the architecture lists with the given native
// architecture implied on both sides, as runtimes always cover it: a
// profile that lists it and one that does not are the same to the runtime.
func diffArchitectures(
	diff *ProfileDiff, native specs.Arch, left, right *specs.LinuxSeccomp,
) {
	added, removed := merge.DiffSlice(
		withoutNative(native, left.Architectures),
		withoutNative(native, right.Architectures),
	)
	if len(added) > 0 || len(removed) > 0 {
		diff.Equal = false
		diff.Architectures = &SliceDiff[specs.Arch]{Added: added, Removed: removed}
	}
}

func withoutNative(native specs.Arch, archs []specs.Arch) []specs.Arch {
	if native == "" {
		return archs
	}

	return slices.DeleteFunc(slices.Clone(archs), func(arch specs.Arch) bool {
		return arch == native
	})
}

func diffFlags(
	diff *ProfileDiff, left, right *specs.LinuxSeccomp,
) {
	added, removed := merge.DiffSlice(loadedFlags(left.Flags), loadedFlags(right.Flags))
	if len(added) > 0 || len(removed) > 0 {
		diff.Equal = false
		diff.Flags = &SliceDiff[specs.LinuxSeccompFlag]{Added: added, Removed: removed}
	}
}

func diffListener(
	diff *ProfileDiff, left, right *specs.LinuxSeccomp,
) {
	if left.ListenerPath != right.ListenerPath {
		diff.Equal = false
		diff.ListenerPath = &StringDiff{
			Left:  left.ListenerPath,
			Right: right.ListenerPath,
		}
	}

	if left.ListenerMetadata != right.ListenerMetadata {
		diff.Equal = false
		diff.ListenerMetadata = &StringDiff{
			Left:  left.ListenerMetadata,
			Right: right.ListenerMetadata,
		}
	}
}

func diffSyscallEntries(
	diff *ProfileDiff, left, right *specs.LinuxSeccomp,
) {
	syscallsDiff := diffSyscallLists(
		left.Syscalls, right.Syscalls, defaultClause(left), defaultClause(right),
	)
	if syscallsDiff != nil {
		diff.Equal = false
		diff.Syscalls = syscallsDiff
	}
}

func collectRemovedSyscalls(
	syscallsDiff *SyscallsDiff,
	names []string,
	leftMap, rightMap map[string][]SyscallEntry,
) {
	for _, name := range names {
		if _, ok := rightMap[name]; !ok {
			syscallsDiff.Removed = append(syscallsDiff.Removed, leftMap[name]...)
		}
	}
}

func collectAddedSyscalls(
	syscallsDiff *SyscallsDiff,
	names []string,
	leftMap, rightMap map[string][]SyscallEntry,
) {
	for _, name := range names {
		if _, ok := leftMap[name]; !ok {
			syscallsDiff.Added = append(syscallsDiff.Added, rightMap[name]...)
		}
	}
}

func collectChangedSyscalls(
	syscallsDiff *SyscallsDiff,
	names []string,
	leftMap, rightMap map[string][]SyscallEntry,
	decided func(name string) (bool, bool),
) {
	for _, name := range names {
		leftEntries := leftMap[name]

		rightEntries, ok := rightMap[name]
		if !ok {
			continue
		}

		var equal bool
		if done, same := decided(name); done {
			equal = same
		} else {
			equal = equalSyscallEntrySlices(leftEntries, rightEntries)
		}

		if !equal {
			syscallsDiff.Changed = append(syscallsDiff.Changed, SyscallChange{
				Name:  name,
				Left:  entriesToDetails(leftEntries),
				Right: entriesToDetails(rightEntries),
			})
		}
	}
}

// buildSyscallMap expands syscall entries into the per-name entries a
// runtime loads from them, in the normal form the merge produces. def is
// the profile default, or nil for bare syscall lists.
func buildSyscallMap(
	syscalls []specs.LinuxSyscall, def *clause,
) map[string][]SyscallEntry {
	result := make(map[string][]SyscallEntry)
	// Deduplication by key rather than by scanning what a name already
	// holds, so that a syscall carrying many entries stays linear. Nothing
	// reaches it today, since collectRules drops exact duplicates and an
	// unconditional rule hides the conditional ones, but the diff must not
	// list an entry twice if that ever changes.
	seen := make(map[string]map[string]struct{})

	for _, syscall := range settledSyscalls(nil, syscalls, def) {
		for _, name := range syscall.Names {
			entry := SyscallEntry{
				Name:     name,
				Action:   syscall.Action,
				ErrnoRet: syscall.ErrnoRet,
				Args:     syscall.Args,
			}

			byKey, ok := seen[name]
			if !ok {
				byKey = make(map[string]struct{})
				seen[name] = byKey
			}

			key := syscallEntryKey(entry)
			if _, dup := byKey[key]; dup {
				continue
			}

			byKey[key] = struct{}{}

			result[name] = append(result[name], entry)
		}
	}

	return result
}

// syscallEntryKey formats the fields equalSyscallEntry compares, so that
// entries compare equal exactly when their keys do.
func syscallEntryKey(entry SyscallEntry) string {
	return resultKey(entry.Action, entry.ErrnoRet, sortedArgsKey(entry.Args))
}

func entriesToDetails(entries []SyscallEntry) []SyscallDetail {
	details := make([]SyscallDetail, 0, len(entries))

	for _, entry := range entries {
		details = append(details, SyscallDetail{
			Action:   entry.Action,
			ErrnoRet: entry.ErrnoRet,
			Args:     entry.Args,
		})
	}

	return details
}

func equalSyscallEntrySlices(left, right []SyscallEntry) bool {
	if len(left) != len(right) {
		return false
	}

	left = slices.Clone(left)
	right = slices.Clone(right)

	sortSyscallEntries(left)
	sortSyscallEntries(right)

	for idx := range left {
		if !equalSyscallEntry(left[idx], right[idx]) {
			return false
		}
	}

	return true
}

func sortSyscallEntries(entries []SyscallEntry) {
	slices.SortFunc(entries, func(left, right SyscallEntry) int {
		if result := cmp.Compare(left.Action, right.Action); result != 0 {
			return result
		}

		if result := compareUintPtr(left.ErrnoRet, right.ErrnoRet); result != 0 {
			return result
		}

		return compareSyscallArgs(left.Args, right.Args)
	})
}

func compareUintPtr(left, right *uint) int {
	if left == nil && right == nil {
		return 0
	}

	if left == nil {
		return -1
	}

	if right == nil {
		return 1
	}

	return cmp.Compare(*left, *right)
}

func compareSyscallArgs(left, right []specs.LinuxSeccompArg) int {
	return slices.CompareFunc(left, right, compareArg)
}

func equalSyscallEntry(first, second SyscallEntry) bool {
	if first.Action != second.Action {
		return false
	}

	if !equalUintPtr(first.ErrnoRet, second.ErrnoRet) {
		return false
	}

	return slices.Equal(first.Args, second.Args)
}

// FormatDiff returns a human-readable representation of a seccomp profile diff.
func FormatDiff(diff *ProfileDiff) string {
	if diff == nil {
		return "Diff{<nil>}"
	}

	if diff.Equal {
		return "Diff{equal}"
	}

	var parts []string

	parts = appendScalarDiffs(parts, diff)
	parts = appendSliceDiffs(parts, diff)
	parts = appendListenerDiffs(parts, diff)

	if diff.Syscalls != nil {
		parts = append(parts, formatSyscallsDiff(diff.Syscalls)...)
	}

	return fmt.Sprintf("Diff{%s}", strings.Join(parts, " "))
}

func appendScalarDiffs(parts []string, diff *ProfileDiff) []string {
	if diff.DefaultAction != nil {
		parts = append(parts, fmt.Sprintf(
			"default:%s->%s",
			merge.SafeText(string(diff.DefaultAction.Left)),
			merge.SafeText(string(diff.DefaultAction.Right)),
		))
	}

	if diff.DefaultErrnoRet != nil {
		parts = append(parts, fmt.Sprintf(
			"defaultErrno:%s->%s",
			formatUintPtr(diff.DefaultErrnoRet.Left),
			formatUintPtr(diff.DefaultErrnoRet.Right),
		))
	}

	return parts
}

func appendSliceDiffs(parts []string, diff *ProfileDiff) []string {
	if diff.Architectures != nil {
		parts = append(parts, formatSliceDiff("arch", diff.Architectures))
	}

	if diff.Flags != nil {
		parts = append(parts, formatSliceDiff("flags", diff.Flags))
	}

	return parts
}

func appendListenerDiffs(parts []string, diff *ProfileDiff) []string {
	if diff.ListenerPath != nil {
		parts = append(parts, fmt.Sprintf(
			"listener:%s->%s",
			formatQuotedOrNone(diff.ListenerPath.Left),
			formatQuotedOrNone(diff.ListenerPath.Right),
		))
	}

	if diff.ListenerMetadata != nil {
		parts = append(parts, fmt.Sprintf(
			"listenerMeta:%s->%s",
			formatQuotedOrNone(diff.ListenerMetadata.Left),
			formatQuotedOrNone(diff.ListenerMetadata.Right),
		))
	}

	return parts
}

func formatUintPtr(val *uint) string {
	if val == nil {
		return "<nil>"
	}

	return strconv.FormatUint(uint64(*val), 10)
}

func formatQuotedOrNone(str string) string {
	if str == "" {
		return "<none>"
	}

	return merge.SafeText(str)
}

func formatSliceDiff[T ~string](prefix string, sliceDiff *SliceDiff[T]) string {
	return merge.FormatSliceDiff(prefix, *sliceDiff)
}

func formatSyscallsDiff(syscallsDiff *SyscallsDiff) []string {
	parts := make([]string, 0,
		len(syscallsDiff.Removed)+len(syscallsDiff.Added)+len(syscallsDiff.Changed),
	)

	for _, entry := range syscallsDiff.Removed {
		parts = append(parts, "-"+entry.String())
	}

	for _, entry := range syscallsDiff.Added {
		parts = append(parts, "+"+entry.String())
	}

	for _, change := range syscallsDiff.Changed {
		parts = append(parts, fmt.Sprintf(
			"~%s:%s->%s",
			merge.SafeText(change.Name),
			formatDetailActions(change.Left),
			formatDetailActions(change.Right),
		))
	}

	return parts
}

func formatDetailActions(details []SyscallDetail) string {
	actions := make([]string, 0, len(details))

	for _, detail := range details {
		actions = append(actions, detail.String())
	}

	return strings.Join(actions, ",")
}
