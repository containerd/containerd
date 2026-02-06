package main

import (
	"bytes"
	//"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
)

// rewriteTestingImports rewrites imports for:
// - all package files
// - the fuzzer
// - dependencies
//
// it rewrites "testing" => "github.com/AdamKorcz/go-118-fuzz-build/testing"
func rewriteTestingImports(pkgs []*packages.Package, fuzzName string) (string, []byte, error) {
	var fuzzFilepath string
	var originalFuzzContents []byte
	originalFuzzContents = []byte("NONE")

	// First find file with fuzz harness
	for _, pkg := range pkgs {
		for _, file := range pkg.GoFiles {
			//fmt.Println(file)
			err := rewriteTestingImport(file)
			if err != nil {
				panic(err)
			}
		}
	}

	// rewrite testing in imported packages
	packages.Visit(pkgs, rewriteImportTesting, nil)

	for _, pkg := range pkgs {
		for _, file := range pkg.GoFiles {
			fuzzFile, b, err := rewriteFuzzer(file, fuzzName)
			if err != nil {
				panic(err)
			}
			if fuzzFile != "" {
				fuzzFilepath = fuzzFile
				originalFuzzContents = b
			}
		}
	}
	return fuzzFilepath, originalFuzzContents, nil
}

func rewriteFuzzer(path, fuzzerName string) (originalPath string, originalFile []byte, err error) {
	var fileHasOurHarness bool // to determine whether we should rewrite filename
	fileHasOurHarness = false

	var originalFuzzContents []byte
	originalFuzzContents = []byte("NONE")

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return "", originalFuzzContents, err
	}
	for _, decl := range f.Decls {
		if _, ok := decl.(*ast.FuncDecl); ok {
			if decl.(*ast.FuncDecl).Name.Name == fuzzerName {
				fileHasOurHarness = true

			}
		}
	}

	if fileHasOurHarness {
		originalFuzzContents, err = os.ReadFile(path)
		if err != nil {
			panic(err)
		}

		// Replace import path
		astutil.DeleteImport(fset, f, "testing")
		astutil.AddImport(fset, f, "github.com/AdamKorcz/go-118-fuzz-build/testing")
	}

	// Rewrite filename
	if fileHasOurHarness {
		var buf bytes.Buffer
		printer.Fprint(&buf, fset, f)

		newFile, err := os.Create(path + "_fuzz.go")
		if err != nil {
			panic(err)
		}
		defer newFile.Close()
		newFile.Write(buf.Bytes())
		return path, originalFuzzContents, nil
	}
	return "", originalFuzzContents, nil
}

// Rewrites testing import of a single path
func rewriteTestingImport(path string) error {
	//fmt.Println("Rewriting ", path)
	fsetCheck := token.NewFileSet()
	fCheck, err := parser.ParseFile(fsetCheck, path, nil, parser.ImportsOnly)
	if err != nil {
		return err
	}

	// First check if the import already exists
	// Return if it does.
	for _, imp := range fCheck.Imports {
		if imp.Path.Value == "github.com/AdamKorcz/go-118-fuzz-build/testing" {
			return nil
		}
	}

	// Replace import path
	for _, imp := range fCheck.Imports {
		if imp.Path.Value == "testing" {
			imp.Path.Value = "github.com/AdamKorcz/go-118-fuzz-build/testing"
		}
	}
	return nil
}

// Rewrites testing import of a package
func rewriteImportTesting(pkg *packages.Package) bool {
	for _, file := range pkg.GoFiles {
		err := rewriteTestingImport(file)
		if err != nil {
			panic(err)
		}
	}
	return true
}

// Checks whether a fuzz test exists in a given file
func rewriteFuzzerImports(path, fuzzName string) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return err
	}
	for _, decl := range f.Decls {
		if _, ok := decl.(*ast.FuncDecl); ok {
			if decl.(*ast.FuncDecl).Name.Name == fuzzName {
				// First rewrite testing.F

			}
		}
	}
	return nil
}
