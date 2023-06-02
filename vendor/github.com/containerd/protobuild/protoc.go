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

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

var (
	tmpl = template.Must(template.New("protoc").Parse(`protoc -I
	{{- range $index, $include := .Includes -}}
		{{if $index}}` + string(filepath.ListSeparator) + `{{end -}}
			{{.}}
	{{- end -}}
	{{- if .Descriptors}} --include_imports --descriptor_set_out={{.Descriptors}}{{- end -}}

	{{ if lt .Version 2 }}
		{{- range $index, $name := .Names }} --{{- $name -}}_out={{- $.GoOutV1 }}{{- end -}}
	{{- else -}}
		{{- range $gen := .Generators }} --{{- $gen.Name -}}_out={{- $gen.OutputDir }}
			{{- range $k, $v := $gen.Parameters }} --{{- $gen.Name -}}_opt={{- $k -}}={{- $v -}}{{- end -}}
		{{- end -}}
	{{- end -}}

	{{- range .Files}} {{.}}{{end -}}
`))
)

type generator struct {
	Name       string
	OutputDir  string
	Parameters map[string]string
}

// protocParams defines inputs to a protoc command string.
type protocCmd struct {
	Generators  []generator
	Includes    []string
	Descriptors string
	Files       []string
	// Version is Protobuild's version.
	Version int

	// V1 fields
	Names      []string
	OutputDir  string
	PackageMap map[string]string
	Plugins    []string
	ImportPath string
}

func (p *protocCmd) mkcmd() (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// GoOutV1 returns the parameter for --go_out= for protoc-gen-go < 1.4.0.
// Note that plugins and import_path are no longer supported by
// newer protoc-gen-go versions.
func (p *protocCmd) GoOutV1() string {
	var result string
	if len(p.Plugins) > 0 {
		result += "plugins=" + strings.Join(p.Plugins, "+") + ","
	}
	result += "import_path=" + p.ImportPath

	for proto, pkg := range p.PackageMap {
		result += fmt.Sprintf(",M%s=%s", proto, pkg)
	}
	result += ":" + p.OutputDir

	return result
}

func (p *protocCmd) run() error {
	arg, err := p.mkcmd()
	if err != nil {
		return err
	}

	// pass to sh -c so we don't need to re-split here.
	args := []string{shArg, arg}
	cmd := exec.Command(shCmd, args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	return cmd.Run()
}
