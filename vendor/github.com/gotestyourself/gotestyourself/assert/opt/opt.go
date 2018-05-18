package opt

import (
	"fmt"
	"time"

	gocmp "github.com/google/go-cmp/cmp"
)

// PathString is a gocmp.FilterPath filter that returns true when path.String()
// matches the string.
//
// The path string is a `.` separated string where each segment is a field name.
// Slices, Arrays, and Maps are always matched against every element in the
// sequence.
func PathString(pathspec string) func(gocmp.Path) bool {
	return func(path gocmp.Path) bool {
		return path.String() == pathspec
	}
}

// nolint: unused,deadcode
func debug(path gocmp.Path) bool {
	fmt.Printf("\nPATH=%s, GoString=%s\n", path, path.GoString())
	for _, step := range path {
		fmt.Printf("STEP=(%T) %s\n", step, step)
	}
	return false
}

// CmpDuration returns a gocmp.Comparer for comparing time.Duration. The
// comparer returns true if the two time.Duration values are within the threshold
// and neither are zero.
func CmpDuration(threshold time.Duration) gocmp.Option {
	return gocmp.Comparer(cmpDuration(threshold))
}

func cmpDuration(threshold time.Duration) func(x, y time.Duration) bool {
	return func(x, y time.Duration) bool {
		if x == 0 || y == 0 {
			return false
		}
		delta := x - y
		return delta <= threshold && delta >= -threshold
	}
}

// Opts is a convenience wrapper for creating a gocmp.Options.
func Opts(opt ...gocmp.Option) gocmp.Options {
	return gocmp.Options(opt)
}

// CmpTime returns a gocmp.Comparer for comparing time.Time. The
// comparer returns true if the two time.Time values are within the threshold
// and neither are zero.
func CmpTime(threshold time.Duration) gocmp.Option {
	return gocmp.Comparer(cmpTime(threshold))
}

func cmpTime(threshold time.Duration) func(x, y time.Time) bool {
	return func(x, y time.Time) bool {
		if x.IsZero() || y.IsZero() {
			return false
		}
		delta := x.Sub(y)
		return delta <= threshold && delta >= -threshold
	}
}
