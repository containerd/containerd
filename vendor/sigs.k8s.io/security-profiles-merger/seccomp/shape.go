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
	"math"
	"slices"
	"strconv"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// The shapes below are the ones the package documentation describes under
// "Evaluation model": the clause sets libseccomp evaluates exactly, which
// are the only ones a merge relies on and the only ones it emits.

// lower32 masks the lower 32 bits of a value, which is what libseccomp
// compares on 32-bit architectures.
const lower32 = math.MaxUint32

// indexSet is a set of argument indices. Valid indices fit the bitmap; the
// map holds the out-of-range indices of unvalidated input.
type indexSet struct {
	bits uint64
	rest map[uint]struct{}
}

const indexSetBits = 64

// pairSize is the number of clauses in a complementary pair, shape (e).
const pairSize = 2

// add inserts index and reports whether it was already present.
func (s *indexSet) add(index uint) bool {
	if index < indexSetBits {
		bit := uint64(1) << index
		present := s.bits&bit != 0
		s.bits |= bit

		return present
	}

	if _, present := s.rest[index]; present {
		return true
	}

	if s.rest == nil {
		s.rest = make(map[uint]struct{})
	}

	s.rest[index] = struct{}{}

	return false
}

// intersects reports whether both sets hold a common index.
func (s *indexSet) intersects(other *indexSet) bool {
	if s.bits&other.bits != 0 {
		return true
	}

	for index := range s.rest {
		if _, ok := other.rest[index]; ok {
			return true
		}
	}

	return false
}

// hasRepeatedIndex reports whether two conditions share an argument index.
// Every index counts, including one beyond maxSyscallArgIndex, which no
// runtime loads at all: runc indexes a fixed array of six argument counters
// with it and panics, and libseccomp rejects index 6 with -EINVAL. Validate
// rejects such an index, so only Diff and the bare syscall-list functions,
// which validate nothing and load nothing, reach one here, and reading the
// entry as alternatives costs them nothing.
// It runs in linear time, since entries come from untrusted input.
func hasRepeatedIndex(args []specs.LinuxSeccompArg) bool {
	var seen indexSet

	for _, arg := range args {
		if seen.add(arg.Index) {
			return true
		}
	}

	return false
}

// safeShape reports whether conditional clauses of one syscall form one of
// the safe shapes (b) to (e) described above. The clauses must be free of
// exact duplicates. An unconditional clause, shape (a), hides conditional
// clauses and is handled by the caller.
func safeShape(clauses []clause) bool {
	if len(clauses) <= 1 {
		return true
	}

	for _, current := range clauses {
		if len(current.args) != 1 {
			return false
		}
	}

	return distinctEqualities(clauses) || complementPair(clauses) || uniformResult(clauses)
}

// distinctEqualities reports shape (c): single SCMP_CMP_EQ conditions on one
// argument index with values that differ in their lower 32 bits, so the
// clauses stay disjoint where libseccomp compares only those.
func distinctEqualities(clauses []clause) bool {
	index := clauses[0].args[0].Index
	seen := make(map[uint64]struct{}, len(clauses))

	for _, current := range clauses {
		arg := current.args[0]
		if arg.Index != index || arg.Op != specs.OpEqualTo {
			return false
		}

		low := arg.Value & lower32
		if _, dup := seen[low]; dup {
			return false
		}

		seen[low] = struct{}{}
	}

	return true
}

// complementPair reports shape (e): two conditions matching exactly the
// values the other does not.
func complementPair(clauses []clause) bool {
	if len(clauses) != pairSize {
		return false
	}

	complement, ok := complementArg(clauses[0].args[0])

	return ok && complement == clauses[1].args[0]
}

// uniformResult reports shape (d): single conditions sharing one result,
// without a wide range comparison on an index several of them use.
func uniformResult(clauses []clause) bool {
	var seen, shared, wide indexSet

	for _, current := range clauses {
		if !current.sameResult(clauses[0]) {
			return false
		}

		arg := current.args[0]
		if seen.add(arg.Index) {
			shared.add(arg.Index)
		}

		if isRangeOp(arg.Op) && arg.Value > lower32 {
			wide.add(arg.Index)
		}
	}

	return !shared.intersects(&wide)
}

// wideClause reports whether a clause compares an argument against a value
// or mask above 32 bits, which libseccomp truncates on 32-bit architectures.
// Clause arguments are canonical, so a valueTwo outside the mask of a
// SCMP_CMP_MASKED_EQ has already been cleared.
func wideClause(current clause) bool {
	return slices.ContainsFunc(current.args, wideArg)
}

func wideArg(arg specs.LinuxSeccompArg) bool {
	return arg.Value > lower32 || arg.ValueTwo > lower32
}

func isRangeOp(op specs.LinuxSeccompOperator) bool {
	switch op {
	case specs.OpLessThan, specs.OpLessEqual, specs.OpGreaterThan, specs.OpGreaterEqual:
		return true
	case specs.OpNotEqual, specs.OpEqualTo, specs.OpMaskedEqual:
		return false
	default:
		return false
	}
}

// clauseKey formats everything that distinguishes two clauses: the result
// and the argument filter.
func clauseKey(current clause) string {
	return resultKey(current.action, current.errnoRet, sortedArgsKey(current.args))
}

// resultKey formats an action, its errno and the key of an argument filter,
// which is what tells two rules of one syscall apart.
func resultKey(action specs.LinuxSeccompAction, errnoRet *uint, args string) string {
	var builder strings.Builder

	builder.WriteString(string(action))
	builder.WriteByte('|')

	if errnoRet != nil {
		builder.WriteString(strconv.FormatUint(uint64(*errnoRet), 10))
	}

	builder.WriteByte('|')
	builder.WriteString(args)

	return builder.String()
}

// dedupeClauses drops exact duplicates, keeping the first occurrence. A
// runtime adds a duplicate rule without effect. It reuses the backing array
// of clauses.
func dedupeClauses(clauses []clause) []clause {
	if len(clauses) <= 1 {
		return clauses
	}

	seen := make(map[string]struct{}, len(clauses))
	kept := clauses[:0]

	for _, current := range clauses {
		key := clauseKey(current)
		if _, dup := seen[key]; dup {
			continue
		}

		seen[key] = struct{}{}

		kept = append(kept, current)
	}

	return kept
}
