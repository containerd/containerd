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
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"

	"sigs.k8s.io/security-profiles-merger/internal/merge"
)

const (
	maxSyscallArgIndex = 5
	// notifyWriteSyscall is the syscall runc refuses to put behind a
	// notification listener, since the listener answers a request by writing
	// to it.
	notifyWriteSyscall = "write"
	// maxErrno is the largest errno the kernel can return (MAX_ERRNO). runc
	// narrows errnoRet to int16, so larger values wrap into a different
	// errno than the profile author wrote.
	maxErrno = 4095
)

var (
	// ErrUnknownAction is returned when a profile contains an unrecognized
	// seccomp action.
	ErrUnknownAction = errors.New("unknown seccomp action")
	// ErrEmptySyscallNames is returned when a syscall entry has no names.
	ErrEmptySyscallNames = errors.New("syscall entry has no names")
	// ErrEmptySyscallName is returned when a syscall entry contains an
	// empty string in its name list.
	ErrEmptySyscallName = errors.New("empty syscall name")
	// ErrInvalidSyscallName is returned when a syscall name holds a NUL
	// byte, and by ValidateArtifact and ValidateStrict when it holds any
	// other control character. A runtime resolves a name through
	// libseccomp's C API, where a NUL ends it: "re\x00ad" is added as "read"
	// there while this package keeps the two apart, so a merge would settle
	// "read" without the rule the runtime loads for it.
	ErrInvalidSyscallName = errors.New("invalid syscall name")
	// ErrDuplicateSyscallName is returned by ValidateStrict when the same
	// syscall name appears in more than one syscall entry, or more than once
	// within one entry.
	ErrDuplicateSyscallName = errors.New("duplicate syscall name")
	// ErrUnknownOperator is returned when a syscall arg contains an
	// unrecognized comparison operator.
	ErrUnknownOperator = errors.New("unknown seccomp operator")
	// ErrArgIndexOutOfRange is returned when a syscall arg index exceeds
	// the maximum (5).
	ErrArgIndexOutOfRange = errors.New("syscall arg index out of range")
	// ErrUnknownArch is returned when a profile contains an unrecognized
	// architecture.
	ErrUnknownArch = errors.New("unknown architecture")
	// ErrDuplicateArch is returned when the same architecture appears
	// more than once.
	ErrDuplicateArch = errors.New("duplicate architecture")
	// ErrUnknownFlag is returned when a profile contains an unrecognized
	// seccomp flag.
	ErrUnknownFlag = errors.New("unknown seccomp flag")
	// ErrDuplicateFlag is returned when the same flag appears more than
	// once.
	ErrDuplicateFlag = errors.New("duplicate seccomp flag")
	// ErrNotifyNotAllowed is returned by ValidateArtifact and ValidateStrict
	// when a profile uses SCMP_ACT_NOTIFY, which needs a listener that an
	// artifact cannot provide.
	ErrNotifyNotAllowed = errors.New("SCMP_ACT_NOTIFY is not allowed")
	// ErrListenerNotAllowed is returned by ValidateArtifact and
	// ValidateStrict when a profile sets listenerPath, listenerMetadata or
	// SECCOMP_FILTER_FLAG_WAIT_KILLABLE_RECV, which belong to a node-local
	// listener that an artifact must not control.
	ErrListenerNotAllowed = errors.New("listener settings are not allowed")
	// ErrTooManyEntries is returned by ValidateArtifact and ValidateStrict
	// when one syscall name appears in more than
	// MaxArtifactEntriesPerSyscall entries.
	ErrTooManyEntries = errors.New("too many entries for syscall")
	// ErrErrnoOutOfRange is returned when errnoRet or defaultErrnoRet
	// exceeds the largest errno the kernel can return, on an action that
	// returns it (SCMP_ACT_ERRNO or SCMP_ACT_TRACE).
	ErrErrnoOutOfRange = errors.New("errno out of range")
	// ErrUnusedValueTwo is returned by ValidateStrict when an argument
	// condition sets valueTwo with an operator other than
	// SCMP_CMP_MASKED_EQ, the only one that reads it.
	ErrUnusedValueTwo = errors.New("valueTwo is only used by SCMP_CMP_MASKED_EQ")
	// ErrUnusedErrnoRet is returned by ValidateStrict and ValidateArtifact
	// when errnoRet or defaultErrnoRet is set on an action other than
	// SCMP_ACT_ERRNO or SCMP_ACT_TRACE, the only ones that return it. runc
	// ignores such a value, but crun refuses the profile.
	ErrUnusedErrnoRet = errors.New("errnoRet is only used by SCMP_ACT_ERRNO and SCMP_ACT_TRACE")
	// ErrConflictingEntries is returned by ValidateArtifact and
	// ValidateStrict when entries for the same syscall yield different
	// results and either the argument filter of one is equal to or wider
	// than the other's (its conditions are a subset of the other's, which
	// includes an unconditional entry), or they do not form one of the
	// shapes libseccomp evaluates exactly. Where the profile covers an
	// architecture that routes the syscall through socketcall or ipc, the
	// same holds for the copies of its rules on that multiplexer, which lose
	// the condition on the first argument, and conditional entries for the
	// multiplexer itself next to entries for a syscall it carries are
	// reported as well, since libseccomp interleaves them with the copies in
	// an order of its own (see the package documentation). A runtime fails
	// to load many such entries, and silently drops or reorders the others.
	ErrConflictingEntries = errors.New("conflicting entries")
	// ErrTooManyClauses is returned by ValidateArtifact and ValidateStrict
	// when one syscall loads more than MaxArtifactClausesPerSyscall rules.
	ErrTooManyClauses = errors.New("too many rules for syscall")
	// ErrTooManyNames is returned by ValidateArtifact and ValidateStrict
	// when one syscall entry carries more than MaxArtifactNamesPerEntry
	// names.
	ErrTooManyNames = errors.New("too many syscall names in entry")
	// ErrTooManyProfileClauses is returned by ValidateArtifact and
	// ValidateStrict when the profile as a whole loads more than
	// MaxArtifactClauses rules.
	ErrTooManyProfileClauses = errors.New("too many rules in profile")
	// ErrValueTooWide is returned by ValidateArtifact and ValidateStrict
	// when an argument condition compares against a value or mask above 32
	// bits while the filter covers a 32-bit architecture: one the profile
	// lists, or the native architecture of the running program, which
	// runtimes always add. libseccomp compares only the lower 32 bits there,
	// so SCMP_CMP_EQ against 0x100000005 matches 5 for a 32-bit caller, and
	// a SCMP_CMP_MASKED_EQ whose mask sets only upper bits matches every
	// call. valueTwo counts only where libseccomp reads it, within the mask
	// of a SCMP_CMP_MASKED_EQ.
	ErrValueTooWide = errors.New("argument value above 32 bits on a 32-bit architecture")
	// ErrNotifyUnsupported is returned by Validate when SCMP_ACT_NOTIFY
	// appears where runc refuses it outright: as the default action, or on
	// the write syscall. libseccomp accepts both, so this is a runtime
	// constraint rather than a kernel one.
	ErrNotifyUnsupported = errors.New("SCMP_ACT_NOTIFY is not supported here")
)

// MaxArtifactEntriesPerSyscall bounds how many entries may name the same
// syscall in a profile accepted by ValidateArtifact, which reports
// ErrTooManyEntries past it. Real profiles use a handful of
// argument-filtered entries per syscall.
//
// The cap bounds one syscall, not the profile: MaxArtifactClauses bounds the
// profile as a whole (see the Cost bounds section of the package
// documentation).
const MaxArtifactEntriesPerSyscall = 128

// MaxArtifactNamesPerEntry bounds how many syscall names one entry may carry
// in a profile accepted by ValidateArtifact, which reports ErrTooManyNames
// past it. Linux has well under a thousand syscalls, and a profile covering
// all of them names each one once.
//
// An entry loads one rule per name, so this bounds the expansion of a single
// entry, which the per-syscall caps do not: they count per name.
const MaxArtifactNamesPerEntry = 1024

// MaxArtifactClauses bounds how many rules a profile accepted by
// ValidateArtifact loads in total, counted the way
// MaxArtifactClausesPerSyscall counts them for one syscall.
// ValidateArtifact reports ErrTooManyProfileClauses past it.
//
// The rules of a profile grow as the product of its name and condition
// counts rather than with its size, and the per-syscall caps bound only one
// factor each: 40000 names against 256 conditions fit in 441 KB of JSON,
// pass both caps, and load ten million rules, which cost the merge seconds
// and gigabytes. This cap is what keeps a merge of an accepted artifact
// proportional to the size of the file it came in.
const MaxArtifactClauses = 16384

// MaxArtifactClausesPerSyscall bounds how many rules one syscall may load in
// a profile accepted by ValidateArtifact, which reports ErrTooManyClauses
// past it. Rules are counted the way runtimes add them: one per entry naming
// the syscall, except that an entry repeating an argument index adds one
// rule per condition, and entries equal to the default add none. It thereby
// also bounds the conditions of such an entry. Every other entry has at most
// one condition per argument index, so at most six once Validate limits
// indices to 0-5.
//
// Intersect compares every rule of a syscall against every rule for it on
// the other side, and collapses the syscall to its most restrictive action
// once that work exceeds the pair budget (see the Cost bounds section of the
// package documentation). The budget admits this many rules against a
// baseline with a dozen filtered entries for the same syscall, so an
// artifact within the bound is merged precisely there, and one beyond it is
// rejected here rather than silently denied the syscall.
const MaxArtifactClausesPerSyscall = 256

// Validate checks that a seccomp profile contains only known actions and
// that every syscall entry has non-empty names, known argument operators,
// argument indices in range, and known architectures and flags, and that
// SCMP_ACT_NOTIFY appears only where runc loads it and only with the
// listenerPath it needs, which is what a runtime needs to load the profile
// at all. It also rejects a syscall name holding a NUL byte
// (ErrInvalidSyscallName), which a runtime loads as another name than the
// merges read. It is what Intersect and Union run on every input, so
// callers that want to report all problems up front can call it themselves.
//
// Failures are collected and returned together, up to a bound: past it the
// error matches ErrMoreProblems instead of listing the rest, so a sentinel
// a profile violates can be absent from the error that reports it.
//
// Validate enforces none of the MaxArtifact limits and does not range-check
// errno values (see the Errno values section of the package documentation);
// ValidateStrict and ValidateArtifact do both.
func Validate(profile *specs.LinuxSeccomp) error {
	if profile == nil {
		return ErrNilProfile
	}

	var errs []error

	err := validateAction(profile.DefaultAction, "default action")
	if err != nil {
		errs = append(errs, err)
	}

	errs = append(errs,
		validateSyscallArgs(profile.Syscalls),
		validateSyscallNames(profile.Syscalls, hasNUL),
		validateArchitectures(profile.Architectures),
		validateFlags(profile.Flags),
		validateNotifySupport(profile),
	)

	for idx := range profile.Syscalls {
		if len(profile.Syscalls[idx].Names) == 0 {
			errs = append(errs, fmt.Errorf(
				"syscall entry %d: %w", idx, ErrEmptySyscallNames,
			))
		}

		if slices.Contains(profile.Syscalls[idx].Names, "") {
			errs = append(errs, fmt.Errorf(
				"syscall entry %d: %w", idx, ErrEmptySyscallName,
			))
		}

		err := validateAction(
			profile.Syscalls[idx].Action,
			fmt.Sprintf("syscall entry %d action", idx),
		)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return merge.JoinLimited(errs...)
}

// validateNotifySupport rejects SCMP_ACT_NOTIFY where runc refuses to load
// it: as the default action ("SCMP_ACT_NOTIFY cannot be used as default
// action"), on the write syscall, which the listener needs to answer a
// notification, and without a listenerPath to hand the notification to.
// libseccomp itself accepts all three, so these are runc constraints, and a
// profile that hits one fails container creation rather than producing a
// weaker filter.
//
// The listener is required here rather than left to the merge because a
// profile that notifies into nothing is not loadable on its own, and the
// merge would otherwise have to choose between rewriting the action, which
// silently drops the caller's supervisor, and refusing a profile Validate
// accepted. With the requirement, the listener travels with every input that
// notifies, and the merge takes it from the first input that sets one, so no
// result can notify without one (see resolveListener).
func validateNotifySupport(profile *specs.LinuxSeccomp) error {
	var errs []error

	if profile.DefaultAction == specs.ActNotify {
		errs = append(errs, fmt.Errorf(
			"default action: %w", ErrNotifyUnsupported,
		))
	}

	notifies := false

	for idx := range profile.Syscalls {
		entry := &profile.Syscalls[idx]
		if entry.Action != specs.ActNotify {
			continue
		}

		notifies = true

		if slices.Contains(entry.Names, notifyWriteSyscall) {
			errs = append(errs, fmt.Errorf(
				"syscall entry %d action: %w (on %s)",
				idx, ErrNotifyUnsupported, notifyWriteSyscall,
			))
		}
	}

	// Reported once for the profile: the listener is a profile-wide setting,
	// so naming every entry that notifies would repeat one problem.
	if notifies && profile.ListenerPath == "" {
		errs = append(errs, fmt.Errorf("listenerPath: %w", ErrNotifyWithoutListener))
	}

	return merge.JoinLimited(errs...)
}

// ValidateStrict is the strictest of the three: it rejects everything
// ValidateArtifact rejects and, on top of that, duplicate syscall names,
// reported once per name that several entries use and once per entry that
// repeats a name within itself (ErrDuplicateSyscallName), and valueTwo set
// on an operator that ignores it (ErrUnusedValueTwo). A profile that passes
// here therefore passes ValidateArtifact and Validate, the same lattice the
// apparmor and landlock packages use.
//
// The OCI runtime-spec allows the same syscall to appear in several entries
// (for example with different argument filters), and Validate permits it.
// ValidateStrict is a lint for profiles a person writes by hand, where a
// duplicate, a listener setting, or a rule set libseccomp does not evaluate
// exactly is likely a mistake. A runtime checks its baseline with Validate:
// the defaults runtimes ship list one syscall in several entries, and a
// baseline that notifies fails here (ErrNotifyNotAllowed,
// ErrListenerNotAllowed). Merge results are not meant to pass it either,
// since they often list one syscall in several entries; check them with
// Validate.
func ValidateStrict(profile *specs.LinuxSeccomp) error {
	native := runningArchitecture()

	return validateWith(profile, strictChecks(native), syscallRuleCheck(native))
}

// ValidateStrictForArch checks a profile as ValidateStrict does on a node
// whose native architecture is the given one, and is otherwise identical to
// it. It accepts only what ValidateArtifactForArch accepts for the same
// architecture, which keeps the lattice of the three validators where the
// check does not run on the node that loads the profile. The native
// architecture is taken as ValidateArtifactForArch takes it.
func ValidateStrictForArch(native specs.Arch, profile *specs.LinuxSeccomp) error {
	err := checkNative(native)
	if err != nil {
		return err
	}

	return validateWith(profile, strictChecks(native), syscallRuleCheck(native))
}

// strictChecks returns the checks ValidateStrict runs besides Validate, for
// a node with the given native architecture: those of ValidateArtifact and
// its own.
func strictChecks(native specs.Arch) []profileCheck {
	return append(artifactChecks(native), validateDuplicateNames, validateUnusedValueTwo)
}

// ValidateArtifact checks an artifact, a profile from an untrusted source
// such as an OCI artifact a container runtime pulls (KEP-6061), so that it
// loads on every runtime and merges precisely. On top of Validate it
// rejects:
//
//   - duplicate architectures and flags (ErrDuplicateArch,
//     ErrDuplicateFlag);
//   - errno values above 4095 on SCMP_ACT_ERRNO and SCMP_ACT_TRACE
//     (ErrErrnoOutOfRange), and errnoRet or defaultErrnoRet on any other
//     action (ErrUnusedErrnoRet), which runc ignores but crun refuses;
//   - SCMP_ACT_NOTIFY (ErrNotifyNotAllowed) and the listener settings
//     listenerPath, listenerMetadata and
//     SECCOMP_FILTER_FLAG_WAIT_KILLABLE_RECV (ErrListenerNotAllowed), which
//     belong to the node-local listener;
//   - syscall names holding a control character (ErrInvalidSyscallName);
//   - a condition against a value or mask above 32 bits when the filter
//     covers a 32-bit architecture, one the profile lists or the native
//     architecture of the running program (ErrValueTooWide);
//   - a profile past MaxArtifactEntriesPerSyscall,
//     MaxArtifactClausesPerSyscall, MaxArtifactNamesPerEntry or
//     MaxArtifactClauses (ErrTooManyEntries, ErrTooManyClauses,
//     ErrTooManyNames, ErrTooManyProfileClauses);
//   - rules of one syscall with different results where the filter of one
//     is equal to or wider than the other's, or where the conditional rules
//     do not form a safe shape (ErrConflictingEntries).
//
// The per-syscall rule count and the conflict checks run only when Validate
// passes.
//
// It allows duplicate syscall names, which the OCI runtime-spec permits and
// the merges handle, and ignores valueTwo where runtimes ignore it. Failures
// are collected and bounded as Validate bounds them.
//
// A profile that passes may still hold rules libseccomp evaluates in its
// own order or refuses on a multiplexing architecture, so runtimes should
// load the result of intersecting it with their baseline rather than the
// artifact itself. See the Conflicting rules, Cost bounds and Architectures
// sections of the package documentation.
func ValidateArtifact(profile *specs.LinuxSeccomp) error {
	native := runningArchitecture()

	return validateWith(profile, artifactChecks(native), syscallRuleCheck(native))
}

// ValidateArtifactForArch checks an artifact as ValidateArtifact does on a
// node whose native architecture is the given one, and is otherwise
// identical to it. The native architecture decides whether the filter
// covers a 32-bit architecture, and so whether a value above 32 bits is
// reported (ErrValueTooWide). Use it with IntersectForArch where the check
// does not run on the node that loads the result. The empty Arch implies
// none, as IntersectForArch takes it, so that only the listed architectures
// decide. An architecture this package does not know is reported with
// ErrUnknownArch.
func ValidateArtifactForArch(native specs.Arch, profile *specs.LinuxSeccomp) error {
	err := checkNative(native)
	if err != nil {
		return err
	}

	return validateWith(profile, artifactChecks(native), syscallRuleCheck(native))
}

// artifactChecks returns the checks ValidateArtifact runs besides Validate,
// for a node with the given native architecture. ValidateStrict runs them
// too, so that a profile it accepts is accepted by ValidateArtifact as well.
func artifactChecks(native specs.Arch) []profileCheck {
	return []profileCheck{
		validateWellFormed,
		validateSyscallNameSpelling,
		validateNoNotify,
		validateNoListener,
		validateEntryCount,
		validateProfileClauses,
		validateUnusedErrnoRet,
		func(profile *specs.LinuxSeccomp) error { return validateValueWidth(profile, native) },
	}
}

// validateValueWidth reports conditions against a value above 32 bits when
// the filter covers a 32-bit architecture (see ErrValueTooWide). Values are
// read as libseccomp reads them, so a valueTwo it ignores or masks away does
// not count. native is the architecture the runtime adds to the filter.
func validateValueWidth(profile *specs.LinuxSeccomp, native specs.Arch) error {
	if !coversAny(profile.Architectures, narrowArchitectures, native) {
		return nil
	}

	var errs []error

	for idx := range profile.Syscalls {
		for argIdx, arg := range profile.Syscalls[idx].Args {
			if wideArg(canonicalArg(arg)) {
				errs = append(errs, fmt.Errorf(
					"syscall entry %d arg %d: %w (value %#x, valueTwo %#x)",
					idx, argIdx, ErrValueTooWide, arg.Value, arg.ValueTwo,
				))
			}
		}
	}

	return merge.JoinLimited(errs...)
}

type profileCheck func(profile *specs.LinuxSeccomp) error

// validateWith runs Validate and, if the profile is non-nil, every check,
// collecting all failures into one error. The checks in loadable run only
// when Validate passes: they expand entries the way a runtime loads them,
// which assumes known operators and argument indices in range, so that an
// entry without a repeated index carries at most one condition per index.
func validateWith(
	profile *specs.LinuxSeccomp, checks []profileCheck, loadable ...profileCheck,
) error {
	err := Validate(profile)
	if profile == nil {
		return err
	}

	errs := []error{err}

	for _, check := range checks {
		errs = append(errs, check(profile))
	}

	if err == nil {
		for _, check := range loadable {
			errs = append(errs, check(profile))
		}
	}

	return merge.JoinLimited(errs...)
}

// validateWellFormed runs the checks shared by ValidateStrict and
// ValidateArtifact that do not depend on trust: duplicate architectures and
// flags, and out-of-range errno values.
func validateWellFormed(profile *specs.LinuxSeccomp) error {
	return merge.JoinLimited(
		validateDuplicateArchitectures(profile.Architectures),
		validateDuplicateFlags(profile.Flags),
		validateErrnoRange(profile),
	)
}

// validateErrnoRange checks errno values where a runtime reads them; values
// on other actions are reported through validateUnusedErrnoRet instead.
func validateErrnoRange(profile *specs.LinuxSeccomp) error {
	var errs []error

	ret := profile.DefaultErrnoRet
	if ret != nil && errnoSignificant(profile.DefaultAction) && *ret > maxErrno {
		errs = append(errs, fmt.Errorf(
			"defaultErrnoRet: %w (%d, max %d)", ErrErrnoOutOfRange, *ret, maxErrno,
		))
	}

	for idx := range profile.Syscalls {
		entry := &profile.Syscalls[idx]

		ret := entry.ErrnoRet
		if ret != nil && errnoSignificant(entry.Action) && *ret > maxErrno {
			errs = append(errs, fmt.Errorf(
				"syscall entry %d errnoRet: %w (%d, max %d)",
				idx, ErrErrnoOutOfRange, *ret, maxErrno,
			))
		}
	}

	return merge.JoinLimited(errs...)
}

func validateUnusedValueTwo(profile *specs.LinuxSeccomp) error {
	var errs []error

	for idx := range profile.Syscalls {
		for argIdx, arg := range profile.Syscalls[idx].Args {
			if arg.ValueTwo != 0 && arg.Op != specs.OpMaskedEqual {
				errs = append(errs, fmt.Errorf(
					"syscall entry %d arg %d: %w (%s)",
					idx, argIdx, ErrUnusedValueTwo, merge.QuoteBounded(string(arg.Op)),
				))
			}
		}
	}

	return merge.JoinLimited(errs...)
}

func validateUnusedErrnoRet(profile *specs.LinuxSeccomp) error {
	var errs []error

	if profile.DefaultErrnoRet != nil && !errnoSignificant(profile.DefaultAction) {
		errs = append(errs, fmt.Errorf(
			"defaultErrnoRet: %w (%s)",
			ErrUnusedErrnoRet, merge.QuoteBounded(string(profile.DefaultAction)),
		))
	}

	for idx := range profile.Syscalls {
		entry := &profile.Syscalls[idx]
		if entry.ErrnoRet != nil && !errnoSignificant(entry.Action) {
			errs = append(errs, fmt.Errorf(
				"syscall entry %d errnoRet: %w (%s)",
				idx, ErrUnusedErrnoRet, merge.QuoteBounded(string(entry.Action)),
			))
		}
	}

	return merge.JoinLimited(errs...)
}

func validateDuplicateNames(profile *specs.LinuxSeccomp) error {
	return validateDuplicateSyscallNames(profile.Syscalls)
}

// resultSet summarizes the results of the rules recorded under one key.
type resultSet struct {
	first clause
	mixed bool
}

// conflicts reports whether a recorded rule yields a different result than
// next. A nil set records nothing.
func (r *resultSet) conflicts(next clause) bool {
	return r != nil && (r.mixed || !r.first.sameResult(next))
}

func recordResult(sets map[string]*resultSet, key string, next clause) {
	current, ok := sets[key]
	if !ok {
		sets[key] = &resultSet{first: next, mixed: false}

		return
	}

	current.mixed = current.mixed || !current.first.sameResult(next)
}

// conflictTracker remembers the rules seen for one syscall name, keyed by
// their filter and by every filter strictly wider than theirs.
type conflictTracker struct {
	exact    map[string]*resultSet
	narrower map[string]*resultSet
}

func newConflictTracker() *conflictTracker {
	return &conflictTracker{
		exact:    make(map[string]*resultSet),
		narrower: make(map[string]*resultSet),
	}
}

// record stores next and reports whether an earlier rule yields a different
// result while its filter is equal to, wider than, or narrower than the
// filter of next. A filter is wider when its conditions are a proper subset
// of the other's. A rule of a profile that passes Validate has at most one
// condition per argument index, so it has at most 63 such subsets.
func (t *conflictTracker) record(next clause) bool {
	key := sortedArgsKey(next.args)
	conflict := t.exact[key].conflicts(next) || t.narrower[key].conflicts(next)

	forEachProperSubset(next.args, func(subset []specs.LinuxSeccompArg) {
		subsetKey := sortedArgsKey(subset)
		conflict = conflict || t.exact[subsetKey].conflicts(next)

		recordResult(t.narrower, subsetKey, next)
	})

	recordResult(t.exact, key, next)

	return conflict
}

// forEachProperSubset calls visit with every proper subset of args, keeping
// their order. The slice passed to visit is reused between calls.
func forEachProperSubset(
	args []specs.LinuxSeccompArg, visit func(subset []specs.LinuxSeccompArg),
) {
	count := len(args)
	if count > maxSyscallArgIndex+1 {
		// Unreachable for a profile that passes Validate.
		return
	}

	subset := make([]specs.LinuxSeccompArg, 0, count)

	for mask := range (1 << count) - 1 {
		subset = subset[:0]

		for idx := range count {
			if mask&(1<<idx) != 0 {
				subset = append(subset, args[idx])
			}
		}

		visit(subset)
	}
}

// truncatedClause returns the rule libseccomp adds for a 32-bit
// architecture, which compares only the lower 32 bits of each value and
// therefore drops a masked comparison whose mask is empty there.
func truncatedClause(current clause) clause {
	args := make([]specs.LinuxSeccompArg, 0, len(current.args))

	for _, arg := range current.args {
		arg.Value &= lower32
		arg.ValueTwo &= lower32

		if !tautology(arg) {
			args = append(args, arg)
		}
	}

	current.args = args

	return current
}

// syscallRuleState is what validateSyscallRules tracks for one syscall.
type syscallRuleState struct {
	// trackers find conflicts among the rules as written and as libseccomp
	// adds them for a 32-bit architecture.
	trackers [2]*conflictTracker
	// conditional holds the conditional rules.
	conditional []clause
	// first is the first rule, and mixedAt the index of the first entry
	// whose rule yields a different result, or -1.
	first   clause
	mixedAt int
}

// syscallRuleCheck returns validateSyscallRules for a node with the given
// native architecture, which decides whether the multiplexer copies of the
// rules are checked.
func syscallRuleCheck(native specs.Arch) profileCheck {
	return func(profile *specs.LinuxSeccomp) error {
		return validateSyscallRules(profile, native)
	}
}

// validateSyscallRules checks the rules each syscall loads: their count
// (MaxArtifactClausesPerSyscall) and, for syscalls within that bound,
// conflicts between them (see ValidateArtifact), on the syscall and, where
// the profile covers an architecture that multiplexes it, on the
// multiplexer. Rules are expanded the way a runtime adds them: entries equal
// to the profile default are skipped, an entry with several conditions on
// one argument index adds one rule per condition, and conditions libseccomp
// drops do not count. Each syscall is reported once, at the first
// conflicting entry.
func validateSyscallRules(profile *specs.LinuxSeccomp, native specs.Arch) error {
	def := defaultClause(profile)

	counts, total := clauseCounts(profile.Syscalls, def)
	checker := &ruleChecker{
		counts:   counts,
		truncate: loadsWideValue(profile),
		states:   make(map[string]*syscallRuleState),
		reported: make(map[string]struct{}),
		errs:     nil,
	}

	checker.checkCounts()

	// Past the profile-wide bound, expanding the entries is the very work
	// that bound exists to prevent, and validateProfileClauses has already
	// rejected the profile for exceeding it.
	if total <= MaxArtifactClauses {
		forEachClause(profile.Syscalls, def, checker.skip, checker.record)
		checker.checkShapes()

		if coversAny(profile.Architectures, multiplexingArchitectures, native) {
			checker.checkMultiplexed(profile.Syscalls, def)
		}
	}

	return merge.JoinLimited(checker.errs...)
}

// validateProfileClauses bounds the profile as a whole: the names one entry
// may carry and the rules the profile may load. The per-syscall caps count
// per name, so neither of them bounds an entry that names a hundred thousand
// syscalls, and the rules such a profile loads are the product of its name
// and condition counts (see MaxArtifactClauses). It counts the rules without
// expanding the entries, so it costs one pass over the profile.
func validateProfileClauses(profile *specs.LinuxSeccomp) error {
	var errs []error

	for idx := range profile.Syscalls {
		names := len(profile.Syscalls[idx].Names)
		if names > MaxArtifactNamesPerEntry {
			errs = append(errs, fmt.Errorf(
				"syscall entry %d: %w (%d, max %d)",
				idx, ErrTooManyNames, names, MaxArtifactNamesPerEntry,
			))
		}
	}

	total := totalClauses(profile.Syscalls, defaultClause(profile))
	if total > MaxArtifactClauses {
		errs = append(errs, fmt.Errorf(
			"profile: %w (%d, max %d)",
			ErrTooManyProfileClauses, total, MaxArtifactClauses,
		))
	}

	return merge.JoinLimited(errs...)
}

// ruleChecker carries the state of validateSyscallRules.
type ruleChecker struct {
	counts   map[string]uint64
	truncate bool
	states   map[string]*syscallRuleState
	reported map[string]struct{}
	errs     []error
}

// checkCounts reports every syscall over the rule bound, in name order.
// Only the names it reports are sorted: a profile may name as many syscalls
// as it has bytes for, while the ones it can put over the bound are limited
// by the rules it loads.
func (c *ruleChecker) checkCounts() {
	var over []string

	for name, count := range c.counts {
		if count > MaxArtifactClausesPerSyscall {
			over = append(over, name)
		}
	}

	slices.Sort(over)

	for _, name := range over {
		c.errs = append(c.errs, fmt.Errorf(
			"syscall %s: %w (%d, max %d)",
			merge.QuoteBounded(name), ErrTooManyClauses,
			c.counts[name], MaxArtifactClausesPerSyscall,
		))
	}
}

func (c *ruleChecker) conflict(idx int, name string) {
	c.reported[name] = struct{}{}

	c.errs = append(c.errs, fmt.Errorf(
		"syscall entry %d: %w for %s",
		idx, ErrConflictingEntries, merge.QuoteBounded(name),
	))
}

// skip reports whether a syscall needs no further rules: it has been
// reported already, or it is over the rule bound, which checkCounts reports
// on its own. forEachClause consults it per name, so the rules of an
// over-bound syscall are never expanded.
func (c *ruleChecker) skip(name string) bool {
	_, done := c.reported[name]

	return done || c.counts[name] > MaxArtifactClausesPerSyscall
}

// record checks the rule an entry adds for a syscall against the rules
// before it. forEachClause consults skip once per entry and name, so a
// conflict reported by an earlier clause of the same entry is caught here
// instead, which keeps a syscall reported once however many rules one entry
// loads for it.
func (c *ruleChecker) record(idx int, name string, next clause) {
	if c.skip(name) {
		return
	}

	state, ok := c.states[name]
	if !ok {
		state = &syscallRuleState{
			trackers:    [2]*conflictTracker{newConflictTracker(), newConflictTracker()},
			conditional: nil,
			first:       next,
			mixedAt:     -1,
		}
		c.states[name] = state
	}

	if state.mixedAt < 0 && !state.first.sameResult(next) {
		state.mixedAt = idx
	}

	if !next.unconditional() {
		state.conditional = append(state.conditional, next)
	}

	conflict := state.trackers[0].record(next)
	if c.truncate {
		conflict = state.trackers[1].record(truncatedClause(next)) || conflict
	}

	if conflict {
		c.conflict(idx, name)
	}
}

// checkShapes reports syscalls whose rules yield different results without
// forming a safe shape: libseccomp evaluates those in its own order and
// refuses many of them. A syscall with an unconditional rule gets here only
// when every rule shares its result, since record reports any other rule as
// conflicting with it.
func (c *ruleChecker) checkShapes() {
	for _, name := range slices.Sorted(maps.Keys(c.states)) {
		state := c.states[name]
		if _, done := c.reported[name]; done || state.mixedAt < 0 {
			continue
		}

		if !safeShape(dedupeClauses(state.conditional)) {
			c.conflict(state.mixedAt, name)
		}
	}
}

// checkMultiplexed reports what libseccomp may refuse on a multiplexer: the
// rules of a multiplexed syscall whose copies, with the condition on the
// first argument gone, have different results and match the whole sub-call
// or form no safe shape (see multiplexRules.refused), and conditional rules
// of the multiplexer itself next to rules of a syscall it carries, which
// libseccomp interleaves with the copies in an order of its own and refuses
// where a copy repeats one of their filters. A syscall over its rule bound
// or reported already is not reported again.
func (c *ruleChecker) checkMultiplexed(syscalls []specs.LinuxSyscall, def *clause) {
	rules := collectMultiplexRules(syscalls, def)

	for _, name := range slices.Sorted(maps.Keys(rules)) {
		if c.skip(name) {
			continue
		}

		if _, multiplexed := multiplexedSyscalls[name]; multiplexed {
			if rules[name].refused() {
				c.conflict(rules[name].entry, name)
			}

			continue
		}

		carried := slices.ContainsFunc(multiplexedBy(name), func(carried string) bool {
			return rules[carried] != nil
		})
		if rules[name].conditional && carried {
			c.conflict(rules[name].entry, name)
		}
	}
}

// validateSyscallNameSpelling reports a syscall name holding a control
// character, which no syscall name holds and which forges a line wherever
// the profile is rendered. A name holding a NUL is left to Validate, which
// reports it for every profile.
func validateSyscallNameSpelling(profile *specs.LinuxSeccomp) error {
	return validateSyscallNames(profile.Syscalls, func(name string) bool {
		return hasControlByte(name) && !hasNUL(name)
	})
}

// validateSyscallNames reports every syscall name invalid holds for.
func validateSyscallNames(syscalls []specs.LinuxSyscall, invalid func(string) bool) error {
	var errs []error

	for idx := range syscalls {
		for _, name := range syscalls[idx].Names {
			if !invalid(name) {
				continue
			}

			errs = append(errs, fmt.Errorf(
				"syscall entry %d: %s: %w",
				idx, merge.QuoteBounded(name), ErrInvalidSyscallName,
			))
		}
	}

	return merge.JoinLimited(errs...)
}

// hasNUL reports whether a name holds a NUL, which ends the name in the C
// API a runtime resolves it through, so that the runtime reads another name
// than this package does.
func hasNUL(name string) bool {
	return strings.IndexByte(name, 0) >= 0
}

// hasControlByte reports whether a name holds a C0 control character or DEL.
func hasControlByte(name string) bool {
	for idx := range len(name) {
		if name[idx] < 0x20 || name[idx] == 0x7f {
			return true
		}
	}

	return false
}

func validateNoNotify(profile *specs.LinuxSeccomp) error {
	var errs []error

	if profile.DefaultAction == specs.ActNotify {
		errs = append(errs, fmt.Errorf(
			"default action: %w", ErrNotifyNotAllowed,
		))
	}

	for idx := range profile.Syscalls {
		if profile.Syscalls[idx].Action == specs.ActNotify {
			errs = append(errs, fmt.Errorf(
				"syscall entry %d action: %w", idx, ErrNotifyNotAllowed,
			))
		}
	}

	return merge.JoinLimited(errs...)
}

func validateEntryCount(profile *specs.LinuxSeccomp) error {
	counts := make(map[string]int)

	for idx := range profile.Syscalls {
		for _, name := range profile.Syscalls[idx].Names {
			counts[name]++
		}
	}

	var errs []error

	for _, name := range slices.Sorted(maps.Keys(counts)) {
		if counts[name] > MaxArtifactEntriesPerSyscall {
			errs = append(errs, fmt.Errorf(
				"syscall %s: %w (%d, max %d)",
				merge.QuoteBounded(name), ErrTooManyEntries, counts[name],
				MaxArtifactEntriesPerSyscall,
			))
		}
	}

	return merge.JoinLimited(errs...)
}

func validateNoListener(profile *specs.LinuxSeccomp) error {
	var errs []error

	if profile.ListenerPath != "" {
		errs = append(errs, fmt.Errorf(
			"listenerPath: %w", ErrListenerNotAllowed,
		))
	}

	if profile.ListenerMetadata != "" {
		errs = append(errs, fmt.Errorf(
			"listenerMetadata: %w", ErrListenerNotAllowed,
		))
	}

	if slices.Contains(profile.Flags, specs.LinuxSeccompFlagWaitKillableRecv) {
		errs = append(errs, fmt.Errorf(
			"flag %s: %w", specs.LinuxSeccompFlagWaitKillableRecv, ErrListenerNotAllowed,
		))
	}

	return merge.JoinLimited(errs...)
}

// validateDuplicateSyscallNames reports each duplicated syscall name once,
// listing every entry that names it, and separately when a name repeats
// within a single entry.
func validateDuplicateSyscallNames(syscalls []specs.LinuxSyscall) error {
	type occurrence struct {
		entries    []int
		repeatedIn []int
	}

	seen := make(map[string]*occurrence)

	for idx, sc := range syscalls {
		inEntry := make(map[string]struct{}, len(sc.Names))

		for _, name := range sc.Names {
			current, ok := seen[name]
			if !ok {
				current = &occurrence{entries: nil, repeatedIn: nil}
				seen[name] = current
			}

			if _, dup := inEntry[name]; dup {
				// Entries are visited in order, so an entry already
				// recorded is the last one.
				last := len(current.repeatedIn) - 1
				if last < 0 || current.repeatedIn[last] != idx {
					current.repeatedIn = append(current.repeatedIn, idx)
				}

				continue
			}

			inEntry[name] = struct{}{}

			current.entries = append(current.entries, idx)
		}
	}

	var errs []error

	for _, name := range slices.Sorted(maps.Keys(seen)) {
		current := seen[name]

		if len(current.entries) > 1 {
			errs = append(errs, fmt.Errorf(
				"syscall %s in entries %s: %w",
				merge.QuoteBounded(name), formatEntries(current.entries),
				ErrDuplicateSyscallName,
			))
		}

		for _, idx := range current.repeatedIn {
			errs = append(errs, fmt.Errorf(
				"syscall %s repeated within entry %d: %w",
				merge.QuoteBounded(name), idx, ErrDuplicateSyscallName,
			))
		}
	}

	return merge.JoinLimited(errs...)
}

// maxListedEntries bounds how many entry indices formatEntries lists. One
// name can sit in as many entries as a profile has, so an error listing all
// of them would grow with the profile (see merge.MaxJoinedErrors).
const maxListedEntries = 8

// formatEntries renders entry indices as "0", "0 and 1", or "0, 1 and 2",
// and past maxListedEntries the first of them followed by how many it left
// out, as in "0, 1, 2, 3, 4, 5, 6, 7 and 5 more".
func formatEntries(entries []int) string {
	listed := min(len(entries), maxListedEntries)

	parts := make([]string, listed)
	for idx, entry := range entries[:listed] {
		parts[idx] = strconv.Itoa(entry)
	}

	if omitted := len(entries) - listed; omitted > 0 {
		return strings.Join(parts, ", ") + " and " + strconv.Itoa(omitted) + " more"
	}

	last := len(parts) - 1
	if last < 1 {
		return strings.Join(parts, "")
	}

	return strings.Join(parts[:last], ", ") + " and " + parts[last]
}

// validateAction names the offending action, which an artifact chooses
// freely and at any length, so the value is bounded before it reaches an
// error a runtime may log.
func validateAction(action specs.LinuxSeccompAction, context string) error {
	if restrictiveness(action) == levelUnknown {
		return fmt.Errorf(
			"%s: %w %s", context, ErrUnknownAction, merge.QuoteBounded(string(action)),
		)
	}

	return nil
}

func isKnownOperator(op specs.LinuxSeccompOperator) bool {
	switch op {
	case specs.OpNotEqual, specs.OpLessThan, specs.OpLessEqual,
		specs.OpEqualTo, specs.OpGreaterEqual, specs.OpGreaterThan,
		specs.OpMaskedEqual:
		return true
	default:
		return false
	}
}

func isKnownArch(arch specs.Arch) bool {
	switch arch {
	case specs.ArchX86, specs.ArchX86_64, specs.ArchX32,
		specs.ArchARM, specs.ArchAARCH64,
		specs.ArchMIPS, specs.ArchMIPS64, specs.ArchMIPS64N32,
		specs.ArchMIPSEL, specs.ArchMIPSEL64, specs.ArchMIPSEL64N32,
		specs.ArchPPC, specs.ArchPPC64, specs.ArchPPC64LE,
		specs.ArchS390, specs.ArchS390X,
		specs.ArchPARISC, specs.ArchPARISC64,
		specs.ArchRISCV64, specs.ArchLOONGARCH64,
		specs.ArchM68K, specs.ArchSH, specs.ArchSHEB:
		return true
	default:
		return false
	}
}

func isKnownFlag(flag specs.LinuxSeccompFlag) bool {
	switch flag {
	case specs.LinuxSeccompFlagLog,
		specs.LinuxSeccompFlagSpecAllow,
		specs.LinuxSeccompFlagWaitKillableRecv,
		flagTsync:
		return true
	default:
		return false
	}
}

func validateArchitectures(archs []specs.Arch) error {
	var errs []error

	for _, arch := range archs {
		if !isKnownArch(arch) {
			errs = append(errs, fmt.Errorf(
				"architecture: %w %s", ErrUnknownArch, merge.QuoteBounded(string(arch)),
			))
		}
	}

	return merge.JoinLimited(errs...)
}

func validateFlags(flags []specs.LinuxSeccompFlag) error {
	var errs []error

	for _, flag := range flags {
		if !isKnownFlag(flag) {
			errs = append(errs, fmt.Errorf(
				"flag: %w %s", ErrUnknownFlag, merge.QuoteBounded(string(flag)),
			))
		}
	}

	return merge.JoinLimited(errs...)
}

func validateDuplicateArchitectures(archs []specs.Arch) error {
	seen := make(map[specs.Arch]struct{}, len(archs))

	var errs []error

	for _, arch := range archs {
		if _, ok := seen[arch]; ok {
			errs = append(errs, fmt.Errorf(
				"architecture: %w %s", ErrDuplicateArch, merge.QuoteBounded(string(arch)),
			))
		} else {
			seen[arch] = struct{}{}
		}
	}

	return merge.JoinLimited(errs...)
}

func validateDuplicateFlags(flags []specs.LinuxSeccompFlag) error {
	seen := make(map[specs.LinuxSeccompFlag]struct{}, len(flags))

	var errs []error

	for _, flag := range flags {
		if _, ok := seen[flag]; ok {
			errs = append(errs, fmt.Errorf(
				"flag: %w %s", ErrDuplicateFlag, merge.QuoteBounded(string(flag)),
			))
		} else {
			seen[flag] = struct{}{}
		}
	}

	return merge.JoinLimited(errs...)
}

func validateSyscallArgs(syscalls []specs.LinuxSyscall) error {
	var errs []error

	for idx, sc := range syscalls {
		for argIdx, arg := range sc.Args {
			if !isKnownOperator(arg.Op) {
				errs = append(errs, fmt.Errorf(
					"syscall entry %d arg %d: %w %s",
					idx, argIdx, ErrUnknownOperator, merge.QuoteBounded(string(arg.Op)),
				))
			}

			if arg.Index > maxSyscallArgIndex {
				errs = append(errs, fmt.Errorf(
					"syscall entry %d arg %d: %w %d",
					idx, argIdx, ErrArgIndexOutOfRange, arg.Index,
				))
			}
		}
	}

	return merge.JoinLimited(errs...)
}
