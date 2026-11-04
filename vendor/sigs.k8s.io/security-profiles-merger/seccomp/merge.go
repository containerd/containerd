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
	"errors"
	"fmt"
	"maps"
	"slices"

	specs "github.com/opencontainers/runtime-spec/specs-go"

	"sigs.k8s.io/security-profiles-merger/internal/merge"
	"sigs.k8s.io/security-profiles-merger/spm"
)

var (
	// ErrNoProfiles is returned when no profiles are provided.
	ErrNoProfiles = spm.ErrNoProfiles
	// ErrNilProfile is returned when a nil profile is provided.
	ErrNilProfile = spm.ErrNilProfile
	// ErrMoreProblems is returned alongside the failures a report lists
	// when it left others out: every validator bounds how many it reports,
	// since a profile holds as many as it holds rules. A caller matching a
	// sentinel must read a match here as "and possibly others", because a
	// failure the profile holds can be absent from the error reporting it.
	ErrMoreProblems = spm.ErrMoreProblems
	// ErrNotifyWithoutListener is returned by Validate for a profile that
	// applies SCMP_ACT_NOTIFY while naming no listener to hand the
	// notification to, and by Intersect and Union if a result would ever
	// hold one. runc sets SECCOMP_FILTER_FLAG_NEW_LISTENER for any such
	// filter and then refuses to create the container without a
	// listenerPath, so the profile does not load at all.
	ErrNotifyWithoutListener = errors.New("SCMP_ACT_NOTIFY without a listener")
)

// InputError is returned by Intersect and Union when one of the profiles
// they were given fails validation, naming its position among the arguments.
type InputError = spm.InputError

// Intersect merges seccomp profiles via intersection: the result permits a
// call only if every input permits it, judged by what runc and crun load
// through libseccomp rather than by the order of the entries. For each
// syscall and argument region the more restrictive action is chosen, and
// where the exact intersection is not expressible, or would not form a safe
// shape, the affected calls fall back to the more restrictive action, so
// the result never permits more than any input. See the Evaluation model
// and Intersection sections of the package documentation.
//
// Every input is checked with Validate first; the first failure is returned
// as an *InputError naming its index. Past an internal budget a syscall
// collapses to its most restrictive action (see Cost bounds).
//
// When inputs share an action, DefaultErrnoRet and per-syscall ErrnoRet
// come from the earliest of them (see Errno values). ListenerPath,
// ListenerMetadata and SECCOMP_FILTER_FLAG_WAIT_KILLABLE_RECV come from the
// first input that sets a ListenerPath, and a result that would notify
// without one is refused with ErrNotifyWithoutListener (see Listener).
// 32-bit and multiplexing architectures may be dropped from the list, and
// the result depends on the native architecture of the running program, so
// run Intersect on the node that loads its result (see Architectures).
// SECCOMP_FILTER_FLAG_SPEC_ALLOW survives only if every input sets it and
// SECCOMP_FILTER_FLAG_LOG if any does (see Flags). A single profile is
// normalized (see Single profiles).
//
// Entries sharing an action, errno and argument filters are grouped into
// one entry with sorted names, and entries are sorted by first name, then
// argument filter, action and errno, so equal inputs give equal output. The
// result passes Validate, but often lists one syscall in several entries,
// which ValidateStrict rejects (see Evaluation model).
//
// This implements the merge KEP-6061 defines for a CRI runtime combining an
// artifact with its baseline.
func Intersect(profiles ...*specs.LinuxSeccomp) (*specs.LinuxSeccomp, error) {
	return foldProfiles(profiles, intersectRules())
}

// Union merges seccomp profiles via union: the result permits a call if
// any input permits it, judged as Intersect judges it. For each syscall and
// argument region the less restrictive action is chosen: every conditional
// entry of every input is kept, with its action raised to the least
// restrictive action any input applies to calls matching its filter. Where
// the exact union is not expressible, or would not form a safe shape, the
// result over-approximates in the permissive direction, so it never permits
// less than any input. See the Union section of the package documentation.
//
// Inputs are validated, results with SCMP_ACT_NOTIFY but no listener are
// refused, and ties in errno and the listener are broken, as for Intersect.
// Past the pair or combined budget a syscall collapses to its least
// restrictive action (see Cost bounds). Flags mirror Intersect:
// SECCOMP_FILTER_FLAG_SPEC_ALLOW survives if any input sets it,
// SECCOMP_FILTER_FLAG_LOG only if every input does. The covered
// architectures are the union of the inputs', and Union never drops one:
// where a 32-bit or multiplexing architecture would need settling, the
// affected syscalls collapse to their least restrictive action instead (see
// Architectures). Single profiles, output order and which validators the
// result passes are as for Intersect.
//
// This implements the merge the Security Profiles Operator uses to combine
// recorded profiles.
func Union(profiles ...*specs.LinuxSeccomp) (*specs.LinuxSeccomp, error) {
	return foldProfiles(profiles, unionRules())
}

// IntersectForArch merges as Intersect does on a node whose native
// architecture is the given one, and is otherwise identical to it.
//
// Intersect settles the result for the architecture of the running program,
// which a runtime always adds to the filter: the 32-bit and multiplexing
// architectures are read differently where one of them is native (see the
// Architectures section of the package documentation). Use this where the
// merge does not run on the node that loads its result, as in a control
// plane merging for a node of another architecture. Pass the empty Arch to
// imply none, as DiffForArch takes it and as Intersect runs on a platform
// without a native architecture: the result is then settled by the listed
// architectures alone. An architecture this package does not know is
// reported with ErrUnknownArch.
func IntersectForArch(
	native specs.Arch, profiles ...*specs.LinuxSeccomp,
) (*specs.LinuxSeccomp, error) {
	err := checkNative(native)
	if err != nil {
		return nil, err
	}

	return foldProfiles(profiles, intersectRulesFor(native))
}

// UnionForArch merges as Union does on a node whose native architecture is
// the given one, and is otherwise identical to it. See IntersectForArch.
func UnionForArch(
	native specs.Arch, profiles ...*specs.LinuxSeccomp,
) (*specs.LinuxSeccomp, error) {
	err := checkNative(native)
	if err != nil {
		return nil, err
	}

	return foldProfiles(profiles, unionRulesFor(native))
}

// checkNative reports a native architecture this package does not know. The
// empty Arch implies none and passes.
func checkNative(native specs.Arch) error {
	if native != "" && !isKnownArch(native) {
		return fmt.Errorf(
			"native architecture: %w %s", ErrUnknownArch, merge.QuoteBounded(string(native)),
		)
	}

	return nil
}

func foldProfiles(
	profiles []*specs.LinuxSeccomp, rules ruleMerger,
) (*specs.LinuxSeccomp, error) {
	for idx, profile := range profiles {
		err := Validate(profile)
		if err != nil {
			return nil, &spm.InputError{Index: idx, Err: err}
		}
	}

	result, err := merge.Fold(
		profiles,
		// A single profile is normalized to the form a merge result takes,
		// which is also the form Diff compares.
		func(only *specs.LinuxSeccomp) *specs.LinuxSeccomp {
			return normalizeProfile(only, rules)
		},
		func(left, right *specs.LinuxSeccomp) (*specs.LinuxSeccomp, error) {
			return mergeTwo(left, right, rules), nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("merge: %w", err)
	}

	err = resolveListener(result)
	if err != nil {
		return nil, err
	}

	result.Flags = flagsForListener(result.Flags, result.ListenerPath)

	result.Syscalls = regroupSyscalls(result.Syscalls)

	slices.Sort(result.Architectures)
	slices.Sort(result.Flags)

	return result, nil
}

// normalizeProfile returns the profile in the form a merge result takes,
// without merging it against anything: syscall entries are reduced to what
// a runtime loads from them and settled for the merge direction (see
// settledSyscalls), errno values and SCMP_ACT_KILL_THREAD are spelled
// canonically, and the other fields are copied.
//
// The rules it keeps are the ones the profile loads, so they mean on a
// 32-bit architecture what the profile means there, and need no narrow
// reading. Its multiplexer path is settled like a merge result's, since a
// syscall the settling collapses changes that path as well.
func normalizeProfile(profile *specs.LinuxSeccomp, rules ruleMerger) *specs.LinuxSeccomp {
	def := defaultClause(profile)

	normalized := &specs.LinuxSeccomp{
		DefaultAction:    def.action,
		DefaultErrnoRet:  outputErrno(def.action, def.errnoRet),
		Architectures:    merge.DeduplicateSlice(profile.Architectures),
		Flags:            normalizeFlags(profile.Flags),
		ListenerPath:     profile.ListenerPath,
		ListenerMetadata: profile.ListenerMetadata,
		Syscalls:         settledSyscalls(&rules, profile.Syscalls, def),
	}

	rules.settleArchitectures(normalized, def, profile)

	return normalized
}

func mergeTwo(
	left, right *specs.LinuxSeccomp,
	rules ruleMerger,
) *specs.LinuxSeccomp {
	// The merged default follows the same tie-break as every other clause:
	// the left side wins when the actions are equivalent, so its errno
	// survives.
	mergedDefault := pickClause(*defaultClause(left), *defaultClause(right), rules.pick)

	// The listener comes from the first profile that provides one rather
	// than from the left unconditionally: the action lattice can carry
	// SCMP_ACT_NOTIFY in from the right, and a result that notifies without
	// a listenerPath does not load, however loadable both inputs were.
	listener := left
	if left.ListenerPath == "" && right.ListenerPath != "" {
		listener = right
	}

	merged := &specs.LinuxSeccomp{
		DefaultAction:    mergedDefault.action,
		DefaultErrnoRet:  outputErrno(mergedDefault.action, mergedDefault.errnoRet),
		ListenerPath:     listener.ListenerPath,
		ListenerMetadata: listener.ListenerMetadata,
	}

	merged.Flags = mergeFlags(left.Flags, right.Flags, rules.intersect, listener == left)

	if rules.intersect {
		merged.Architectures = merge.IntersectSlice(left.Architectures, right.Architectures)
	} else {
		merged.Architectures = merge.UnionSlice(left.Architectures, right.Architectures)
	}

	if coversAny(merged.Architectures, narrowArchitectures, rules.native) &&
		(loadsWideValue(left) || loadsWideValue(right)) {
		if rules.intersect && !narrowArchitectures[rules.native] {
			merged.Architectures = withoutAny(merged.Architectures, narrowArchitectures)
		} else {
			rules.narrow = true
		}
	}

	merged.Syscalls = rules.mergeProfileSyscalls(left, right, &mergedDefault)

	rules.settleArchitectures(merged, &mergedDefault, left, right)

	return merged
}

// settleArchitectures makes a result safe on the multiplexer path of the
// socket and SysV IPC syscalls when it covers an architecture that
// multiplexes them (see settleMultiplexed). Where it has to settle anything,
// intersection drops the multiplexing architectures from the list instead
// when it can, which is when the native architecture does not multiplex:
// the filter then applies libseccomp's action for an unlisted architecture,
// SCMP_ACT_KILL, to every call of those architectures, as the plain
// intersection of the lists does for an architecture one input leaves out,
// and keeps the rules of the syscalls intact for the architectures that
// remain. A collapse would deny or allow a syscall everywhere to settle one
// architecture. Union cannot drop an architecture an input covers, so it
// collapses.
func (m ruleMerger) settleArchitectures(
	result *specs.LinuxSeccomp, def *clause, inputs ...*specs.LinuxSeccomp,
) {
	if !coversAny(result.Architectures, multiplexingArchitectures, m.native) {
		return
	}

	read := make([]multiplexInput, 0, len(inputs))
	for _, input := range inputs {
		read = append(read, newMultiplexInput(input.Syscalls, defaultClause(input)))
	}

	settled, unsafe := m.settleMultiplexed(result.Syscalls, def, read)
	if unsafe && m.intersect && !multiplexingArchitectures[m.native] {
		result.Architectures = withoutAny(result.Architectures, multiplexingArchitectures)

		return
	}

	result.Syscalls = settled
}

// loadsWideValue reports whether a profile loads a rule comparing an
// argument against a value or mask above 32 bits, as libseccomp reads the
// condition (see canonicalArg). It looks at the entries without expanding
// them into rules: every condition of an entry that loads any rule ends up
// in one.
func loadsWideValue(profile *specs.LinuxSeccomp) bool {
	def := defaultClause(profile)

	for idx := range profile.Syscalls {
		entry := &profile.Syscalls[idx]
		if len(entry.Names) == 0 || entryClauseCount(entry, def) == 0 {
			continue
		}

		if slices.ContainsFunc(entry.Args, func(arg specs.LinuxSeccompArg) bool {
			return wideArg(canonicalArg(arg))
		}) {
			return true
		}
	}

	return false
}

// withoutAny returns the architectures not in the set.
func withoutAny(archs []specs.Arch, set map[specs.Arch]bool) []specs.Arch {
	return slices.DeleteFunc(slices.Clone(archs), func(arch specs.Arch) bool {
		return set[arch]
	})
}

// resolveListener keeps SCMP_ACT_NOTIFY and the listener together in the
// result, for either strategy. runc refuses to create a container for a
// filter that notifies without a listenerPath, so such a result is refused
// rather than emitted, and rather than rewritten: the action a caller wrote
// is the supervisor it asked for, and both rewriting it and dropping it
// silently lose that.
//
// It holds for every result the two merges can build. The action of an entry
// is always the action of an input entry (see pickClause), Validate requires
// a listenerPath from every input that notifies, and the listener is taken
// from the first input that sets one, so an input notifies only alongside a
// listener the result keeps. The default action is never SCMP_ACT_NOTIFY
// either, since Validate rejects it on every input and the merged default is
// one of theirs. This is the check that keeps that reasoning true.
func resolveListener(profile *specs.LinuxSeccomp) error {
	if profile.ListenerPath != "" {
		return nil
	}

	notifies := func(entry specs.LinuxSyscall) bool { return entry.Action == specs.ActNotify }
	if !slices.ContainsFunc(profile.Syscalls, notifies) {
		return nil
	}

	return fmt.Errorf("merge: %w", ErrNotifyWithoutListener)
}

// regroupSyscalls drops entries without names, merges entries sharing the
// same action, errno, and argument filters into one multi-name entry, and
// sorts the result by first name, then by argument filter, action, and
// errno, which is a total order over the result.
//
// Every entry it is given carries exactly one name, so the name check only
// guards the sort below, which reads the first name of each group.
func regroupSyscalls(syscalls []specs.LinuxSyscall) []specs.LinuxSyscall {
	type group struct {
		entry   specs.LinuxSyscall
		argsKey string
		names   map[string]struct{}
	}

	groups := make(map[string]*group)

	for idx := range syscalls {
		entry := &syscalls[idx]
		if len(entry.Names) == 0 {
			continue
		}

		key := groupKey(entry)

		current, ok := groups[key]
		if !ok {
			args := sortedArgs(entry.Args)
			current = &group{
				entry: specs.LinuxSyscall{
					Names:    nil,
					Action:   entry.Action,
					ErrnoRet: merge.ClonePtr(entry.ErrnoRet),
					Args:     args,
				},
				argsKey: sortedArgsKey(args),
				names:   make(map[string]struct{}),
			}
			groups[key] = current
		}

		for _, name := range entry.Names {
			current.names[name] = struct{}{}
		}
	}

	ordered := make([]*group, 0, len(groups))

	for _, current := range groups {
		current.entry.Names = slices.Sorted(maps.Keys(current.names))
		ordered = append(ordered, current)
	}

	slices.SortFunc(ordered, func(a, b *group) int {
		return cmp.Or(
			cmp.Compare(a.entry.Names[0], b.entry.Names[0]),
			cmp.Compare(a.argsKey, b.argsKey),
			cmp.Compare(a.entry.Action, b.entry.Action),
			compareUintPtr(a.entry.ErrnoRet, b.entry.ErrnoRet),
		)
	})

	result := make([]specs.LinuxSyscall, 0, len(ordered))
	for _, current := range ordered {
		result = append(result, current.entry)
	}

	return result
}

func groupKey(entry *specs.LinuxSyscall) string {
	return resultKey(entry.Action, entry.ErrnoRet, argsKey(entry.Args))
}

// UnionSyscalls merges two syscall lists via union, following the rules of
// Union, including how errno values are compared and spelled, but without a
// profile-level DefaultAction, so no entry is elided for equaling it.
//
// The result is only meaningful relative to the default the caller loads it
// with. UnionSyscalls assumes that this default is the same for both lists
// and the result, and more restrictive than every action in them, as in an
// allowlist. Under that assumption the result never permits less than
// either list. A syscall whose rules do not form a safe shape collapses to
// its least restrictive action. Without a profile there are no
// architectures, so the result gets none of the settling Union applies for
// 32-bit or multiplexing architectures. The result is grouped and ordered
// as Union orders it. See the Bare syscall lists section of the package
// documentation.
//
// This function does not validate its inputs. Callers should ensure that
// actions are known, that every entry has at least one name and that no
// name holds a NUL byte, or call Validate on the enclosing profile first.
func UnionSyscalls(left, right []specs.LinuxSyscall) []specs.LinuxSyscall {
	return regroupSyscalls(unionRules().mergeBareSyscalls(left, right))
}

// IntersectSyscalls merges two syscall lists via intersection, following
// the rules of Intersect, including how errno values are compared and
// spelled, but without a profile-level DefaultAction.
//
// It makes the assumption UnionSyscalls makes about the caller's default,
// and under it the result never permits more than either list. Calls a list
// leaves to the default are left to it: syscalls present in only one list
// are dropped, a conditional entry survives only where the other list
// constrains every call it matches, and a syscall that does not form a safe
// shape or exceeds the pair budget is dropped. Without a profile there are
// no architectures, so the result gets none of the settling Intersect
// applies for 32-bit or multiplexing architectures. The result is grouped
// and ordered as Intersect orders it. See the Bare syscall lists section of
// the package documentation.
//
// This function does not validate its inputs. Callers should ensure that
// actions are known, that every entry has at least one name and that no
// name holds a NUL byte, or call Validate on the enclosing profile first.
func IntersectSyscalls(left, right []specs.LinuxSyscall) []specs.LinuxSyscall {
	return regroupSyscalls(intersectRules().mergeBareSyscalls(left, right))
}
