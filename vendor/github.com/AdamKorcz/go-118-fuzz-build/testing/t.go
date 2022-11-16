package testing

import (
	"fmt"
	"strings"
	"time"
)

// T can be used to terminate the current fuzz iteration
// without terminating the whole fuzz run. To do so, simply
// panic with the text "GO-FUZZ-BUILD-PANIC" and the fuzzer
// will recover.
type T struct {
}

func unsupportedApi(name string) string {
	plsOpenIss := "Please open an issue https://github.com/AdamKorcz/go-118-fuzz-build if you need this feature."
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s is not supported when fuzzing in libFuzzer mode\n.", name))
	b.WriteString(plsOpenIss)
	return b.String()
}

func (t *T) Cleanup(f func()) {
	f()
}

func (t *T) Deadline() (deadline time.Time, ok bool) {
	panic(unsupportedApi("t.Deadline()"))
}

func (t *T) Error(args ...any) {
	fmt.Println(args...)
	panic("error")
}

func (t *T) Errorf(format string, args ...any) {
	fmt.Println(format)
	fmt.Println(args...)
	panic("errorf")
}

func (t *T) Fail() {
	panic("Called T.Fail()")
}

func (t *T) FailNow() {
	panic("Called T.Fail()")
	panic(unsupportedApi("t.FailNow()"))
}

func (t *T) Failed() bool {
	panic(unsupportedApi("t.Failed()"))
}

func (t *T) Fatal(args ...any) {
	fmt.Println(args...)
	panic("fatal")
}
func (t *T) Fatalf(format string, args ...any) {
	fmt.Println(format, args)
	panic("fatal")
}
func (t *T) Helper() {
	panic(unsupportedApi("t.Failed()"))
}
func (t *T) Log(args ...any) {
	fmt.Println(args...)
}

func (t *T) Logf(format string, args ...any) {
	fmt.Println(format)
	fmt.Println(args...)
}

func (t *T) Name() string {
	return "libFuzzer"
}

func (t *T) Parallel() {
	panic(unsupportedApi("t.Failed()"))
}
func (t *T) Run(name string, f func(t *T)) bool {
	panic(unsupportedApi("t.Run()."))
}

func (t *T) Setenv(key, value string) {

}

func (t *T) Skip(args ...any) {
	panic("GO-FUZZ-BUILD-PANIC")
}
func (t *T) SkipNow() {
	panic("GO-FUZZ-BUILD-PANIC")
}

// Is not really supported. We just skip instead
// of printing any message. A log message can be
// added if need be.
func (t *T) Skipf(format string, args ...any) {
	panic("GO-FUZZ-BUILD-PANIC")
}
func (t *T) Skipped() bool {
	panic(unsupportedApi("t.Skipped()"))
}
func (t *T) TempDir() string {
	panic(unsupportedApi("t.TempDir()"))
}
