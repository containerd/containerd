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
	"go/build"
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

// TestWarningsAreRegistered checks every Warning constant in the package's
// non-test source files for the current platform.
func TestWarningsAreRegistered(t *testing.T) {
	source, err := build.Default.ImportDir(".", 0)
	require.NoError(t, err)
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range slices.Concat(source.GoFiles, source.CgoFiles) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		files = append(files, f)
	}

	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check("deprecation", fset, files, nil)
	require.NoError(t, err)
	scope := pkg.Scope()
	warningType := scope.Lookup("Warning").Type()
	seen := map[Warning]string{}
	for _, name := range scope.Names() {
		c, ok := scope.Lookup(name).(*types.Const)
		if !ok || !types.Identical(c.Type(), warningType) {
			continue
		}
		w := Warning(constant.StringVal(c.Val()))
		if other, ok := seen[w]; ok {
			t.Errorf("%s and %s share warning ID %q", name, other, w)
		}
		seen[w] = name
		t.Run(name, func(t *testing.T) {
			id, ok := strings.CutPrefix(string(w), Prefix)
			assert.True(t, ok, "warning %q must start with %q", w, Prefix)
			assert.NotEmpty(t, id)
			assert.True(t, Valid(w))
			message, ok := Message(w)
			assert.True(t, ok)
			assert.NotEmpty(t, message)
		})
	}
	require.NotEmpty(t, seen)
	for w := range messages {
		assert.Contains(t, seen, w, "message has no matching Warning constant")
	}
}

func TestUnknownWarnings(t *testing.T) {
	for _, w := range []Warning{"", Prefix, Prefix + "does-not-exist", "cri-registry-mirrors"} {
		t.Run("invalid:"+string(w), func(t *testing.T) {
			assert.False(t, Valid(w))
			message, ok := Message(w)
			assert.False(t, ok)
			assert.Empty(t, message)
		})
	}
}
