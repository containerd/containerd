package files

import (
	"os"

	ignore "github.com/crackcomm/go-gitignore"
)

// Filter represents a set of rules for determining if a file should be included or excluded.
// A rule follows the syntax for patterns used in .gitgnore files for specifying untracked files.
// Examples:
// foo.txt
// *.app
// bar/
// **/baz
// fizz/**
type Filter struct {
	// IncludeHidden - Include hidden files
	IncludeHidden bool
	// Rules - File filter rules
	Rules *ignore.GitIgnore
}

// NewFilter creates a new file filter from a .gitignore file and/or a list of ignore rules.
// An ignoreFile is a path to a file with .gitignore-style patterns to exclude, one per line
// rules is an array of strings representing .gitignore-style patterns
// For reference on ignore rule syntax, see https://git-scm.com/docs/gitignore
func NewFilter(ignoreFile string, rules []string, includeHidden bool) (*Filter, error) {
	var ignoreRules *ignore.GitIgnore
	var err error
	if ignoreFile == "" {
		ignoreRules, err = ignore.CompileIgnoreLines(rules...)
	} else {
		ignoreRules, err = ignore.CompileIgnoreFileAndLines(ignoreFile, rules...)
	}
	if err != nil {
		return nil, err
	}
	return &Filter{IncludeHidden: includeHidden, Rules: ignoreRules}, nil
}

// ShouldExclude takes an os.FileInfo object and applies rules to determine if its target should be excluded.
func (filter *Filter) ShouldExclude(fileInfo os.FileInfo) (result bool) {
	path := fileInfo.Name()
	if !filter.IncludeHidden && isHidden(fileInfo) {
		return true
	}
	return filter.Rules.MatchesPath(path)
}
