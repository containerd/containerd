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
  deny @{PROC}/{[^1-9],[^1-9][^0-9],[^1-9s][^0-9y][^0-9s],[^1-9][^0-9][^0-9][^0-9]*}/** w,
  deny @{PROC}/sys/[^k]** w,  # deny /proc/sys except /proc/sys/k* (effectively /proc/sys/kernel)
  deny @{PROC}/sys/kernel/{?,??,[^s][^h][^m]**} w,  # deny everything except shm* in /proc/sys/kernel/
  deny @{PROC}/sysrq-trigger rwklx,
  deny @{PROC}/mem rwklx,
  deny @{PROC}/kmem rwklx,
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

type data struct {
	Name          string
	Imports       []string
	InnerImports  []string
	DaemonProfile string
	RootlessKit   string
}

func cleanProfileName(profile string) string {
	// Normally profiles are suffixed by " (enforce)". AppArmor profiles cannot
	// contain spaces so this doesn't restrict daemon profile names.
	profile, _, _ = strings.Cut(profile, " ")
	if profile == "" {
		profile = "unconfined"
	}
	return profile
}

func loadData(name string) (*data, error) {
	p := data{
		Name: name,
	}

	if macroExists("tunables/global") {
		p.Imports = append(p.Imports, "#include <tunables/global>")
	} else {
		p.Imports = append(p.Imports, "@{PROC}=/proc/")
	}
	if macroExists("abstractions/base") {
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

// macrosExists checks if the passed macro exists.
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
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		p, err := r.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, err
		}
		if strings.HasPrefix(p, name+" ") {
			return true, nil
		}
	}
	return false, nil
}
