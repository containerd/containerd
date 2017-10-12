package main

const releaseNotes = `Welcome to the release of containerd {{.Version}}!
{{if .PreRelease}}
*This is a pre-release of containerd*
{{- end}}

{{.Preface}}

Please try out the release binaries and report any issues at
https://github.com/containerd/containerd/issues.

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

Previous release can be found at [{{.Previous}}](https://github.com/containerd/containerd/releases/tag/{{.Previous}})
{{range $dep := .Dependencies}}
* {{$dep.Previous}} -> {{$dep.Commit}} **{{$dep.Name}}**
{{- end}}
`
