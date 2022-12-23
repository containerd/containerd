package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

func readGoMod(path string) (map[string]string, map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	mod1, err := modfile.Parse(path, b, nil)
	if err != nil {
		return nil, nil, err
	}
	require := make(map[string]string)
	replace := make(map[string]string)
	for _, req := range mod1.Require {
		require[req.Mod.Path] = req.Mod.Version
	}
	for _, rep := range mod1.Replace {
		replace[rep.Old.Path] = rep.Old.Path + rep.Old.Version
	}
	return require, replace, err
}

func realMain() error {
	require1, replace1, err := readGoMod("go.mod")
	if err != nil {
		return err
	}

	dir := os.Args[1]
	require2, replace2, err := readGoMod(filepath.Join(dir, "go.mod"))
	if err != nil {
		return err
	}

	var errors int

	for path, v2 := range require2 {
		if v1, ok := require1[path]; ok && v1 != v2 {
			fmt.Fprintf(os.Stderr, "%s has different values in the go.mod files require section:", path)
			fmt.Fprintf(os.Stderr, "%s in root go.mod %s in %s/go.mod\n", v1, v2, dir)
			errors++
		}
	}

	for path, v2 := range replace2 {
		if v1, ok := replace1[path]; ok && v1 != v2 {
			fmt.Fprintf(os.Stderr, "%s has different values in the go.mod files replace section:", path)
			fmt.Fprintf(os.Stderr, "%s in root go.mod %s in %s/go.mod\n", v1, v2, dir)
			errors++
		}
	}

	for path := range replace1 {
		if _, ok := replace2[path]; !ok {
			fmt.Fprintf(os.Stderr, "%s has an entry in root go.mod replace section, ", path)
			fmt.Fprintf(os.Stderr, "but is missing from replace section in %s/go.mod\n", dir)
			errors++
		}
	}

	if errors > 0 {
		return fmt.Errorf("%d errors", errors)
	}

	return nil
}

func main() {
	err := realMain()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}
