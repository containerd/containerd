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
	"maps"
	"slices"

	specs "github.com/opencontainers/runtime-spec/specs-go"

	"sigs.k8s.io/security-profiles-merger/internal/merge"
)

// clause is a single rule for one syscall as a runtime adds it to
// libseccomp: an action, an optional errno, and optional argument filters.
// A clause without args is unconditional. See the package documentation
// for how libseccomp evaluates the clauses of a syscall.
//
// The merge computes over a working model of a syscall's clauses: an
// unconditional clause decides every call; otherwise the least restrictive
// clause matching a call decides it, and the fallback (the profile default)
// decides calls no clause matches. This model agrees with libseccomp on the
// safe shapes, where at most one result applies to any call, and the merge
// only reads and writes safe shapes, so its results hold for what runtimes
// load.
type clause struct {
	action   specs.LinuxSeccompAction
	errnoRet *uint
	args     []specs.LinuxSeccompArg
}

func (c clause) unconditional() bool { return len(c.args) == 0 }

// sameResult reports whether two clauses yield the same runtime effect,
// ignoring their argument filters. ErrnoRet only matters for actions that
// return it to the caller; clauses carry it in runtime form, so an unset
// value on such an action has already become EPERM.
func (c clause) sameResult(other clause) bool {
	if !actionsEquivalent(c.action, other.action) {
		return false
	}

	return !errnoSignificant(c.action) || equalUintPtr(c.errnoRet, other.errnoRet)
}

// pickClause selects between two clauses using the given action preference.
// On a tie the left clause wins, so ErrnoRet comes from the leftmost profile.
func pickClause(
	left, right clause,
	pick func(first, second specs.LinuxSeccompAction) specs.LinuxSeccompAction,
) clause {
	if actionsEquivalent(left.action, right.action) {
		return left
	}

	if actionsEquivalent(pick(left.action, right.action), left.action) {
		return left
	}

	return right
}

func lessRestrictiveClause(left, right clause) clause {
	return pickClause(left, right, lessRestrictive)
}

// syscallRules holds every clause of one profile for a single syscall name.
// Past the collectRules budget it holds a summary of them instead, which
// settleInputs turns into the one clause a collapse picks before any merge
// reads the fallback or the conditional clauses.
type syscallRules struct {
	unconditional *clause
	conditional   []clause
	summary       *clauseSummary
}

// forEachClause expands syscall entries into clauses the way a runtime loads
// them and calls visit once per entry index, syscall name, and clause.
// Multi-name entries contribute one clause per name, and entries equal to
// def are skipped when def is non-nil, as runtimes skip them. Names for which
// skip reports true are not visited at all, so a caller that has already
// settled a syscall pays nothing for its remaining names. A nil skip visits
// every name.
func forEachClause(
	syscalls []specs.LinuxSyscall, def *clause,
	skip func(name string) bool,
	visit func(idx int, name string, next clause),
) {
	for idx := range syscalls {
		entry := &syscalls[idx]

		clauses := entryClauses(entry)
		if def != nil {
			clauses = slices.DeleteFunc(clauses, func(next clause) bool {
				return next.sameResult(*def)
			})
		}

		for _, name := range entry.Names {
			if skip != nil && skip(name) {
				continue
			}

			for _, next := range clauses {
				visit(idx, name, next)
			}
		}
	}
}

// maxLoadedClauses bounds how many clauses collectRules materializes for one
// input. An entry contributes one clause per name, so the clauses of a
// profile grow as the product of its name and condition counts rather than
// with its size: 40000 names against 256 conditions fit in 441 KB of JSON and
// expand to ten million clauses. Past this bound an input is read in
// summarized form instead, which costs one pass over the entries and no
// memory per clause (see summarizeRules).
//
// No profile a runtime should accept comes near the bound. ValidateArtifact
// rejects an artifact loading more than MaxArtifactClauses rules, which is a
// quarter of it, so an artifact is always read exactly.
const maxLoadedClauses = 1 << 16

// collectRules splits syscall entries into per-name clause sets as a runtime
// loads them. Entries equal to the profile default are skipped when def is
// non-nil, the first unconditional entry wins over later ones and hides the
// conditional entries of its name, and exact duplicates are dropped.
//
// An input whose entries would load more than maxLoadedClauses rules in
// total has as many of its syscalls summarized as it takes to fit, largest
// first: such a syscall is read as the clause a collapse picks, which is at
// least as restrictive as its rules for intersection and at least as
// permissive for union. The syscalls that fit are read exactly, so one
// oversized syscall costs its own filters rather than every filter of the
// profile.
func collectRules(syscalls []specs.LinuxSyscall, def *clause) map[string]*syscallRules {
	summarized := summarizedNames(syscalls, def)

	rules := summarizeRules(syscalls, def, summarized)

	skip := func(name string) bool { return summarized[name] }
	if len(summarized) == 0 {
		skip = nil
	}

	forEachClause(syscalls, def, skip, func(_ int, name string, next clause) {
		current, ok := rules[name]
		if !ok {
			current = &syscallRules{unconditional: nil, conditional: nil, summary: nil}
			rules[name] = current
		}

		current.add(next)
	})

	for _, current := range rules {
		if current.unconditional != nil {
			current.conditional = nil

			continue
		}

		current.conditional = dedupeClauses(current.conditional)
	}

	return rules
}

// summarizedNames returns the syscalls collectRules reads in summarized
// form, or nil when every syscall fits the budget.
func summarizedNames(syscalls []specs.LinuxSyscall, def *clause) map[string]bool {
	counts, total := clauseCounts(syscalls, def)

	return oversizedNames(counts, total)
}

// entryClauseCount counts the clauses one entry loads for each of its names,
// the way runtimes add them but without expanding the entry: none when the
// entry equals the profile default, one per condition when it repeats an
// argument index, and one otherwise.
func entryClauseCount(entry *specs.LinuxSyscall, def *clause) uint64 {
	result := clause{
		action:   canonicalAction(entry.Action),
		errnoRet: runtimeErrno(entry.Action, entry.ErrnoRet),
		args:     nil,
	}
	if def != nil && result.sameResult(*def) {
		return 0
	}

	if hasRepeatedIndex(entry.Args) {
		return uint64(len(entry.Args))
	}

	return 1
}

// totalClauses returns how many clauses the entries load in total, so that a
// caller can bound the expansion before paying for it.
//
// The count is a uint64 because it is the product of two counts an untrusted
// profile chooses, which overflows an int on a 32-bit platform.
func totalClauses(syscalls []specs.LinuxSyscall, def *clause) uint64 {
	var total uint64

	for idx := range syscalls {
		total += entryClauseCount(&syscalls[idx], def) * uint64(len(syscalls[idx].Names))
	}

	return total
}

// singleEquality reports whether a clause tests one argument for equality,
// which is the only filter shape unifyErrno rewrites.
func singleEquality(current clause) bool {
	return len(current.args) == 1 && current.args[0].Op == specs.OpEqualTo
}

// oversizedNames returns the syscalls to read in summarized form so that the
// clauses the others load stay inside maxLoadedClauses. The largest go
// first, and only as many as the bound requires, so a profile spends its
// budget on the syscalls that fit rather than on the one that does not.
// Names of equal size are ordered by name, so which syscall loses its
// filters never depends on the order the entries arrive in.
func oversizedNames(counts map[string]uint64, total uint64) map[string]bool {
	if total <= maxLoadedClauses {
		return nil
	}

	names := slices.Collect(maps.Keys(counts))
	slices.SortFunc(names, func(first, second string) int {
		if counts[first] != counts[second] {
			return cmp.Compare(counts[second], counts[first])
		}

		return cmp.Compare(first, second)
	})

	summarized := make(map[string]bool)

	for _, name := range names {
		if total <= maxLoadedClauses {
			break
		}

		summarized[name] = true
		total -= counts[name]
	}

	return summarized
}

// clauseCounts returns how many clauses each syscall name loads and how many
// they are in total, counted as totalClauses counts them.
func clauseCounts(syscalls []specs.LinuxSyscall, def *clause) (map[string]uint64, uint64) {
	counts := make(map[string]uint64)

	var total uint64

	for idx := range syscalls {
		entry := &syscalls[idx]

		rules := entryClauseCount(entry, def)
		if rules == 0 {
			continue
		}

		for _, name := range entry.Names {
			counts[name] += rules
			total += rules
		}
	}

	return counts, total
}

// clauseSummary is how an over-budget input is read: the two clauses a
// collapse can pick for one syscall. Folding every clause into the most and
// the least restrictive one costs no memory per clause, and folds them with
// the preference and tie-break collapseAll uses, so collapsing a summary
// yields the clause collapsing the whole set would have yielded.
type clauseSummary struct {
	strictest clause
	loosest   clause
}

// fold records one clause. Summaries stand for a collapse, which decides
// every call of its syscall, so the argument filters are dropped here.
func (s *clauseSummary) fold(next clause) *clauseSummary {
	next.args = nil

	if s == nil {
		next.errnoRet = merge.ClonePtr(next.errnoRet)

		return &clauseSummary{strictest: next, loosest: next}
	}

	s.strictest = pickClause(s.strictest, next, moreRestrictive)
	s.loosest = pickClause(s.loosest, next, lessRestrictive)

	return s
}

func (s *clauseSummary) foldSummary(other *clauseSummary) *clauseSummary {
	return s.fold(other.strictest).fold(other.loosest)
}

// pick returns the clause a collapse in the given direction takes.
func (s *clauseSummary) pick(intersect bool) clause {
	if intersect {
		return s.strictest
	}

	return s.loosest
}

// summarizeRules reads entries into per-name summaries, which is what
// collectRules falls back to past maxLoadedClauses. Every name of an entry
// loads the same clauses, so each entry is folded once and its fold applied
// per name, which keeps the pass linear in the size of the input instead of
// in the clauses it would expand to. Only the syscalls in summarized are
// read; the rest are left to the caller, which reads them exactly.
func summarizeRules(
	syscalls []specs.LinuxSyscall, def *clause, summarized map[string]bool,
) map[string]*syscallRules {
	rules := make(map[string]*syscallRules)

	if len(summarized) == 0 {
		return rules
	}

	for idx := range syscalls {
		entry := &syscalls[idx]

		var entrySummary *clauseSummary

		for _, next := range entryClauses(entry) {
			if def != nil && next.sameResult(*def) {
				continue
			}

			entrySummary = entrySummary.fold(next)
		}

		if entrySummary == nil {
			continue
		}

		for _, name := range entry.Names {
			if !summarized[name] {
				continue
			}

			current, ok := rules[name]
			if !ok {
				current = &syscallRules{unconditional: nil, conditional: nil, summary: nil}
				rules[name] = current
			}

			current.summary = current.summary.foldSummary(entrySummary)
		}
	}

	return rules
}

// entryClauses expands one syscall entry into clauses. An entry whose args
// repeat an argument index yields one single-condition clause per arg, as
// runc loads such entries; every other entry yields exactly one clause.
// Conditions that hold for every value are dropped, as libseccomp drops them,
// so a clause left without conditions is unconditional.
func entryClauses(entry *specs.LinuxSyscall) []clause {
	base := clause{
		action:   canonicalAction(entry.Action),
		errnoRet: runtimeErrno(entry.Action, entry.ErrnoRet),
		args:     nil,
	}

	if !hasRepeatedIndex(entry.Args) {
		base.args = loadedArgs(entry.Args)

		return []clause{base}
	}

	clauses := make([]clause, 0, len(entry.Args))

	for _, arg := range entry.Args {
		next := base
		next.errnoRet = merge.ClonePtr(base.errnoRet)
		next.args = loadedArgs([]specs.LinuxSeccompArg{arg})
		clauses = append(clauses, next)
	}

	return clauses
}

// loadedArgs returns the sorted, canonical conditions libseccomp keeps from a
// rule, or nil when it keeps none.
func loadedArgs(args []specs.LinuxSeccompArg) []specs.LinuxSeccompArg {
	loaded := slices.DeleteFunc(sortedArgs(args), tautology)
	if len(loaded) == 0 {
		return nil
	}

	return loaded
}

// add records a clause. The first unconditional clause wins; conditional
// clauses accumulate.
func (r *syscallRules) add(next clause) {
	next.errnoRet = merge.ClonePtr(next.errnoRet)

	if !next.unconditional() {
		r.conditional = append(r.conditional, next)

		return
	}

	if r.unconditional == nil {
		r.unconditional = &next
	}
}

// fallback returns the clause applied when no conditional clause matches:
// the unconditional clause if present, otherwise the given profile default.
// A nil default (bare syscall lists) yields nil when no unconditional clause
// exists.
func (r *syscallRules) fallback(def *clause) *clause {
	if r != nil && r.unconditional != nil {
		return r.unconditional
	}

	return def
}

func (r *syscallRules) conditionals() []clause {
	if r == nil {
		return nil
	}

	return r.conditional
}

// maxPairwiseClauses bounds the product of the conditional clause counts of
// the two sides of a merge for one syscall. Both directions compare every
// conditional clause against every clause of the other side, and
// intersection emits a clause per overlapping pair, so this product is the
// work and the output size a merge takes on for one syscall. Past it the
// syscall collapses to one unconditional clause on the safe side of the
// merge direction (see collapse), which is what the merge does anyway
// wherever the exact result is not expressible. It admits an artifact at
// MaxArtifactClausesPerSyscall against a baseline with a dozen filtered
// entries for the same syscall.
const maxPairwiseClauses = 4096

// maxSyscallClauses bounds the conditional clause count of a union for one
// syscall with a fallback. Union raises every clause that overlaps a
// redundant one (see raiseOverlapsOfRedundant), which compares the clauses
// pairwise, so past this bound the syscall collapses as for
// maxPairwiseClauses. Intersection does no such pass and has no bound on
// one side's clause count.
const maxSyscallClauses = 512

// maxPruneClauses bounds the clause count for which pruneDominated runs.
// Pruning compares every clause against every other one and only removes
// clauses that can never decide a call, so skipping it past this bound
// leaves the result less minimal but unchanged in what it permits.
const maxPruneClauses = 512

// ruleMerger describes one merge direction over the clause model.
type ruleMerger struct {
	// pick chooses the action for a call that both sides constrain.
	pick func(first, second specs.LinuxSeccompAction) specs.LinuxSeccompAction
	// intersect selects intersection semantics: a fallback exists only when
	// both sides have one, unconstrained regions are dropped, and a clause is
	// emitted for the overlap of two conditional clauses to express "both
	// filters hold". Union keeps every input clause instead.
	intersect bool
	// narrow reports that the filter the result is loaded into covers an
	// architecture libseccomp compiles 32-bit comparisons for, where a
	// condition against a value above 32 bits tests something else than it
	// says (see narrowArchitectures). settleInputs then reads a syscall
	// carrying such a condition the way it reads one outside the safe
	// shapes.
	narrow bool
	// native is the architecture a runtime always adds to the filter of the
	// result, which the merge cannot drop from it: the one of the running
	// program.
	native specs.Arch
}

func intersectRules() ruleMerger {
	return intersectRulesFor(runningArchitecture())
}

func unionRules() ruleMerger {
	return unionRulesFor(runningArchitecture())
}

// intersectRulesFor and unionRulesFor return the merger of a node whose
// native architecture is the given one.
func intersectRulesFor(native specs.Arch) ruleMerger {
	return ruleMerger{pick: moreRestrictive, intersect: true, narrow: false, native: native}
}

func unionRulesFor(native specs.Arch) ruleMerger {
	return ruleMerger{pick: lessRestrictive, intersect: false, narrow: false, native: native}
}

func (m ruleMerger) pickClause(left, right clause) clause {
	return pickClause(left, right, m.pick)
}

// collapse returns the clause that replaces conditional clauses whose effect
// the merge cannot rely on or cannot express: one unconditional clause
// combining def with every clause action, using the merge direction's
// preference. Whatever libseccomp does with the clauses, a call gets def or
// one of those actions, so the collapsed clause is at least as restrictive
// as any of them for intersection and at least as permissive for union.
//
// def is the profile default, or nil for bare syscall lists. It returns nil
// when the default decides every call instead: when the collapsed clause
// equals def, and for the intersection of bare lists, whose unknown default
// is assumed to be at least as restrictive as every action in them (see
// IntersectSyscalls).
func (m ruleMerger) collapse(def *clause, conditional []clause) *clause {
	if def == nil && m.intersect {
		return nil
	}

	collapsed := m.collapseAll(def, conditional)
	if def != nil && collapsed != nil && collapsed.sameResult(*def) {
		return nil
	}

	return collapsed
}

// settleInputs reads the rules of one input the way the merge can rely on:
// a syscall whose conditional clauses do not form a safe shape, or that was
// summarized for exceeding the collectRules budget, is replaced by the clause
// collapse returns, or removed when that is nil. With m.narrow, so is a
// syscall with a condition against a value above 32 bits: libseccomp
// compares only the lower 32 bits of it on a 32-bit architecture, where the
// syscall's rules then mean something the model does not read, while a call
// still gets the default or the action of one of them.
func (m ruleMerger) settleInputs(rules map[string]*syscallRules, def *clause) {
	for name, current := range rules {
		if current.summary != nil {
			current.unconditional = m.collapse(def, []clause{current.summary.pick(m.intersect)})
			current.summary = nil

			if current.unconditional == nil {
				delete(rules, name)
			}

			continue
		}

		if current.unconditional != nil {
			continue
		}

		current.conditional = unifyErrno(current.conditional)
		if safeShape(current.conditional) &&
			(!m.narrow || !slices.ContainsFunc(current.conditional, wideClause)) {
			continue
		}

		collapsed := m.collapse(def, current.conditional)
		if collapsed == nil {
			delete(rules, name)

			continue
		}

		current.unconditional = collapsed
		current.conditional = nil
	}
}

// settleOutput makes the merged rules of one syscall safe to emit: they
// already are when they form a safe shape, and otherwise the syscall
// collapses. The fallback is nil whenever conditional clauses remain, since
// resolveMixed never leaves both.
func (m ruleMerger) settleOutput(
	def, fallback *clause, conditional []clause,
) (*clause, []clause) {
	conditional = unifyErrno(conditional)
	if len(conditional) == 0 || safeShape(conditional) {
		return fallback, conditional
	}

	return m.collapse(def, conditional), nil
}

// unifyErrno gives clauses that apply one action but report different errno
// values the errno of the first of them. Such clauses are different results
// to libseccomp, which evaluates them in an order of its own, so the shape
// check refuses them and the syscall collapses to a single unconditional
// rule: a baseline filtering one argument and an artifact filtering another,
// one of them spelling the EPERM the other leaves implicit, would deny every
// call of a syscall both inputs allow. Unifying the errno keeps the filters
// and costs only the value a denied call reports, which is the trade
// finishRules already makes when it keeps a clause rather than its errno.
//
// The clauses must share one action: an action decides whether a call runs
// at all, so no action may be rewritten here. ValidateArtifact reports the
// same clauses as conflicting rather than unifying them, since an artifact
// says what it means and the ambiguity is the author's to resolve.
//
// Only clauses testing one argument for equality are unified. That is the
// shape a baseline and an artifact filtering different arguments produce,
// which is the case worth keeping filters for; every other operator leaves
// the clauses as they are, so a rule set whose evaluation order this
// package reads conservatively keeps being read that way rather than being
// made to look uniform.
//
// A clause set that is already a safe shape is left alone. Shape (c) holds
// single equalities on one argument index with any results, so libseccomp
// evaluates it exactly as written: rewriting its errno values there would
// lose what the inputs said for nothing, since no collapse threatens the
// filters.
func unifyErrno(clauses []clause) []clause {
	if !errnoUnifiable(clauses) {
		return clauses
	}

	unified := slices.Clone(clauses)
	for idx := range unified {
		unified[idx].errnoRet = clauses[0].errnoRet
	}

	return unified
}

// errnoUnifiable reports whether unifyErrno may rewrite these clauses: they
// are several, they apply one errno-carrying action, they do not already
// form a safe shape, each tests one argument for equality, no two test the
// same one, and they do not all report the same errno already.
func errnoUnifiable(clauses []clause) bool {
	if len(clauses) < 2 || !errnoSignificant(clauses[0].action) || safeShape(clauses) {
		return false
	}

	differs := false
	filters := make(map[string]struct{}, len(clauses))

	for _, current := range clauses {
		if !actionsEquivalent(current.action, clauses[0].action) || !singleEquality(current) {
			return false
		}

		// Two clauses testing the same argument for the same value are a
		// conflict rather than a pair to unify: libseccomp refuses the
		// second rule, so which result a call gets is not something this
		// package can read, and the syscall must keep collapsing.
		filter := sortedArgsKey(current.args)
		if _, repeated := filters[filter]; repeated {
			return false
		}

		filters[filter] = struct{}{}

		if !equalUintPtr(current.errnoRet, clauses[0].errnoRet) {
			differs = true
		}
	}

	return differs
}

// mergeRules merges the clauses of one syscall from two sides.
//
// For intersection the result never permits more than either input, and for
// union it never permits less, under the working model documented on the
// clause type. Where the exact result is not expressible, intersection
// falls back to the more restrictive and union to the less restrictive
// surrounding action.
//
// leftDef and rightDef are the profile defaults, or nil for bare syscall
// lists. The returned fallback is the unconditional clause of the result, or
// nil when only the caller's default applies. The conditional clauses are
// not yet collapsed: callers run finishRules with the effective fallback,
// which is the merged default where the fallback is elided.
func (m ruleMerger) mergeRules(
	left, right *syscallRules,
	leftDef, rightDef *clause,
) (*clause, []clause) {
	leftFallback := left.fallback(leftDef)
	rightFallback := right.fallback(rightDef)
	fallback := m.mergeFallback(leftFallback, rightFallback)

	leftConds := left.conditionals()
	rightConds := right.conditionals()

	// A union of bare lists without a fallback compares no clauses (see
	// adjustClauses) and runs no pairwise pass, so its work stays linear and
	// it needs no budget.
	if (fallback != nil || m.intersect) && m.exceedsClauseBudget(leftConds, rightConds) {
		if fallback == nil {
			// Only bare lists lack a fallback, and intersection leaves
			// their calls to the caller's default.
			return nil, nil
		}

		return m.collapseAll(fallback, leftConds, rightConds), nil
	}

	var conditional []clause

	conditional = append(conditional, m.adjustClauses(leftConds, rightConds, rightFallback)...)
	conditional = append(conditional, m.adjustClauses(rightConds, leftConds, leftFallback)...)

	if m.intersect {
		leftKeys := clauseKeys(leftConds)
		rightKeys := clauseKeys(rightConds)

		for leftIdx, leftClause := range leftConds {
			for rightIdx, rightClause := range rightConds {
				args, ok := conjoinClauseArgs(
					leftClause.args, rightClause.args,
					leftKeys[leftIdx], rightKeys[rightIdx],
				)
				if !ok {
					continue
				}

				picked := m.pickClause(leftClause, rightClause)
				picked.args = args
				conditional = append(conditional, picked)
			}
		}
	}

	return fallback, conditional
}

// exceedsClauseBudget reports whether merging the two clause sets would take
// more work than the budget allows: the pairwise comparison both directions
// run, and for union the pairwise raising pass over all clauses. For
// intersection only the product counts, so a large clause set against an
// unconditional or absent rule never exceeds the budget. For union the
// raising pass is quadratic in the combined clause count, so the combined
// count is bounded as well, even when one side has no conditional clauses.
//
// The counts are widened to uint64 first: they come from untrusted profiles,
// and their product overflows an int on a 32-bit platform, which would turn
// the budget off exactly for the inputs it exists for.
func (m ruleMerger) exceedsClauseBudget(left, right []clause) bool {
	if uint64(len(left))*uint64(len(right)) > maxPairwiseClauses {
		return true
	}

	return !m.intersect && uint64(len(left))+uint64(len(right)) > maxSyscallClauses
}

// collapseAll reduces a syscall to one unconditional clause combining the
// fallback with every conditional clause of the given sides, using the merge
// direction's preference. For intersection the result is at least as
// restrictive as the exact merge everywhere, since no input applies an
// action more restrictive than the one picked here; for union it is at
// least as permissive, for the same reason, provided a fallback exists or
// the caller's default is at least as restrictive as every clause.
func (m ruleMerger) collapseAll(fallback *clause, sides ...[]clause) *clause {
	var collapsed *clause

	combine := func(next clause) {
		if collapsed == nil {
			picked := next
			picked.errnoRet = merge.ClonePtr(next.errnoRet)
			collapsed = &picked

			return
		}

		picked := m.pickClause(*collapsed, next)
		collapsed = &picked
	}

	if fallback != nil {
		combine(*fallback)
	}

	for _, side := range sides {
		for _, current := range side {
			combine(current)
		}
	}

	if collapsed != nil {
		collapsed.args = nil
	}

	return collapsed
}

// resolveMixed makes a merged rule set expressible: an unconditional entry
// would override every conditional entry of the same syscall at load time,
// so a non-nil fallback must not be emitted next to conditional clauses.
// A single one-condition clause is rewritten as the clause plus its
// complement carrying the fallback, which is exact and a safe shape.
// Otherwise the whole syscall collapses to one unconditional clause
// combining the fallback and every conditional action with the merge
// direction's preference, which is conservative in the safe direction.
func (m ruleMerger) resolveMixed(fallback *clause, conditional []clause) (*clause, []clause) {
	if fallback == nil || len(conditional) == 0 {
		return fallback, conditional
	}

	conditional = m.foldIntoFallback(fallback, conditional)
	if len(conditional) == 0 {
		return fallback, nil
	}

	if len(conditional) == 1 && len(conditional[0].args) == 1 {
		if complement, ok := complementArg(conditional[0].args[0]); ok {
			rest := clause{
				action:   fallback.action,
				errnoRet: merge.ClonePtr(fallback.errnoRet),
				args:     []specs.LinuxSeccompArg{complement},
			}

			return nil, sortClauses([]clause{conditional[0], rest})
		}
	}

	return m.collapseAll(fallback, conditional), nil
}

// foldIntoFallback drops conditional clauses that differ from the fallback
// only by errno. The rewrite in resolveMixed cannot keep their errno, and
// folding them first lets a single remaining filter use the exact complement
// form instead of collapsing the whole syscall. For union, a stricter clause
// overlapping a folded clause is raised to the fallback first, because the
// folded clause no longer shields the overlap.
func (m ruleMerger) foldIntoFallback(fallback *clause, conditional []clause) []clause {
	byArgs, order := groupByArgs(conditional)

	if !m.intersect {
		raiseOverlapsOfRedundant(byArgs, func(current clause) bool {
			return actionsEquivalent(current.action, fallback.action)
		}, *fallback)
	}

	kept := make([]clause, 0, len(order))

	for _, key := range order {
		current := byArgs[key]
		if actionsEquivalent(current.action, fallback.action) {
			continue
		}

		kept = append(kept, current)
	}

	return kept
}

// groupByArgs merges clauses with identical argument filters into the least
// restrictive one, which is the one the working model applies where they
// match, and returns them by argument key together with the keys in order
// of first appearance.
func groupByArgs(clauses []clause) (map[string]clause, []string) {
	byArgs := make(map[string]clause, len(clauses))
	order := make([]string, 0, len(clauses))

	for _, current := range clauses {
		key := sortedArgsKey(current.args)

		if existing, ok := byArgs[key]; ok {
			byArgs[key] = lessRestrictiveClause(existing, current)

			continue
		}

		byArgs[key] = current
		order = append(order, key)
	}

	return byArgs, order
}

// complementArg returns the condition matching exactly the values the given
// condition does not match. Masked comparisons have no complement.
func complementArg(arg specs.LinuxSeccompArg) (specs.LinuxSeccompArg, bool) {
	complement, ok := complementOps[arg.Op]
	if !ok {
		return specs.LinuxSeccompArg{}, false
	}

	return specs.LinuxSeccompArg{
		Index:    arg.Index,
		Value:    arg.Value,
		ValueTwo: 0,
		Op:       complement,
	}, true
}

// complementOps maps each comparison operator to the one matching exactly
// the remaining values. Masked comparisons have no complement.
//
//nolint:gochecknoglobals // immutable lookup table
var complementOps = map[specs.LinuxSeccompOperator]specs.LinuxSeccompOperator{
	specs.OpEqualTo:      specs.OpNotEqual,
	specs.OpNotEqual:     specs.OpEqualTo,
	specs.OpLessThan:     specs.OpGreaterEqual,
	specs.OpGreaterEqual: specs.OpLessThan,
	specs.OpLessEqual:    specs.OpGreaterThan,
	specs.OpGreaterThan:  specs.OpLessEqual,
}

// keyedClause pairs a clause with its precomputed argument key so that
// sorting does not rebuild the key on every comparison.
type keyedClause struct {
	key    string
	clause clause
}

func sortClauses(clauses []clause) []clause {
	keyed := make([]keyedClause, len(clauses))
	for idx := range clauses {
		keyed[idx] = keyedClause{key: sortedArgsKey(clauses[idx].args), clause: clauses[idx]}
	}

	slices.SortFunc(keyed, func(a, b keyedClause) int {
		return cmp.Compare(a.key, b.key)
	})

	for idx := range keyed {
		clauses[idx] = keyed[idx].clause
	}

	return clauses
}

// clauseKeys returns the argument key of every clause, computed once.
func clauseKeys(clauses []clause) []string {
	keys := make([]string, len(clauses))
	for idx := range clauses {
		keys[idx] = sortedArgsKey(clauses[idx].args)
	}

	return keys
}

// mergeFallback combines the fallback clauses of both sides. Intersection
// needs both to be present; union takes whichever exists.
func (m ruleMerger) mergeFallback(left, right *clause) *clause {
	switch {
	case left != nil && right != nil:
		picked := m.pickClause(*left, *right)

		return &picked
	case m.intersect:
		return nil
	case left != nil:
		return left
	default:
		return right
	}
}

// adjustClauses returns one clause per entry of clauses, with the action
// combined against what the other side may apply inside the clause's
// argument region. For intersection this lowers the action to what both
// sides allow; for union it raises it to what either side allows.
//
// The other side's fallback applies inside the region unless an other-side
// clause matches everything the clause matches (see argsCover), in which
// case the fallback can never be reached there. Intersection also
// lowers the action by every overlapping other-side clause, because the
// working model applies the least restrictive matching clause. Union does
// not need that: every other-side clause is emitted on its own and raises
// the result wherever it matches.
//
// When the other side has no fallback (bare lists) and no clause subsuming
// the clause, part of the region is unconstrained on that side and left to
// the caller's default there. Intersection then drops the clause, since that
// default is assumed to be the most restrictive action (see
// IntersectSyscalls), and the pairwise conjunctions of mergeRules keep what
// both sides constrain. Union keeps the clause unchanged without looking at
// the other side at all, so a union of bare lists stays linear.
func (m ruleMerger) adjustClauses(
	clauses, others []clause, otherFallback *clause,
) []clause {
	if !m.intersect && otherFallback == nil {
		return slices.Clone(clauses)
	}

	result := make([]clause, 0, len(clauses))

	for _, current := range clauses {
		adjusted, subsumed := m.adjustAgainstOthers(current, others)

		if otherFallback != nil && !subsumed {
			adjusted = m.pickClause(adjusted, *otherFallback)
		}

		if m.intersect && otherFallback == nil && !subsumed {
			continue
		}

		adjusted.args = current.args
		result = append(result, adjusted)
	}

	return result
}

// adjustAgainstOthers combines current with every overlapping other-side
// clause (intersection only) and reports whether an other-side clause
// subsumes current.
func (m ruleMerger) adjustAgainstOthers(
	current clause, others []clause,
) (clause, bool) {
	adjusted := current
	subsumed := false

	for _, other := range others {
		if argsDisjoint(current.args, other.args) {
			continue
		}

		if argsCover(other.args, current.args) {
			subsumed = true
		}

		if m.intersect {
			adjusted = m.pickClause(adjusted, other)
		}
	}

	return adjusted, subsumed
}

// conjoinClauseArgs returns the filter matching the overlap of two
// conditional clauses, given their precomputed argument keys. Identical
// filters are kept as-is; otherwise the filters must not be provably
// disjoint and must be conjoinable.
func conjoinClauseArgs(
	left, right []specs.LinuxSeccompArg, leftKey, rightKey string,
) ([]specs.LinuxSeccompArg, bool) {
	if leftKey == rightKey {
		return slices.Clone(left), true
	}

	if argsDisjoint(left, right) {
		return nil, false
	}

	return conjoinArgs(left, right)
}

// collapseClauses merges clauses with identical argument filters (keeping the
// least restrictive, since they always match together) and drops clauses
// that yield the same result as the fallback. Runtimes skip such entries at
// load time, so they cannot shield a call from a stricter overlapping clause;
// for union the stricter clause is raised to the fallback instead.
func (m ruleMerger) collapseClauses(clauses []clause, fallback *clause) []clause {
	byArgs, order := groupByArgs(clauses)

	if fallback != nil && !m.intersect {
		raiseOverlapsOfRedundant(byArgs, func(current clause) bool {
			return current.sameResult(*fallback)
		}, *fallback)
	}

	result := make([]clause, 0, len(order))

	for _, key := range order {
		current := byArgs[key]
		if fallback != nil && current.sameResult(*fallback) {
			continue
		}

		result = append(result, current)
	}

	return sortClauses(pruneDominated(result))
}

// pruneDominated drops clauses that can never decide a call under the
// working model: a clause whose filter another clause's filter covers (see
// argsCover) matches only calls the other matches too, and since the least
// restrictive matching clause wins, it is dead unless it is less restrictive
// than that other clause. Clauses with the same result also collapse into
// the wider one, and of two clauses with the same result whose filters
// cover each other, such as "a0 < 1" and "a0 <= 0", the later one goes: a
// merge spells a conjoined condition after one input, so the same clause
// can arrive in two spellings, which would be no safe shape together.
//
// It compares every clause against every other one, so past maxPruneClauses
// it is skipped. Pruning only removes clauses that never decide a call, so
// skipping it leaves the result less minimal but unchanged in what it
// permits.
func pruneDominated(clauses []clause) []clause {
	if len(clauses) > maxPruneClauses {
		return clauses
	}

	kept := make([]clause, 0, len(clauses))

	for idx, current := range clauses {
		dominated := false

		for otherIdx, other := range clauses {
			if otherIdx != idx && dominates(other, current, otherIdx < idx) {
				dominated = true

				break
			}
		}

		if !dominated {
			kept = append(kept, current)
		}
	}

	return kept
}

// dominates reports whether the clause other makes current dead: other
// strictly covers current and is less restrictive or has the same result,
// or has the same result and an equivalent filter and comes first. Two
// equivalent filters with different results are both kept: they are a
// conflict to libseccomp, and the clause set collapses as one.
func dominates(other, current clause, otherFirst bool) bool {
	if !argsCover(other.args, current.args) {
		return false
	}

	if argsCover(current.args, other.args) {
		return otherFirst && current.sameResult(other)
	}

	return current.sameResult(other) || stricter(current, other)
}

// raiseOverlapsOfRedundant raises every clause that is stricter than a
// redundant clause and may match a call the redundant clause also matches.
// Redundant clauses are dropped from the result, or skipped by runtimes when
// they equal the default, so without this the stricter clause would win
// where both match and the union could deny a call an input permits. A
// raised clause becomes redundant itself, so raising propagates.
//
// A raised clause takes raiseTo, which is what decides the region once the
// redundant clause is gone, and raiseTo satisfies redundant, so a clause is
// raised at most once. Processing the redundant clauses from a work list
// therefore compares each pair at most once, and the closure it computes
// does not depend on map iteration order.
func raiseOverlapsOfRedundant(
	byArgs map[string]clause, redundant func(clause) bool, raiseTo clause,
) {
	pending := make([]string, 0, len(byArgs))

	for key, current := range byArgs {
		if redundant(current) {
			pending = append(pending, key)
		}
	}

	for len(pending) > 0 {
		key := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		current := byArgs[key]

		for otherKey, other := range byArgs {
			if otherKey == key || argsDisjoint(current.args, other.args) ||
				!stricter(other, current) {
				continue
			}

			raised := raiseTo
			raised.errnoRet = merge.ClonePtr(raiseTo.errnoRet)
			raised.args = other.args
			byArgs[otherKey] = raised

			pending = append(pending, otherKey)
		}
	}
}

// stricter reports whether the first clause applies a strictly more
// restrictive action than the second.
func stricter(first, second clause) bool {
	return !actionsEquivalent(first.action, second.action) &&
		actionsEquivalent(moreRestrictive(first.action, second.action), first.action)
}

func defaultClause(profile *specs.LinuxSeccomp) *clause {
	return &clause{
		action:   canonicalAction(profile.DefaultAction),
		errnoRet: runtimeErrno(profile.DefaultAction, profile.DefaultErrnoRet),
		args:     nil,
	}
}

// mergedNames returns every syscall name of either side, sorted once over
// the union rather than once per side.
func mergedNames(left, right map[string]*syscallRules) []string {
	names := make([]string, 0, len(left)+len(right))
	for name := range left {
		names = append(names, name)
	}

	for name := range right {
		if _, ok := left[name]; !ok {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	return names
}

// mergeProfileSyscalls merges the syscall entries of two profiles given the
// merged default clause. Entries equal to the merged default are elided.
func (m ruleMerger) mergeProfileSyscalls(
	left, right *specs.LinuxSeccomp,
	mergedDefault *clause,
) []specs.LinuxSyscall {
	leftDef := defaultClause(left)
	rightDef := defaultClause(right)
	leftRules := collectRules(left.Syscalls, leftDef)
	rightRules := collectRules(right.Syscalls, rightDef)

	m.settleInputs(leftRules, leftDef)
	m.settleInputs(rightRules, rightDef)

	names := mergedNames(leftRules, rightRules)

	var result []specs.LinuxSyscall

	for _, name := range names {
		fallback, conditional := m.mergeRules(
			leftRules[name], rightRules[name], leftDef, rightDef,
		)
		fallback, conditional = m.finishRules(mergedDefault, fallback, conditional)

		result = appendRules(result, name, mergedDefault, fallback, conditional)
	}

	return result
}

// appendRules emits the settled rules of one syscall, eliding a fallback
// equal to def.
func appendRules(
	result []specs.LinuxSyscall, name string,
	def, fallback *clause, conditional []clause,
) []specs.LinuxSyscall {
	if fallback != nil && (def == nil || !fallback.sameResult(*def)) {
		result = append(result, clauseToSyscall(name, *fallback))
	}

	for _, current := range conditional {
		result = append(result, clauseToSyscall(name, current))
	}

	return result
}

// finishRules turns merged rules into emittable ones. A fallback equal to
// the merged default is elided, and conditional clauses are collapsed
// against whatever applies where they do not match: the fallback, or the
// merged default once the fallback is elided. A fallback that differs from
// the default only by errno is elided as well when a conditional clause with
// a different action remains, since keeping that clause is worth more than
// the errno value: an unconditional entry would force it to collapse.
// Clauses that share the fallback's action fold into it instead, so the
// leftmost errno survives. Whatever remains is settled into a safe shape.
func (m ruleMerger) finishRules(
	mergedDefault, fallback *clause, conditional []clause,
) (*clause, []clause) {
	switch {
	case fallback == nil || fallback.sameResult(*mergedDefault):
		fallback = nil
		conditional = m.collapseClauses(conditional, mergedDefault)
	case actionsEquivalent(fallback.action, mergedDefault.action):
		conditional = m.collapseClauses(conditional, fallback)

		if slices.ContainsFunc(conditional, func(current clause) bool {
			return !actionsEquivalent(current.action, fallback.action)
		}) {
			conditional = m.collapseClauses(conditional, mergedDefault)
			fallback = nil
		}
	default:
		conditional = m.collapseClauses(conditional, fallback)
	}

	fallback, conditional = m.resolveMixed(fallback, conditional)

	return m.settleOutput(mergedDefault, fallback, conditional)
}

// mergeBareSyscalls merges two syscall lists that carry no profile default.
// Names present on one side only are dropped for intersection and kept for
// union.
func (m ruleMerger) mergeBareSyscalls(left, right []specs.LinuxSyscall) []specs.LinuxSyscall {
	leftRules := collectRules(left, nil)
	rightRules := collectRules(right, nil)

	m.settleInputs(leftRules, nil)
	m.settleInputs(rightRules, nil)

	// Intersection only visits names both sides carry, and the loop below
	// skips the ones the right side lacks, so the left names suffice there.
	var names []string
	if m.intersect {
		names = slices.Sorted(maps.Keys(leftRules))
	} else {
		names = mergedNames(leftRules, rightRules)
	}

	var result []specs.LinuxSyscall

	for _, name := range names {
		leftRule, inLeft := leftRules[name]
		rightRule, inRight := rightRules[name]

		if m.intersect && (!inLeft || !inRight) {
			continue
		}

		fallback, conditional := m.mergeRules(leftRule, rightRule, nil, nil)
		fallback, conditional = m.resolveMixed(fallback, m.collapseClauses(conditional, fallback))
		fallback, conditional = m.settleOutput(nil, fallback, conditional)

		result = appendRules(result, name, nil, fallback, conditional)
	}

	return result
}

// settledSyscalls returns the entries of one profile or bare list the way a
// merge in direction m reads them (see settleInputs), in the form a merge
// result takes: one name per entry, conditional clauses sorted. def is the
// profile default, or nil for bare lists; entries equal to it are elided.
// With a nil merger the syscalls are not settled, which is the form Diff
// compares.
func settledSyscalls(
	m *ruleMerger, syscalls []specs.LinuxSyscall, def *clause,
) []specs.LinuxSyscall {
	rules := collectRules(syscalls, def)
	if m != nil {
		m.settleInputs(rules, def)
	}

	var result []specs.LinuxSyscall

	for _, name := range slices.Sorted(maps.Keys(rules)) {
		current := rules[name]
		if current.summary != nil {
			result = appendSummary(result, name, def, current.summary)

			continue
		}

		result = appendRules(
			result, name, def, current.unconditional, sortClauses(current.conditional),
		)
	}

	return result
}

// appendSummary emits the rules of a syscall that was summarized for
// exceeding the collectRules budget, for a caller without a merge direction:
// both clauses a collapse could pick, so that two inputs differing in either
// direction still differ here. A merge never gets this far, since
// settleInputs resolves the summary to the clause its direction picks.
func appendSummary(
	result []specs.LinuxSyscall, name string, def *clause, summary *clauseSummary,
) []specs.LinuxSyscall {
	result = appendRules(result, name, def, &summary.strictest, nil)
	if summary.loosest.sameResult(summary.strictest) {
		return result
	}

	return appendRules(result, name, def, &summary.loosest, nil)
}

func clauseToSyscall(name string, current clause) specs.LinuxSyscall {
	return specs.LinuxSyscall{
		Names:    []string{name},
		Action:   current.action,
		ErrnoRet: outputErrno(current.action, current.errnoRet),
		Args:     slices.Clone(current.args),
	}
}
