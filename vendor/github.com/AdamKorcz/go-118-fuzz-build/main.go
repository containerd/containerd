package main

import (
	"flag"
	"go/token"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strings"
	"text/template"

	"golang.org/x/tools/go/packages"
)

var (
	flagFunc      = flag.String("func", "Fuzz", "fuzzer entry point")
	flagO         = flag.String("o", "", "output file")
	flagPath      = flag.String("abs_path", "", "absolute path to fuzzer")
	flagSanitizer = flag.String("sanitizer", "address", "The sanitizer to compile the target with. Either 'address' or 'coverage'.")
	flagCoverpkg  = flag.String("coverpkg", "./...", "the value go-118-fuzz-build passes to the 'coverpkg' flag in coverage builds. Should be the module name+'/...'")

	flagRace    = flag.Bool("race", false, "enable data race detection")
	flagTags    = flag.String("tags", "", "a comma-separated list of build tags to consider satisfied during the build")
	flagV       = flag.Bool("v", false, "print the names of packages as they are compiled")
	flagWork    = flag.Bool("work", false, "print the name of the temporary work directory and do not remove it when exiting")
	flagX       = flag.Bool("x", false, "print the commands")
	flagOverlay = flag.String("overlay", "", "JSON config file that provides an overlay for build operations")

	flagInclude  = flag.String("include", "*", "a comma-separated list of import paths to instrument")
	flagPreserve = flag.String("preserve", "", "a comma-separated list of import paths not to instrument")

	LoadMode = packages.NeedName |
		packages.NeedFiles |
		packages.NeedCompiledGoFiles |
		packages.NeedImports |
		packages.NeedDeps
)

var include, ignore []string

func main() {
	flag.Parse()

	if !token.IsIdentifier(*flagFunc) || !token.IsExported(*flagFunc) {
		log.Fatal("-func must be an exported identifier")
	}

	tags := "gofuzz_libfuzzer,libfuzzer"
	if *flagTags != "" {
		tags += "," + *flagTags
	}

	buildFlags := []string{
		"-buildmode", "c-archive",
		"-tags", tags,
		"-trimpath",
	}

	if *flagRace {
		buildFlags = append(buildFlags, "-race")
	}
	if *flagV {
		buildFlags = append(buildFlags, "-v")
	}
	if *flagWork {
		buildFlags = append(buildFlags, "-work")
	}
	if *flagX {
		buildFlags = append(buildFlags, "-x")
	}

	if len(flag.Args()) != 1 {
		log.Fatal("must specify exactly one package path")
	}
	path := flag.Args()[0]
	if strings.Contains(path, "...") {
		log.Fatal("package path must not contain ... wildcards")
	}

	include = strings.Split(*flagInclude, ",")
	ignore = []string{
		"runtime/cgo",   // No reason to instrument these.
		"runtime/pprof", // No reason to instrument these.
		"runtime/race",  // No reason to instrument these.
		"syscall",       // https://github.com/google/oss-fuzz/issues/3639
	}
	if *flagPreserve != "" {
		ignore = append(ignore, strings.Split(*flagPreserve, ",")...)
	}
	buildFlags = append(buildFlags, "-gcflags", "all=-d=libfuzzer")

	//fset := token.NewFileSet()
	pkgs, err := packages.Load(&packages.Config{
		Mode:       LoadMode,
		BuildFlags: buildFlags,
		Tests:      true,
	}, "pattern="+path)
	if err != nil {
		log.Fatal("failed to load packages:", err)
	}
	visit := func(pkg *packages.Package) {
		if !shouldInstrument(pkg.PkgPath) {
			buildFlags = append(buildFlags, "-gcflags", pkg.PkgPath+"=-d=libfuzzer=0")
		}
	}
	packages.Visit(pkgs, nil, visit)
	if packages.PrintErrors(pkgs) != 0 {
		os.Exit(1)
	}
	/*if len(pkgs) != 1 {
		log.Fatal("package path matched multiple packages")
	}*/

	fuzzerFile, originalFuzzContents, err := rewriteTestingImports(pkgs, *flagFunc)
	if err != nil {
		panic(err)
	}
	os.Remove(fuzzerFile)

	pkg := pkgs[0]

	importPath := pkg.PkgPath
	if strings.HasPrefix(importPath, "_/") {
		importPath = path
	}

	mainFile, err := ioutil.TempFile(".", "main.*.go")
	if err != nil {
		log.Fatal("failed to create temporary file:", err)
	}
	defer os.Remove(mainFile.Name())

	type Data struct {
		PkgPath      string
		Func         string
		Declarations string
		FuzzerParams string
	}
	/*err = mainTmpl.Execute(os.Stdout, &Data{
		PkgPath: importPath,
		Func:    *flagFunc,
	})*/
	//return
	err = mainTmpl.Execute(mainFile, &Data{
		PkgPath: importPath,
		Func:    *flagFunc,
	})
	if err != nil {
		log.Fatal("failed to execute template:", err)
	}
	if err := mainFile.Close(); err != nil {
		log.Fatal(err)
	}

	out := *flagO
	if out == "" {
		out = pkg.Name + "-fuzz.a"
	}

	args := []string{"build", "-o", out}
	if *flagOverlay != "" {
		buildFlags = append(buildFlags, "-overlay", *flagOverlay)
	}
	args = append(args, buildFlags...)
	args = append(args, mainFile.Name())
	cmd := exec.Command("go", args...)
	//cmd := exec.Command("gotip", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		log.Fatal("failed to build packages:", err)
	}

	newFile, err := os.Create(fuzzerFile)
	if err != nil {
		panic(err)
	}
	defer newFile.Close()
	_, err = newFile.Write(originalFuzzContents)
	if err != nil {
		panic(err)
	}
	os.Remove(fuzzerFile + "_fuzz.go")
}

// Packages that match one of the include patterns (default is include all packages)
// and none of the exclude patterns (default is none) will be instrumented.
func shouldInstrument(pkgPath string) bool {
	for _, incPath := range include {
		if matchPattern(incPath, pkgPath) {
			for _, excPath := range ignore {
				if matchPattern(excPath, pkgPath) {
					return false
				}
			}
			return true
		}
	}
	return false
}

func matchPattern(pattern, path string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(path, strings.TrimSuffix(pattern, "*"))
	}
	return strings.EqualFold(path, pattern)
}

var mainTmpl = template.Must(template.New("main").Parse(`
// Code generated by go-118-fuzz-build; DO NOT EDIT.

// +build ignore

package main

import (
	"runtime"
	"strings"
	"unsafe"
	target {{printf "%q" .PkgPath}}
	"github.com/AdamKorcz/go-118-fuzz-build/testing"
)

// #include <stdint.h>
import "C"

//export LLVMFuzzerTestOneInput
func LLVMFuzzerTestOneInput(data *C.char, size C.size_t) C.int {
	s := (*[1<<30]byte)(unsafe.Pointer(data))[:size:size]
	//target.{{.Func}}(s)
	defer catchPanics()
	LibFuzzer{{.Func}}(s)
	return 0
}

func LibFuzzer{{.Func}}(data []byte) int {
	fuzzer := &testing.F{Data:data, T:testing.NewT()}
	defer fuzzer.CleanupTempDirs()
	target.{{.Func}}(fuzzer)
	return 1
}

func catchPanics() {
	if r := recover(); r != nil {
		var err string
		switch r.(type) {
		case string:
			err = r.(string)
		case runtime.Error:
			err = r.(runtime.Error).Error()
		case error:
			err = r.(error).Error()
		}
		if strings.Contains(err, "GO-FUZZ-BUILD-PANIC") {
			return
		} else {
			panic(err)
		}
	}
}

func main() {
}
`))
