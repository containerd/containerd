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

package deprecation

import (
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// declaredWarnings returns every constant of type Warning declared in
// deprecation.go, keyed by constant name. The source file is parsed and
// type-checked so that a newly added Warning constant is picked up
// automatically instead of relying on a hand-maintained list.
func declaredWarnings(t *testing.T) map[string]Warning {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "deprecation.go", nil, 0)
	require.NoError(t, err)

	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check("deprecation", fset, []*ast.File{f}, nil)
	require.NoError(t, err)

	warnings := map[string]Warning{}
	for _, name := range pkg.Scope().Names() {
		c, ok := pkg.Scope().Lookup(name).(*types.Const)
		if !ok {
			continue
		}
		named, ok := c.Type().(*types.Named)
		if !ok || named.Obj().Name() != "Warning" {
			continue
		}
		warnings[name] = Warning(constant.StringVal(c.Val()))
	}
	require.NotEmpty(t, warnings, "no Warning constants found in deprecation.go")
	return warnings
}

func TestWarningsAreRegistered(t *testing.T) {
	declared := declaredWarnings(t)

	seen := map[Warning]string{}
	for name, w := range declared {
		t.Run(name, func(t *testing.T) {
			assert.True(t, Valid(w), "Warning %s (%q) has no entry in messages", name, w)
			assert.True(t, strings.HasPrefix(string(w), Prefix), "Warning %s (%q) should carry the standard prefix %q", name, w, Prefix)
			assert.Greater(t, len(strings.TrimPrefix(string(w), Prefix)), 0, "Warning %s has an empty id after the prefix", name)
			if other, dup := seen[w]; dup {
				t.Errorf("Warning %s and %s share the same id %q", name, other, w)
			}
			seen[w] = name
		})
	}

	// Every entry in messages must correspond to a declared constant.
	values := make([]Warning, 0, len(declared))
	for _, w := range declared {
		values = append(values, w)
	}
	for w := range messages {
		assert.True(t, slices.Contains(values, w), "messages has an entry %q with no matching Warning constant", w)
	}
}

func TestValid(t *testing.T) {
	for name, w := range declaredWarnings(t) {
		t.Run(name, func(t *testing.T) {
			assert.True(t, Valid(w))
		})
	}

	for _, w := range []Warning{
		"",
		Prefix,
		Prefix + "does-not-exist",
		"cri-registry-mirrors", // known id without the standard prefix
	} {
		t.Run("invalid:"+string(w), func(t *testing.T) {
			assert.False(t, Valid(w))
		})
	}
}

func TestMessage(t *testing.T) {
	for name, w := range declaredWarnings(t) {
		t.Run(name, func(t *testing.T) {
			msg, ok := Message(w)
			assert.True(t, ok, "Warning %s should have a message", name)
			assert.NotEmpty(t, msg, "message for %s should not be empty", name)
		})
	}

	msg, ok := Message(Prefix + "does-not-exist")
	assert.False(t, ok)
	assert.Empty(t, msg)
}
