package containerd

import (
	"fmt"
	"strings"
)

// VersionMajor holds the release major number
const VersionMajor = 1

// VersionMinor holds the release minor number
const VersionMinor = 0

// VersionPatch holds the release patch number
const VersionPatch = 0

// GitCommit is filled with the Git revision being used to build the
// program at linking time
var GitCommit = ""

// Version returns the version string which holds the combination of major minor patch and git commit
func Version() string {
	var (
		version           = fmt.Sprintf("%d.%d.%d", VersionMajor, VersionMinor, VersionPatch)
		versions []string = []string{version}
	)
	if GitCommit != "" {
		versions = append(versions, fmt.Sprintf("commit: %s", GitCommit))
	}
	return strings.Join(versions, "\n")
}
