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

const (
	defaultTemplateFile = "TEMPLATE"
	releaseNotes        = `Welcome to the {{.Version}} release of {{.ProjectName}}!
{{if .PreRelease -}}
*This is a pre-release of {{.ProjectName}}*
{{- end}}

{{.Preface}}

Please try out the release binaries and report any issues at
https://github.com/{{.GithubRepo}}/issues.

{{range  $note := .Notes}}
### {{$note.Title}}

{{$note.Description}}
{{- end}}

### Contributors
{{range $contributor := .Contributors}}
* {{$contributor}}
{{- end}}

### Changes
{{range $change := .Changes}}
* {{$change.Commit}} {{$change.Description}}
{{- end}}

### Dependency Changes

Previous release can be found at [{{.Previous}}](https://github.com/{{.GithubRepo}}/releases/tag/{{.Previous}})
{{range $dep := .Dependencies}}
* {{$dep.Previous}} -> {{$dep.Commit}} **{{$dep.Name}}**
{{- end}}
`
)
