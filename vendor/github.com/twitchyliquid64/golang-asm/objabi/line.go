// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package objabi

import (
	"os"
	"path/filepath"
	"strings"
)

// WorkingDir returns the current working directory
// (or "/???" if the directory cannot be identified),
// with "/" as separator.
func WorkingDir() string {
	var path string
	path, _ = os.Getwd()
	if path == "" {
		path = "/???"
	}
	return filepath.ToSlash(path)
}

// AbsFile returns the absolute filename for file in the given directory,
// as rewritten by the rewrites argument.
// For unrewritten paths, AbsFile rewrites a leading $GOROOT prefix to the literal "$GOROOT".
// If the resulting path is the empty string, the result is "??".
//
// The rewrites argument is a ;-separated list of rewrites.
// Each rewrite is of the form "prefix" or "prefix=>replace",
// where prefix must match a leading sequence of path elements
// and is either removed entirely or replaced by the replacement.
func AbsFile(dir, file, rewrites string) string {
	abs := file
	if dir != "" && !filepath.IsAbs(file) {
		abs = filepath.Join(dir, file)
	}

	start := 0
	for i := 0; i <= len(rewrites); i++ {
		if i == len(rewrites) || rewrites[i] == ';' {
			if new, ok := applyRewrite(abs, rewrites[start:i]); ok {
				abs = new
				goto Rewritten
			}
			start = i + 1
		}
	}
	if hasPathPrefix(abs, GOROOT) {
		abs = "$GOROOT" + abs[len(GOROOT):]
	}

Rewritten:
	if abs == "" {
		abs = "??"
	}
	return abs
}

// applyRewrite applies the rewrite to the path,
// returning the rewritten path and a boolean
// indicating whether the rewrite applied at all.
func applyRewrite(path, rewrite string) (string, bool) {
	prefix, replace := rewrite, ""
	if j := strings.LastIndex(rewrite, "=>"); j >= 0 {
		prefix, replace = rewrite[:j], rewrite[j+len("=>"):]
	}

	if prefix == "" || !hasPathPrefix(path, prefix) {
		return path, false
	}
	if len(path) == len(prefix) {
		return replace, true
	}
	if replace == "" {
		return path[len(prefix)+1:], true
	}
	return replace + path[len(prefix):], true
}

// Does s have t as a path prefix?
// That is, does s == t or does s begin with t followed by a slash?
// For portability, we allow ASCII case folding, so that hasPathPrefix("a/b/c", "A/B") is true.
// Similarly, we allow slash folding, so that hasPathPrefix("a/b/c", "a\\b") is true.
// We do not allow full Unicode case folding, for fear of causing more confusion
// or harm than good. (For an example of the kinds of things that can go wrong,
// see http://article.gmane.org/gmane.linux.kernel/1853266.)
func hasPathPrefix(s string, t string) bool {
	if len(t) > len(s) {
		return false
	}
	var i int
	for i = 0; i < len(t); i++ {
		cs := int(s[i])
		ct := int(t[i])
		if 'A' <= cs && cs <= 'Z' {
			cs += 'a' - 'A'
		}
		if 'A' <= ct && ct <= 'Z' {
			ct += 'a' - 'A'
		}
		if cs == '\\' {
			cs = '/'
		}
		if ct == '\\' {
			ct = '/'
		}
		if cs != ct {
			return false
		}
	}
	return i >= len(s) || s[i] == '/' || s[i] == '\\'
}
