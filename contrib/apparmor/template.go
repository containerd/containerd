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

// NOTE: This code is copied from <github.com/docker/docker/profiles/apparmor>.
//       If you plan to make any changes, please make sure they are also sent
//       upstream.

const dir = "/etc/apparmor.d"

const defaultTemplate = `
{{if .Abi}}abi <{{.Abi}}>,
{{end}}
{{range $value := .Imports}}
{{$value}}
{{end}}

profile {{.Name}} flags=(attach_disconnected,mediate_deleted) {
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
  signal (receive) peer={{.DaemonProfile}},
  # Container processes may send signals amongst themselves.
  signal (send,receive) peer={{.Name}},
{{if .RootlessKit}}
  # https://github.com/containerd/nerdctl/issues/2730
  signal (receive) peer={{.RootlessKit}},
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
  ptrace (trace,tracedby,read,readby) peer={{.Name}},
}
`

// profileData holds information about the given profile for generation.
type profileData struct {
	// abi is the ABI version to use.
	abi string
	// name is profile name.
	name string
	// daemonProfile is the profile name of our daemon.
	daemonProfile string
	// imports defines the AppArmor functions to import, before defining the profile.
	imports []string
	// innerImports defines the AppArmor functions to import in the profile.
	innerImports []string
	// rootlessKit is the path to the rootlesskit executable, if available.
	rootlessKit string
}

// Abi returns the AppArmor ABI version used by the profile.
func (d profileData) Abi() string {
	return d.abi
}

// Name returns the quoted AppArmor profile name.
func (d profileData) Name() string {
	return quoteProfileName(d.name)
}

// Imports returns the AppArmor functions imported before the profile definition.
func (d profileData) Imports() []string {
	return d.imports
}

// InnerImports returns the AppArmor functions imported inside the profile.
func (d profileData) InnerImports() []string {
	return d.innerImports
}

// DaemonProfile returns the quoted AppArmor profile name of the daemon.
func (d profileData) DaemonProfile() string {
	if d.daemonProfile == "unconfined" {
		return d.daemonProfile
	}
	return quoteProfileName(d.daemonProfile)
}

// RootlessKit returns the quoted path to the rootlesskit executable.
func (d profileData) RootlessKit() string {
	return quoteProfileName(d.rootlessKit)
}

// quoteProfileName returns s as a quoted AppArmor profile name, escaping
// characters as needed to preserve the name literally rather than interpreting
// it as an AARE pattern. Empty strings are returned unchanged.
//
// AppArmor quoted identifiers may contain any character other than NUL. When
// processing an identifier, the parser decodes escape sequences while
// preserving escapes for AARE special characters so they can be handled by
// the pattern-matching backend.
//
// Callers are expected to pass valid profile names, which excludes NUL.
//
// See:
//   - https://gitlab.com/apparmor/apparmor/-/blob/v5.0.2/parser/parser_lex.l#L286-287
//   - https://gitlab.com/apparmor/apparmor/-/blob/v5.0.2/parser/parser_misc.c#L468-507
//   - https://gitlab.com/apparmor/apparmor/-/blob/v5.0.2/parser/lib.c#L144-219
func quoteProfileName(s string) string {
	if s == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(s) + 2)

	b.WriteByte('"')
	for i := range len(s) {
		c := s[i]
		switch c {
		case '\\', '"', '*', '?', '[', ']', '{', '}', '^', ',':
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')

	return b.String()
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

func loadData(name string) (*profileData, error) {
	p := profileData{
		name: name,
	}

	const abi = "abi/3.0"
	if macroExists(abi) {
		p.abi = abi
	}

	if macroExists("tunables/global") {
		p.imports = append(p.imports, "#include <tunables/global>")
	} else {
		p.imports = append(p.imports, "@{PROC}=/proc/")
	}
	if macroExists("abstractions/base") {
		p.innerImports = append(p.innerImports, "#include <abstractions/base>")
	}

	// Figure out the daemon profile.
	currentProfile, err := os.ReadFile("/proc/self/attr/current")
	if err != nil {
		// If we couldn't get the daemon profile, assume we are running
		// unconfined which is generally the default.
		currentProfile = nil
	}
	p.daemonProfile = cleanProfileName(string(currentProfile))

	// If we were running in Rootless mode, we could read `/proc/$(cat ${ROOTLESSKIT_STATE_DIR}/child_pid)/exe`,
	// but `nerdctl apparmor load` has to be executed as the root.
	// So, do not check ${ROOTLESSKIT_STATE_DIR} (nor EUID) here.
	p.rootlessKit, err = exec.LookPath("rootlesskit")
	if err != nil {
		log.L.WithError(err).Debug("apparmor: failed to determine the RootlessKit binary path")
		p.rootlessKit = ""
	}
	log.L.Debugf("apparmor: RootlessKit=%q", p.rootlessKit)

	return &p, nil
}

func generate(p *profileData, o io.Writer) error {
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
	out, err := exec.Command("apparmor_parser", args...).CombinedOutput() //nolint:gosec // G702: arguments are passed directly to a fixed executable.
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
