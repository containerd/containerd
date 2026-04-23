//go:build linux

/*
   Copyright The docker Authors.
   Copyright The Moby Authors.
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

package apparmor

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
	"text/template"

	"github.com/containerd/log"
)

// NOTE: This code is copied from <github.com/moby/profiles/apparmor>.
//       If you plan to make any changes, please make sure they are also sent
//       upstream.

const dir = "/etc/apparmor.d"

const defaultTemplate = `
{{if .Abi}}abi <{{.Abi}}>,
{{end}}
{{range $value := .Imports}}
{{$value}}
{{end}}

profile "{{.Name}}" flags=(attach_disconnected,mediate_deleted) {
{{range $value := .InnerImports}}
  {{$value}}
{{end}}

  network,
  capability,
  file,
  umount,
  # Host (privileged) processes may send signals to container processes.
  signal (receive) peer=unconfined,
  # runc may send signals to container processes.
  signal (receive) peer=runc,
  # crun may send signals to container processes.
  signal (receive) peer=crun,
  # Manager may send signals to container processes.
  signal (receive) peer="{{.DaemonProfile}}",
  # Container processes may send signals amongst themselves.
  signal (send,receive) peer="{{.Name}}",
{{if .RootlessKit}}
  # https://github.com/containerd/nerdctl/issues/2730
  signal (receive) peer="{{.RootlessKit}}",
{{end}}

  deny @{PROC}/* w,   # deny write for all files directly in /proc (not in a subdir)
  # deny write to files not in /proc/<number>/** or /proc/sys/**
  deny @{PROC}/{[^1-9/],[^1-9/][^0-9/],[^1-9s/][^0-9y/][^0-9s/],[^1-9/][^0-9/][^0-9/][^0-9/]*}/** w,
  deny @{PROC}/sys/[^k]** w,  # deny /proc/sys except /proc/sys/k* (effectively /proc/sys/kernel)
  deny @{PROC}/sys/kernel/{?,??,[^s][^h][^m]**} w,  # deny everything except shm* in /proc/sys/kernel/
  deny @{PROC}/sysrq-trigger rwklx,
  deny @{PROC}/kcore rwklx,

  deny mount,

  deny /sys/[^f]*/** wklx,
  deny /sys/f[^s]*/** wklx,
  deny /sys/fs/[^c]*/** wklx,
  deny /sys/fs/c[^g]*/** wklx,
  deny /sys/fs/cg[^r]*/** wklx,
  deny /sys/firmware/** rwklx,
  deny /sys/devices/virtual/powercap/** rwklx,
  deny /sys/kernel/security/** rwklx,

  # allow processes within the container to trace each other,
  # provided all other LSM and yama setting allow it.
  ptrace (trace,tracedby,read,readby) peer="{{.Name}}",
}
`

type data struct {
	Abi           string
	Name          string
	Imports       []string
	InnerImports  []string
	DaemonProfile string
	RootlessKit   string
}

// cleanProfileName returns the AppArmor profile name from a confinement
// context as reported by /proc/self/attr/current.
//
// The value may be either a bare profile name, "unconfined", or a profile name
// with a trailing mode suffix of the form " (<mode>)". If profile is empty,
// cleanProfileName returns "unconfined".
func cleanProfileName(profile string) string {
	label, _ := splitCon(profile)
	if label == "" {
		return "unconfined"
	}
	return label
}

// splitCon splits an AppArmor confinement context into a label and mode,
// similar to libapparmor [splitcon]. splitCon follows libapparmor's parsing
// semantics and does not validate the returned mode.
//
// /proc/self/attr/current returns the current confinement context for the
// process. Unlike /sys/kernel/security/apparmor/profiles, this value may not
// include a " (<mode>)" suffix.
//
// Supported forms:
//
//	<profile>
//	<profile> (<mode>)
//	unconfined
//
// splitCon strips one trailing newline before parsing.
//
// [splitcon]: https://gitlab.com/apparmor/apparmor/-/blob/v5.0.1/libraries/libapparmor/src/kernel.c#L562-615
func splitCon(con string) (label, mode string) {
	// Value includes a trailing newline.
	con = strings.TrimSuffix(con, "\n")
	if con == "" || con == "unconfined" {
		return con, ""
	}

	if strings.HasSuffix(con, ")") {
		// Profile names may contain spaces, so split on the last " (" before
		// the trailing ")" rather than the first space.
		if i := strings.LastIndex(con[:len(con)-1], " ("); i >= 0 {
			return con[:i], con[i+2 : len(con)-1]
		}
	}

	return con, ""
}

func loadData(name string, macroExistsFn func(string) bool) (*data, error) {
	p := data{
		Name: name,
	}

	const abi = "abi/3.0"
	if macroExistsFn(abi) {
		p.Abi = abi
	}

	if macroExistsFn("tunables/global") {
		p.Imports = append(p.Imports, "#include <tunables/global>")
	} else {
		p.Imports = append(p.Imports, "@{PROC}=/proc/")
	}
	if macroExistsFn("abstractions/base") {
		p.InnerImports = append(p.InnerImports, "#include <abstractions/base>")
	}

	// Figure out the daemon profile.
	currentProfile, err := os.ReadFile("/proc/self/attr/current")
	if err != nil {
		// If we couldn't get the daemon profile, assume we are running
		// unconfined which is generally the default.
		currentProfile = nil
	}
	p.DaemonProfile = cleanProfileName(string(currentProfile))

	// If we were running in Rootless mode, we could read `/proc/$(cat ${ROOTLESSKIT_STATE_DIR}/child_pid)/exe`,
	// but `nerdctl apparmor load` has to be executed as the root.
	// So, do not check ${ROOTLESSKIT_STATE_DIR} (nor EUID) here.
	p.RootlessKit, err = exec.LookPath("rootlesskit")
	if err != nil {
		log.L.WithError(err).Debug("apparmor: failed to determine the RootlessKit binary path")
		p.RootlessKit = ""
	}
	log.L.Debugf("apparmor: RootlessKit=%q", p.RootlessKit)

	return &p, nil
}

func generate(p *data, o io.Writer) error {
	t, err := template.New("apparmor_profile").Parse(defaultTemplate)
	if err != nil {
		return err
	}
	return t.Execute(o, p)
}

func load(path string) error {
	out, err := aaParser("-Kr", path)
	if err != nil {
		return fmt.Errorf("parser error(%q): %w", strings.TrimSpace(out), err)
	}
	return nil
}

// macroExists checks if the passed macro exists.
func macroExists(m string) bool {
	_, err := os.Stat(path.Join(dir, m))
	return err == nil
}

func aaParser(args ...string) (string, error) {
	out, err := exec.Command("apparmor_parser", args...).CombinedOutput()
	return string(out), err
}

func isLoaded(name string) (bool, error) {
	f, err := os.Open("/sys/kernel/security/apparmor/profiles")
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Entries are of the form "<profile> (<mode>)", e.g. "foo (enforce)".
		// Profile names may contain spaces (quoted names are supported in AppArmor);
		// use splitCon to correctly handle profile names containing spaces and/or parentheses.
		label, _ := splitCon(scanner.Text())
		if label == name {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}
