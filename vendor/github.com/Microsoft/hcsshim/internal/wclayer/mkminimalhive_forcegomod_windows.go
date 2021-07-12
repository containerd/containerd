// +build generate_but_never_actually_compile

// This exists to force the inclusion of the below package in
// go.mod and hence the vendor directory, so that
// golang.org/x/tools/go/packages.Load can read files from it.

// However, if this import statement gets pulled into an actual
// compile (i.e. by appearing in mkminimalhive_windows.go) then
// it forces both CGO and installation of the libhive library
// this package wraps.

// Per https://github.com/golang/go/wiki/Modules#how-can-i-track-tool-dependencies-for-a-module
// this is the best option.

package main

import _ "github.com/gabriel-samfira/go-hivex"
