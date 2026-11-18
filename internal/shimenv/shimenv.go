/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package shimenv

import (
	"slices"
	"strings"
)

// DefaultCommandGOMAXPROCS is containerd's default GOMAXPROCS passed in cmd.Env
// when invoking shim helper commands ("start" and "delete") so the Go runtime
// bounds P count at process boot rather than after main() runs.
const DefaultCommandGOMAXPROCS = "2"

// explicitGOMAXPROCSEnv marks that GOMAXPROCS="2" in a "start" helper's
// environment was explicitly configured via [plugins."io.containerd.shim.v1.manager"] env
// rather than defaulted by containerd. Shims that spawn a grouped daemon with a
// higher default (such as GOMAXPROCS=4) check and strip this marker before exec.
const explicitGOMAXPROCSEnv = "_CONTAINERD_SHIM_EXPLICIT_GOMAXPROCS"

// CommandEnv constructs the environment for launching a shim helper ("start" or
// "delete") from daemonEnv:
//   - Strips GOMAXPROCS and GOGC from daemonEnv so Go runtime tuning configured
//     on the containerd daemon itself does not bleed into shim processes.
//   - Appends extraEnv and shimEnv ([plugins."io.containerd.shim.v1.manager"] env).
//   - Defaults GOMAXPROCS=2 in cmd.Env when shimEnv does not configure a
//     non-empty GOMAXPROCS, preserving boot-time GOMAXPROCS before main() runs.
func CommandEnv(daemonEnv, extraEnv, shimEnv []string, isStart bool) []string {
	env := slices.DeleteFunc(slices.Clone(daemonEnv), func(entry string) bool {
		return strings.HasPrefix(entry, "GOMAXPROCS=") ||
			strings.HasPrefix(entry, "GOGC=") ||
			strings.HasPrefix(entry, explicitGOMAXPROCSEnv+"=")
	})
	env = append(env, extraEnv...)
	env = append(env, shimEnv...)

	gomaxprocs := lastValue(shimEnv, "GOMAXPROCS=")
	if gomaxprocs == "" {
		env = append(env, "GOMAXPROCS="+DefaultCommandGOMAXPROCS)
	} else if isStart && gomaxprocs == DefaultCommandGOMAXPROCS {
		env = append(env, explicitGOMAXPROCSEnv+"=1")
	}
	return env
}

// DaemonEnv constructs the environment for a long-running shim daemon spawned
// by a "start" helper (such as containerd-shim-runc-v2). It strips any
// transient explicit-GOMAXPROCS marker and applies defaultGOMAXPROCS (e.g. "4")
// when GOMAXPROCS is unset, empty, or at containerd's unconfigured helper
// default ("2"). Any GOMAXPROCS explicitly configured via
// [plugins."io.containerd.shim.v1.manager"] env (including "2") is preserved.
func DaemonEnv(inheritedEnv []string, defaultGOMAXPROCS string) []string {
	explicit := lastValue(inheritedEnv, explicitGOMAXPROCSEnv+"=") == "1"
	gomaxprocs := lastValue(inheritedEnv, "GOMAXPROCS=")
	useDefault := gomaxprocs == "" || (gomaxprocs == DefaultCommandGOMAXPROCS && !explicit)

	env := slices.DeleteFunc(slices.Clone(inheritedEnv), func(entry string) bool {
		if strings.HasPrefix(entry, explicitGOMAXPROCSEnv+"=") {
			return true
		}
		return useDefault && strings.HasPrefix(entry, "GOMAXPROCS=")
	})
	if useDefault {
		env = append(env, "GOMAXPROCS="+defaultGOMAXPROCS)
	}
	return env
}

func lastValue(env []string, prefix string) string {
	var val string
	for _, entry := range env {
		if v, ok := strings.CutPrefix(entry, prefix); ok {
			val = v
		}
	}
	return val
}
