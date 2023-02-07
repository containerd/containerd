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

package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/defaults"
	"github.com/urfave/cli"
)

var (
	// SnapshotterFlags are cli flags specifying snapshotter names
	SnapshotterFlags = []cli.Flag{
		cli.StringFlag{
			Name:   "snapshotter",
			Usage:  "snapshotter name. Empty value stands for the default value.",
			EnvVar: "CONTAINERD_SNAPSHOTTER",
		},
	}

	// SnapshotterLabels are cli flags specifying labels which will be added to the new snapshot for container.
	SnapshotterLabels = cli.StringSliceFlag{
		Name:  "snapshotter-label",
		Usage: "Labels added to the new snapshot for this container.",
	}

	// LabelFlag is a cli flag specifying labels
	LabelFlag = cli.StringSliceFlag{
		Name:  "label",
		Usage: "Labels to attach to the image",
	}

	// RegistryFlags are cli flags specifying registry options
	RegistryFlags = []cli.Flag{
		cli.BoolFlag{
			Name:  "skip-verify,k",
			Usage: "Skip SSL certificate validation",
		},
		cli.BoolFlag{
			Name:  "plain-http",
			Usage: "Allow connections using plain HTTP",
		},
		cli.StringFlag{
			Name:  "user,u",
			Usage: "User[:password] Registry user and password",
		},
		cli.StringFlag{
			Name:  "refresh",
			Usage: "Refresh token for authorization server",
		},
		cli.StringFlag{
			Name: "hosts-dir",
			// compatible with "/etc/docker/certs.d"
			Usage: "Custom hosts configuration directory",
		},
		cli.StringFlag{
			Name:  "tlscacert",
			Usage: "Path to TLS root CA",
		},
		cli.StringFlag{
			Name:  "tlscert",
			Usage: "Path to TLS client certificate",
		},
		cli.StringFlag{
			Name:  "tlskey",
			Usage: "Path to TLS client key",
		},
		cli.BoolFlag{
			Name:  "http-dump",
			Usage: "Dump all HTTP request/responses when interacting with container registry",
		},
		cli.BoolFlag{
			Name:  "http-trace",
			Usage: "Enable HTTP tracing for registry interactions",
		},
	}

	// ContainerFlags are cli flags specifying container options
	ContainerFlags = []cli.Flag{
		cli.StringFlag{
			Name:  "config,c",
			Usage: "Path to the runtime-specific spec config file",
		},
		cli.StringFlag{
			Name:  "cwd",
			Usage: "Specify the working directory of the process",
		},
		cli.StringSliceFlag{
			Name:  "env",
			Usage: "Specify additional container environment variables (e.g. FOO=bar)",
		},
		cli.StringFlag{
			Name:  "env-file",
			Usage: "Specify additional container environment variables in a file(e.g. FOO=bar, one per line)",
		},
		cli.StringSliceFlag{
			Name:  "label",
			Usage: "Specify additional labels (e.g. foo=bar)",
		},
		cli.StringSliceFlag{
			Name:  "annotation",
			Usage: "Specify additional OCI annotations (e.g. foo=bar)",
		},
		cli.StringSliceFlag{
			Name:  "mount",
			Usage: "Specify additional container mount (e.g. type=bind,src=/tmp,dst=/host,options=rbind:ro)",
		},
		cli.BoolFlag{
			Name:  "net-host",
			Usage: "Enable host networking for the container",
		},
		cli.BoolFlag{
			Name:  "privileged",
			Usage: "Run privileged container",
		},
		cli.BoolFlag{
			Name:  "read-only",
			Usage: "Set the containers filesystem as readonly",
		},
		cli.StringFlag{
			Name:  "runtime",
			Usage: "Runtime name or absolute path to runtime binary",
			Value: defaults.DefaultRuntime,
		},
		cli.StringFlag{
			Name:  "runtime-config-path",
			Usage: "Optional runtime config path",
		},
		cli.BoolFlag{
			Name:  "tty,t",
			Usage: "Allocate a TTY for the container",
		},
		cli.StringSliceFlag{
			Name:  "with-ns",
			Usage: "Specify existing Linux namespaces to join at container runtime (format '<nstype>:<path>')",
		},
		cli.StringFlag{
			Name:  "pid-file",
			Usage: "File path to write the task's pid",
		},
		cli.IntSliceFlag{
			Name:  "gpus",
			Usage: "Add gpus to the container",
		},
		cli.BoolFlag{
			Name:  "allow-new-privs",
			Usage: "Turn off OCI spec's NoNewPrivileges feature flag",
		},
		cli.Uint64Flag{
			Name:  "memory-limit",
			Usage: "Memory limit (in bytes) for the container",
		},
		cli.StringSliceFlag{
			Name:  "cap-add",
			Usage: "Add Linux capabilities (Set capabilities with 'CAP_' prefix)",
		},
		cli.StringSliceFlag{
			Name:  "cap-drop",
			Usage: "Drop Linux capabilities (Set capabilities with 'CAP_' prefix)",
		},
		cli.BoolFlag{
			Name:  "seccomp",
			Usage: "Enable the default seccomp profile",
		},
		cli.StringFlag{
			Name:  "seccomp-profile",
			Usage: "File path to custom seccomp profile. seccomp must be set to true, before using seccomp-profile",
		},
		cli.StringFlag{
			Name:  "apparmor-default-profile",
			Usage: "Enable AppArmor with the default profile with the specified name, e.g. \"cri-containerd.apparmor.d\"",
		},
		cli.StringFlag{
			Name:  "apparmor-profile",
			Usage: "Enable AppArmor with an existing custom profile",
		},
		cli.StringFlag{
			Name:  "blockio-config-file",
			Usage: "File path to blockio class definitions. By default class definitions are not loaded.",
		},
		cli.StringFlag{
			Name:  "blockio-class",
			Usage: "Name of the blockio class to associate the container with",
		},
		cli.StringFlag{
			Name:  "rdt-class",
			Usage: "Name of the RDT class to associate the container with. Specifies a Class of Service (CLOS) for cache and memory bandwidth management.",
		},
		cli.StringFlag{
			Name:  "hostname",
			Usage: "Set the container's host name",
		},
		cli.StringFlag{
			Name:  "user,u",
			Usage: "Username or user id, group optional (format: <name|uid>[:<group|gid>])",
		},
	}
)

// ObjectWithLabelArgs returns the first arg and a LabelArgs object
func ObjectWithLabelArgs(clicontext *cli.Context) (string, map[string]string) {
	var (
		first        = clicontext.Args().First()
		labelStrings = clicontext.Args().Tail()
	)

	return first, LabelArgs(labelStrings)
}

// LabelArgs returns a map of label key,value pairs
func LabelArgs(labelStrings []string) map[string]string {
	labels := make(map[string]string, len(labelStrings))
	for _, label := range labelStrings {
		key, value, ok := strings.Cut(label, "=")
		if !ok {
			value = "true"
		}
		labels[key] = value
	}

	return labels
}

// AnnotationArgs returns a map of annotation key,value pairs.
func AnnotationArgs(annoStrings []string) (map[string]string, error) {
	annotations := make(map[string]string, len(annoStrings))
	for _, anno := range annoStrings {
		key, value, ok := strings.Cut(anno, "=")
		if !ok {
			return nil, fmt.Errorf("invalid key=value format annotation: %v", anno)
		}
		annotations[key] = value
	}
	return annotations, nil
}

// PrintAsJSON prints input in JSON format
func PrintAsJSON(x interface{}) {
	b, err := json.MarshalIndent(x, "", "    ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "can't marshal %+v as a JSON string: %v\n", x, err)
	}
	fmt.Println(string(b))
}

// WritePidFile writes the pid atomically to a file
func WritePidFile(path string, pid int) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	tempPath := filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s", filepath.Base(path)))
	f, err := os.OpenFile(tempPath, os.O_RDWR|os.O_CREATE|os.O_EXCL|os.O_SYNC, 0666)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%d", pid)
	f.Close()
	if err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
