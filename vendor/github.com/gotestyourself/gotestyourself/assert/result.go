package assert

import (
	"fmt"
	"go/ast"

	"github.com/gotestyourself/gotestyourself/assert/cmp"
	"github.com/gotestyourself/gotestyourself/internal/format"
	"github.com/gotestyourself/gotestyourself/internal/source"
)

func runComparison(
	t TestingT,
	exprFilter astExprListFilter,
	f cmp.Comparison,
	msgAndArgs ...interface{},
) bool {
	if ht, ok := t.(helperT); ok {
		ht.Helper()
	}
	result := f()
	if result.Success() {
		return true
	}

	var message string
	switch typed := result.(type) {
	case resultWithComparisonArgs:
		const stackIndex = 3 // Assert/Check, assert, runComparison
		args, err := source.CallExprArgs(stackIndex)
		if err != nil {
			t.Log(err.Error())
		}
		message = typed.FailureMessage(filterPrintableExpr(exprFilter(args)))
	case resultBasic:
		message = typed.FailureMessage()
	default:
		message = fmt.Sprintf("comparison returned invalid Result type: %T", result)
	}

	t.Log(format.WithCustomMessage(failureMessage+message, msgAndArgs...))
	return false
}

type resultWithComparisonArgs interface {
	FailureMessage(args []ast.Expr) string
}

type resultBasic interface {
	FailureMessage() string
}

type astExprListFilter func([]ast.Expr) []ast.Expr

// filterPrintableExpr filters the ast.Expr slice to only include nodes that are
// easy to read when printed and contain relevant information to an assertion.
//
// Ident and SelectorExpr are included because they print nicely and the variable
// names may provide additional context to their values.
// BasicLit and CompositeLit are excluded because their source is equivalent to
// their value, which is already available.
// Other types are ignored for now, but could be added if they are relevant.
func filterPrintableExpr(args []ast.Expr) []ast.Expr {
	result := make([]ast.Expr, len(args))
	for i, arg := range args {
		switch arg.(type) {
		case *ast.Ident, *ast.SelectorExpr, *ast.IndexExpr, *ast.SliceExpr:
			result[i] = arg
		default:
			result[i] = nil
		}
	}
	return result
}

func filterExprExcludeFirst(args []ast.Expr) []ast.Expr {
	if len(args) < 1 {
		return nil
	}
	return args[1:]
}

func filterExprArgsFromComparison(args []ast.Expr) []ast.Expr {
	if len(args) < 1 {
		return nil
	}
	if callExpr, ok := args[1].(*ast.CallExpr); ok {
		return callExpr.Args
	}
	return nil
}
