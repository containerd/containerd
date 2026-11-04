/*
Copyright The Kubernetes Authors.

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

package seccomp

import (
	"fmt"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"

	"sigs.k8s.io/security-profiles-merger/internal/merge"
)

// FormatProfile returns a human-readable representation of a seccomp profile.
func FormatProfile(profile *specs.LinuxSeccomp) string {
	if profile == nil {
		return "Profile{<nil>}"
	}

	var parts []string

	parts = append(parts, "default:"+merge.SafeText(string(profile.DefaultAction)))

	if profile.DefaultErrnoRet != nil {
		parts = append(parts, fmt.Sprintf("defaultErrno:%d", *profile.DefaultErrnoRet))
	}

	if len(profile.Architectures) > 0 {
		archs := make([]string, len(profile.Architectures))
		for idx, arch := range profile.Architectures {
			archs[idx] = merge.SafeText(string(arch))
		}

		parts = append(parts, "arch:"+strings.Join(archs, ","))
	}

	if profile.Flags != nil {
		parts = append(parts, formatFlags(profile.Flags))
	}

	if profile.ListenerPath != "" {
		parts = append(parts, "listener:"+merge.SafeText(profile.ListenerPath))

		if profile.ListenerMetadata != "" {
			parts = append(
				parts, "listenerMeta:"+merge.SafeText(profile.ListenerMetadata),
			)
		}
	}

	for _, sc := range profile.Syscalls {
		parts = append(parts, formatSyscall(sc))
	}

	return fmt.Sprintf("Profile{%s}", strings.Join(parts, " "))
}

// formatFlags renders a flag list that is set. An empty one reads "none"
// rather than nothing: it turns off the flags a runtime sets for a profile
// without a list, so it is not the same profile.
func formatFlags(flags []specs.LinuxSeccompFlag) string {
	if len(flags) == 0 {
		return "flags:none"
	}

	parts := make([]string, len(flags))
	for idx, flag := range flags {
		parts[idx] = merge.SafeText(string(flag))
	}

	return "flags:" + strings.Join(parts, ",")
}

// String returns a human-readable representation of the syscall entry.
func (e SyscallEntry) String() string {
	action := merge.SafeText(string(e.Action))
	if e.ErrnoRet != nil {
		action = fmt.Sprintf("%s(errno:%d)", action, *e.ErrnoRet)
	}

	if len(e.Args) == 0 {
		return merge.SafeText(e.Name) + "->" + action
	}

	return fmt.Sprintf("%s(%s)->%s", merge.SafeText(e.Name), formatArgs(e.Args), action)
}

// String returns a human-readable representation of the syscall detail.
func (d SyscallDetail) String() string {
	action := merge.SafeText(string(d.Action))
	if d.ErrnoRet != nil {
		action = fmt.Sprintf("%s(errno:%d)", action, *d.ErrnoRet)
	}

	if len(d.Args) == 0 {
		return action
	}

	return fmt.Sprintf("(%s)->%s", formatArgs(d.Args), action)
}

func formatArgs(args []specs.LinuxSeccompArg) string {
	parts := make([]string, len(args))

	for idx, arg := range args {
		if arg.Op == specs.OpMaskedEqual {
			parts[idx] = fmt.Sprintf(
				"[%d]%s:%d:%d",
				arg.Index, merge.SafeText(string(arg.Op)), arg.Value, arg.ValueTwo,
			)
		} else {
			parts[idx] = fmt.Sprintf(
				"[%d]%s:%d", arg.Index, merge.SafeText(string(arg.Op)), arg.Value,
			)
		}
	}

	return strings.Join(parts, ",")
}

func formatSyscall(syscall specs.LinuxSyscall) string {
	names := strings.Join(merge.SafeTexts(syscall.Names), ",")
	action := merge.SafeText(string(syscall.Action))

	if syscall.ErrnoRet != nil {
		action = fmt.Sprintf("%s(errno:%d)", action, *syscall.ErrnoRet)
	}

	if len(syscall.Args) == 0 {
		return names + "->" + action
	}

	return fmt.Sprintf("%s(%s)->%s", names, formatArgs(syscall.Args), action)
}
